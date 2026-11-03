package reminders

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/database/reminders"
	"ReZeroTsu/internal/discord"
	"ReZeroTsu/internal/interaction"
	"ReZeroTsu/internal/logger"

	"github.com/bwmarrin/discordgo"
)

const (
	embedTitleReminders = "Reminders"
	hintRemoveRemind    = "Use the menu below, or /reminder remove with the ID, to delete a reminder."

	msgNoReminders = "No saved reminders for you found."
	msgLoadFailed  = "Could not load your reminders."

	// removePickerPrefix starts the custom ID of the Remove picker: <prefix><owner user ID>:<page>.
	removePickerPrefix = "reminder:rm:"

	msgOnlyOwnerRemoves = "Only the member whose reminders these are can remove them."
	msgInvalidPicker    = "Invalid menu. Run /reminder list again."
)

// timeHintChoices are autocomplete hints for the /reminder set time option.
var timeHintChoices = []string{
	"1 minute", "5 minutes", "15 minutes", "30 minutes",
	"1 hour", "1 day", "1 week",
	"2 weeks 1 day 12 hours 30 minutes",
}

// store is the part of the reminders store the commands use. It has no whole-document rewrite, so a command can
// never undo a removal made by a concurrent delivery run.
type store interface {
	GetByID(ctx context.Context, id string) (*reminders.RemindMeSlice, error)
	Append(ctx context.Context, id string, isGuild bool, rm reminders.RemindMe) error
	RemoveByRemindIDs(ctx context.Context, id string, remindIDs []int) error
	RemoveByRefs(ctx context.Context, id string, refs []reminders.RemindRef) error
}

// storeOf picks the store of a database client; tests replace it.
var storeOf = func(db *database.Client) store { return db.Reminders() }

func init() {
	commands.Add(&commands.Command{
		Visibility: commands.Private,
		Response:   commands.Deferred,
		Name:       "reminder",
		Desc:       "Set, list and remove your reminders.",
		Permission: commands.PermEveryone,
		Module:     "general",
		Installs:   []discordgo.ApplicationIntegrationType{discordgo.ApplicationIntegrationGuildInstall, discordgo.ApplicationIntegrationUserInstall},
		Subcommands: []*commands.Subcommand{
			{
				Name: "set",
				Desc: "Set a reminder; you'll get a DM at the given time.",
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:         discordgo.ApplicationCommandOptionString,
						Name:         "time",
						Description:  "Amount of time to wait. Format: 2w1d12h30m (weeks, days, hours, minutes).",
						Required:     true,
						Autocomplete: true,
					},
					{
						Type:        discordgo.ApplicationCommandOptionString,
						Name:        "message",
						Description: "The message you want to be sent.",
						Required:    true,
					},
				},
				Run:                runSet,
				AutocompleteOption: autocompleteRemindTime,
			},
			{
				Name: "list",
				Desc: "List your reminders.",
				Run:  runList,
			},
			{
				Name: "remove",
				Desc: "Remove a reminder by ID.",
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:         discordgo.ApplicationCommandOptionInteger,
						Name:         "id",
						Description:  "The reminder to remove.",
						Required:     true,
						Autocomplete: true,
					},
				},
				Run:                runRemove,
				AutocompleteOption: autocompleteRemindID,
			},
		},
	})
	// A reminder list is the member's own: only they page it (a restricted renderer refuses everyone else).
	commands.RegisterPaginationRenderer("reminders", RenderRemindersPage)
	commands.AddComponent(removePickerPrefix, handleRemovePick)

	// The DM prefix commands stay until ticket 3.7 retires them.
	commands.AddMessageCommand(&commands.MessageCommand{
		Name:       "remind",
		Aliases:    []string{"setremind", "addremind", "remindme", "remind-me", "add-remind"},
		Permission: commands.PermEveryone,
		Context:    commands.ContextDMOnly,
		MessageHandler: func(ctx context.Context, db *database.Client, s *discordgo.Session, m *discordgo.MessageCreate, args string, runtime commands.BotRuntime) {
			parts := strings.SplitN(strings.TrimSpace(args), " ", 2)
			timeStr := ""
			msg := ""
			if len(parts) >= 1 {
				timeStr = strings.TrimSpace(parts[0])
			}
			if len(parts) >= 2 {
				msg = strings.TrimSpace(parts[1])
			}
			desc, logErr := addRemindCore(ctx, storeOf(db), m.Author.ID, m.GuildID, m.ChannelID, timeStr, msg)
			if logErr != nil {
				commands.LogToBotLog(ctx, db, s, m.GuildID, fmt.Sprintf("[reminders] %v", logErr))
			}
			emb := commands.NewEmbed(s, embedTitleReminders, desc)
			_, _ = s.ChannelMessageSendEmbed(m.ChannelID, emb)
		},
	})
	commands.AddMessageCommand(&commands.MessageCommand{
		Name:       "remove-remind",
		Aliases:    []string{"removeremindme", "deleteremind", "deleteremindme", "killremind", "stopremind", "removeremind"},
		Permission: commands.PermEveryone,
		Context:    commands.ContextDMOnly,
		MessageHandler: func(ctx context.Context, db *database.Client, s *discordgo.Session, m *discordgo.MessageCreate, args string, runtime commands.BotRuntime) {
			id := 0
			if f := strings.Fields(strings.TrimSpace(args)); len(f) > 0 {
				id, _ = strconv.Atoi(f[0])
			}
			desc, logErr := removeRemindCore(ctx, storeOf(db), m.Author.ID, id)
			if logErr != nil {
				commands.LogToBotLog(ctx, db, s, m.GuildID, fmt.Sprintf("[reminders] %v", logErr))
			}
			emb := commands.NewEmbed(s, embedTitleReminders, desc)
			_, _ = s.ChannelMessageSendEmbed(m.ChannelID, emb)
		},
	})
	commands.AddMessageCommand(&commands.MessageCommand{
		Name:       "reminders",
		Aliases:    []string{"viewremindmes", "viewremindme", "viewremind", "viewreminds", "remindmes"},
		Permission: commands.PermEveryone,
		Context:    commands.ContextDMOnly,
		MessageHandler: func(ctx context.Context, db *database.Client, s *discordgo.Session, m *discordgo.MessageCreate, args string, runtime commands.BotRuntime) {
			uid := m.Author.ID
			slice, err := storeOf(db).GetByID(ctx, uid)
			if err != nil {
				commands.LogToBotLog(ctx, db, s, m.GuildID, fmt.Sprintf("[reminders] GetByID %s: %v", uid, err))
				emb := commands.NewEmbed(s, embedTitleReminders, msgLoadFailed)
				_, _ = s.ChannelMessageSendEmbed(m.ChannelID, emb)
				return
			}
			if slice == nil || len(slice.Reminders) == 0 {
				emb := commands.NewEmbed(s, embedTitleReminders, msgNoReminders)
				_, _ = s.ChannelMessageSendEmbed(m.ChannelID, emb)
				return
			}
			page := listPage(slice, 0, uid)
			_, _ = s.ChannelMessageSendComplex(m.ChannelID, &discordgo.MessageSend{Embeds: page.Embeds, Components: page.Components})
		},
	})
}

func autocompleteRemindTime(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate, optionName, focusedValue string) []*discordgo.ApplicationCommandOptionChoice {
	var choices []*discordgo.ApplicationCommandOptionChoice
	for _, hint := range commands.MatchAnywhere(timeHintChoices, focusedValue, func(h string) []string { return []string{h} }) {
		choices = append(choices, &discordgo.ApplicationCommandOptionChoice{Name: hint, Value: hint})
	}
	return choices
}

func autocompleteRemindID(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate, optionName, focusedValue string) []*discordgo.ApplicationCommandOptionChoice {
	return remindIDChoices(ctx, storeOf(db), commands.UserID(i), focusedValue)
}

// clip cuts text to at most n characters, ending with "..." when it was cut.
func clip(text string, n int) string {
	r := []rune(text)
	if len(r) <= n {
		return text
	}
	return string(r[:n-3]) + "..."
}

// remindIDChoices suggests the user's own reminders by ID and text, matching the typed text anywhere in either.
func remindIDChoices(ctx context.Context, st store, uid, typed string) []*discordgo.ApplicationCommandOptionChoice {
	slice, err := st.GetByID(ctx, uid)
	if err != nil || slice == nil || len(slice.Reminders) == 0 {
		return nil
	}
	sorted := append([]reminders.RemindMe(nil), slice.Reminders...)
	sort.Slice(sorted, func(a, b int) bool { return sorted[a].RemindID < sorted[b].RemindID })
	filtered := commands.MatchAnywhere(sorted, typed, func(r reminders.RemindMe) []string {
		return []string{strconv.Itoa(r.RemindID), r.Message}
	})
	choices := make([]*discordgo.ApplicationCommandOptionChoice, 0, len(filtered))
	for _, r := range filtered {
		name := clip(fmt.Sprintf("%d: %s", r.RemindID, clip(r.Message, 50)), 100)
		choices = append(choices, &discordgo.ApplicationCommandOptionChoice{Name: name, Value: r.RemindID})
	}
	return choices
}

// addRemindCore validates and stores one reminder and returns the text to show. Its guards are the old
// /remind ones: both fields present, a parseable non-zero time, the next ID after the highest the user has.
func addRemindCore(ctx context.Context, st store, uid, guildID, channelID, timeStr, msg string) (responseDesc string, logErr error) {
	if timeStr == "" || msg == "" {
		return "Please provide time and message.", nil
	}
	targetTime, perma, err := commands.ResolveTimeFromString(timeStr)
	if err != nil {
		return "Invalid time given.", nil
	}
	if perma {
		return "Cannot use that time. Please use another.", nil
	}
	slice, err := st.GetByID(ctx, uid)
	if err != nil {
		return msgLoadFailed, err
	}
	maxID := 0
	if slice != nil {
		for _, r := range slice.Reminders {
			if r.RemindID > maxID {
				maxID = r.RemindID
			}
		}
	}
	maxID++
	// Atomic append: never rewrites the document, so a concurrent delivery run's removals are not lost.
	if err := st.Append(ctx, uid, guildID != "", reminders.RemindMe{
		Message:        msg,
		Date:           targetTime,
		CommandChannel: channelID,
		Origin:         reminders.OriginFor(guildID),
		RemindID:       maxID,
		CreatedAt:      time.Now(),
	}); err != nil {
		return "Could not save reminder.", err
	}
	unix := targetTime.UTC().Unix()
	return fmt.Sprintf("Success! You will be reminded of the message on <t:%d:F> (<t:%d:R>). Make sure your DMs are open.", unix, unix), nil
}

// removeRemindCore removes the user's reminder with the given ID and returns the text to show.
func removeRemindCore(ctx context.Context, st store, uid string, id int) (responseDesc string, logErr error) {
	if id <= 0 {
		return "Invalid ID.", nil
	}
	slice, err := st.GetByID(ctx, uid)
	if err != nil {
		return msgLoadFailed, err
	}
	if slice == nil || len(slice.Reminders) == 0 {
		return msgNoReminders, nil
	}
	found := false
	for _, r := range slice.Reminders {
		if r.RemindID == id {
			found = true
			break
		}
	}
	if !found {
		return "No reminder found with that ID.", nil
	}
	if err := st.RemoveByRemindIDs(ctx, uid, []int{id}); err != nil {
		return "Could not remove reminder.", err
	}
	return fmt.Sprintf("Success! Deleted reminder with ID `%d`.", id), nil
}

// show fills in the reply: the placeholder of a deferred command, or the first reply of a direct one.
func show(ic *interaction.Context, r interaction.Response) error {
	if ic.Acknowledged() {
		return ic.EditReply(r)
	}
	return ic.Reply(r)
}

func showText(ic *interaction.Context, desc string) error {
	return show(ic, interaction.Response{Embeds: []*discordgo.MessageEmbed{commands.NewEmbed(nil, embedTitleReminders, desc)}})
}

func logFailure(ic *interaction.Context, err error) {
	if err != nil {
		logger.For("reminders").Error("reminder command failed", "user_id", ic.Invoker.ID, "guild_id", ic.GuildID, "err", err)
	}
}

func stringOption(ic *interaction.Context, name string) string {
	if o := commands.ParseOption(ic.Command.Options, name); o != nil {
		return o.StringValue()
	}
	return ""
}

func runSet(ic *interaction.Context) error {
	desc, logErr := addRemindCore(ic.Context(), storeOf(ic.DB), ic.Invoker.ID, ic.GuildID, ic.ChannelID, stringOption(ic, "time"), stringOption(ic, "message"))
	logFailure(ic, logErr)
	return showText(ic, desc)
}

func runRemove(ic *interaction.Context) error {
	opt := commands.ParseOption(ic.Command.Options, "id")
	if opt == nil {
		return showText(ic, "Please provide the reminder ID.")
	}
	desc, logErr := removeRemindCore(ic.Context(), storeOf(ic.DB), ic.Invoker.ID, int(opt.IntValue()))
	logFailure(ic, logErr)
	return showText(ic, desc)
}

func runList(ic *interaction.Context) error {
	uid := ic.Invoker.ID
	slice, err := storeOf(ic.DB).GetByID(ic.Context(), uid)
	if err != nil {
		logFailure(ic, err)
		return showText(ic, msgLoadFailed)
	}
	if slice == nil || len(slice.Reminders) == 0 {
		return showText(ic, msgNoReminders)
	}
	return show(ic, listPage(slice, 0, uid))
}

// pageCount is how many pages n reminders take.
func pageCount(n int) int {
	return max((n+discord.MaxEmbedFields-1)/discord.MaxEmbedFields, 1)
}

func reminderFieldsForPage(rs []reminders.RemindMe, start int) []*discordgo.MessageEmbedField {
	fields := make([]*discordgo.MessageEmbedField, 0, discord.MaxEmbedFields)
	end := min(start+discord.MaxEmbedFields, len(rs))
	for j := start; j < end; j++ {
		r := rs[j]
		dueTs := r.Date.UTC().Unix()
		createdLine := fmt.Sprintf("<t:%d:R>", r.CreatedAt.UTC().Unix())
		if r.CreatedAt.IsZero() {
			createdLine = "—"
		}
		value := fmt.Sprintf("**Message:** %s\n**Created:** %s\n**Due:** <t:%d:R>", clip(r.Message, 200), createdLine, dueTs)
		fields = append(fields, &discordgo.MessageEmbedField{
			Name:   fmt.Sprintf("ID %d", r.RemindID),
			Value:  value,
			Inline: false,
		})
	}
	return fields
}

// removePickerID is the custom ID of the Remove picker on page of uid's list. It names the list's owner, the only
// member the picker works for.
func removePickerID(uid string, page int) string {
	return removePickerPrefix + uid + ":" + strconv.Itoa(page)
}

// pickValue is the value of a reminder's picker entry: its ID and due time, so a reminder that took a removed
// one's ID is never removed by a stale pick.
func pickValue(r reminders.RemindMe) string {
	return strconv.Itoa(r.RemindID) + ":" + strconv.FormatInt(r.Date.UTC().Unix(), 10)
}

func parsePickValue(v string) (id int, unix int64, ok bool) {
	idText, unixText, found := strings.Cut(v, ":")
	if !found {
		return 0, 0, false
	}
	id, err := strconv.Atoi(idText)
	if err != nil {
		return 0, 0, false
	}
	unix, err = strconv.ParseInt(unixText, 10, 64)
	return id, unix, err == nil
}

// listPage renders one page (0-based, clamped) of a non-empty list: the embed, the Remove picker for the page's
// entries, and the paging buttons when the list has more than one page.
func listPage(slice *reminders.RemindMeSlice, page int, ownerID string) interaction.Response {
	n := len(slice.Reminders)
	totalPages := pageCount(n)
	page = max(min(page, totalPages-1), 0)
	start := page * discord.MaxEmbedFields
	fields := reminderFieldsForPage(slice.Reminders, start)
	end := start + len(fields)
	desc := fmt.Sprintf("Page %d of %d. Showing %d–%d of %d.", page+1, totalPages, start+1, end, n)

	options := make([]discordgo.SelectMenuOption, 0, len(fields))
	for _, r := range slice.Reminders[start:end] {
		options = append(options, discordgo.SelectMenuOption{
			Label: clip(fmt.Sprintf("ID %d: %s", r.RemindID, r.Message), 100),
			Value: pickValue(r),
		})
	}
	picker := discordgo.ActionsRow{Components: []discordgo.MessageComponent{discordgo.SelectMenu{
		MenuType:    discordgo.StringSelectMenu,
		CustomID:    removePickerID(ownerID, page),
		Placeholder: "Remove a reminder",
		Options:     options,
	}}}
	components := []discordgo.MessageComponent{picker}
	if n > discord.MaxEmbedFields {
		components = append(components, commands.PaginationComponents("reminders", page, n, ownerID)...)
	}
	emb := commands.NewEmbedWithFields(nil, embedTitleReminders, desc, fields, hintRemoveRemind)
	return interaction.Response{Embeds: []*discordgo.MessageEmbed{emb}, Components: components}
}

// emptyList is what replaces a list whose last reminder was just removed; the empty component list takes the
// picker and buttons off the message.
func emptyList() interaction.Response {
	return interaction.Response{
		Embeds:     []*discordgo.MessageEmbed{commands.NewEmbed(nil, embedTitleReminders, msgNoReminders)},
		Components: []discordgo.MessageComponent{},
	}
}

// handleRemovePick removes the reminder chosen in the Remove picker and re-renders the page. Guard home: only the
// member whose list it is may remove from it, checked before anything is read or removed.
func handleRemovePick(ic *interaction.Context, _ *discordgo.Session) error {
	rest := strings.TrimPrefix(ic.CustomID, removePickerPrefix)
	ownerID, pageText, ok := strings.Cut(rest, ":")
	page, err := strconv.Atoi(pageText)
	if !ok || ownerID == "" || err != nil || len(ic.Values) != 1 {
		return ic.Refuse(msgInvalidPicker)
	}
	if ic.Invoker.ID != ownerID {
		return ic.Refuse(msgOnlyOwnerRemoves)
	}
	id, unix, ok := parsePickValue(ic.Values[0])
	if !ok {
		return ic.Refuse(msgInvalidPicker)
	}
	st := storeOf(ic.DB)
	ctx := ic.Context()
	slice, err := st.GetByID(ctx, ownerID)
	if err != nil {
		logFailure(ic, err)
		return ic.Refuse(msgLoadFailed)
	}
	if slice != nil {
		for _, r := range slice.Reminders {
			if r.RemindID != id || r.Date.UTC().Unix() != unix {
				continue
			}
			if err := st.RemoveByRefs(ctx, ownerID, []reminders.RemindRef{{RemindID: r.RemindID, Date: r.Date}}); err != nil {
				logFailure(ic, err)
				return ic.Refuse("Could not remove reminder.")
			}
			break
		}
	}
	slice, err = st.GetByID(ctx, ownerID)
	if err != nil {
		logFailure(ic, err)
		return ic.Refuse(msgLoadFailed)
	}
	if slice == nil || len(slice.Reminders) == 0 {
		return ic.UpdateMessage(emptyList())
	}
	return ic.UpdateMessage(listPage(slice, page, ownerID))
}

// RenderRemindersPage renders one page of reminders for pagination. The pagination handler only lets the list's
// owner reach it, because it is registered as a restricted renderer.
func RenderRemindersPage(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate, page, total int, authorID string) commands.Page {
	if authorID == "" {
		return commands.Page{Refusal: "Could not load reminders."}
	}
	slice, err := storeOf(db).GetByID(ctx, authorID)
	if err != nil {
		logger.For("reminders").Error("pagination load failed", "user_id", authorID, "err", err)
		return commands.Page{Refusal: msgLoadFailed}
	}
	if slice == nil || len(slice.Reminders) == 0 {
		return commands.Page{Refusal: msgNoReminders}
	}
	return commands.Page{Response: listPage(slice, page, authorID)}
}
