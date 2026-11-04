package animesubs

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"ReZeroTsu/internal/animeschedule"
	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/database/anime_subs"
	"ReZeroTsu/internal/discord"
	"ReZeroTsu/internal/interaction"
	"ReZeroTsu/internal/logger"

	"github.com/bwmarrin/discordgo"
)

const (
	// pageKey names the subscription list's page renderer.
	pageKey = "subscription"
	// pickerPrefix starts the custom ID of the Remove picker: subscription-rm:<authorID>:<page>.
	pickerPrefix = "subscription-rm:"

	// clearPrefix starts the custom ID of the clear confirmation buttons: subscription-clear:<authorID>:<yes|no>.
	clearPrefix = "subscription-clear:"
	clearYes    = "yes"
	clearNo     = "no"

	msgOnlyOwnerClears = "Only the member whose list this is can clear it."
	msgStaleClear      = "Could not read that choice. Run /subscription clear again."

	msgNoSubscriptions  = "You have no active anime subscriptions."
	msgOnlyOwnerRemoves = "Only the member whose list this is can remove from it."
	msgStalePicker      = "Could not read that choice. Run /subscription list again."
	maxChoiceLength     = 100
)

// storeOf is the subscription store behind the commands; tests replace it.
var storeOf = func(db *database.Client) subsStore { return db.AnimeSubs() }

func init() {
	commands.Add(&commands.Command{
		Name:       "subscription",
		Desc:       "Get a DM when a new episode of an anime airs.",
		Visibility: commands.Private,
		Response:   commands.Deferred,
		Permission: commands.PermEveryone,
		Context:    commands.ContextBoth,
		Module:     "schedule",
		Installs:   commands.BothInstalls(),
		Subcommands: []*commands.Subcommand{
			{
				Name: "add",
				Desc: "Subscribe to DMs for an anime's sub, dub or raw; the reply says when it airs next.",
				Options: []*discordgo.ApplicationCommandOption{{
					Type:         discordgo.ApplicationCommandOptionString,
					Name:         "anime",
					Description:  "An airing or upcoming anime: any part of its title, romaji, English or native name",
					Required:     true,
					Autocomplete: true,
				}, {
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        optTrack,
					Description: "Which airings alert you: Sub (default, raw when a week has no sub), Dub, Raw, or several",
					Choices:     trackOptionChoices,
				}},
				Run:                runAdd,
				AutocompleteOption: autocompleteAdd,
			},
			{
				Name: "remove",
				Desc: "Stop getting DMs for an anime.",
				Options: []*discordgo.ApplicationCommandOption{{
					Type:         discordgo.ApplicationCommandOptionString,
					Name:         "anime",
					Description:  "One of your subscriptions",
					Required:     true,
					Autocomplete: true,
				}},
				Run:                runRemove,
				AutocompleteOption: autocompleteOwn,
			},
			{Name: "list", Desc: "Show your subscriptions and when each airs next.", Run: runList},
			{Name: "clear", Desc: "Remove all your subscriptions, after asking first.", Run: runClear},
		},
	})
	// Restricted: a list belongs to its author, so another member's press is refused, not given a copy.
	commands.RegisterPaginationRenderer(pageKey, RenderSubscriptionPage)
	commands.AddComponent(pickerPrefix, handleRemovePicker)
	commands.AddComponent(clearPrefix, handleClearButton)
}

// answer replaces the deferred placeholder with a titled embed.
func answer(ic *interaction.Context, desc string) error {
	return ic.EditReply(interaction.Response{Embeds: []*discordgo.MessageEmbed{commands.NewEmbed(nil, embedTitleAnimeSubs, desc)}})
}

// finish shows a core's message and logs the error it carries. A failure is logged here, with the invoker's
// context, because the new form has no session to post to the server's bot log.
func finish(ic *interaction.Context, desc string, logErr error) error {
	if logErr != nil {
		logger.For("animesubs").Error("subscription command failed", "guild_id", ic.GuildID, "err", logErr)
	}
	return answer(ic, desc)
}

// tracksArg is the subscription tracks the invoker chose; Sub when the option is unset.
func tracksArg(ic *interaction.Context) animeschedule.SubscriptionTracks {
	if opt := commands.ParseOption(ic.Command.Options, optTrack); opt != nil {
		if set, ok := animeschedule.ParseSubscriptionTracks(opt.StringValue()); ok {
			return set
		}
	}
	return animeschedule.SubscribeSub
}

// guildSettings reads a server's settings; tests replace it.
var guildSettings = commands.GetSettings

// donghuaHidden reports whether donghua is left out of what /subscription add offers: in a server the bot is a member
// of whose donghua setting is off. It uses the rule /schedule uses (commands.ServerSettings), so a DM, a user install
// in a server the bot has not joined, and an unreadable setting all offer donghua.
func donghuaHidden(ctx context.Context, db *database.Client, i *discordgo.Interaction) bool {
	shown, _ := commands.ServerSettings(ctx, db, i, guildSettings)
	return !shown
}

func animeArg(ic *interaction.Context) string {
	if opt := commands.ParseOption(ic.Command.Options, "anime"); opt != nil {
		return opt.StringValue()
	}
	return ""
}

// runAdd keeps today's rules through subCore: the show must be on the loaded weeks or the upcoming list, one
// subscription per show (ADR-0002), the subscription limit, and seeding from today's aired entry.
func runAdd(ic *interaction.Context) error {
	desc, confirmed, logErr := subCore(ic.Context(), storeOf(ic.DB), ic.Invoker.ID, animeArg(ic), tracksArg(ic), donghuaHidden(ic.Context(), ic.DB, ic.Interaction))
	if !confirmed {
		return finish(ic, desc, logErr)
	}
	return ic.EditReply(interaction.Response{Embeds: []*discordgo.MessageEmbed{commands.NewEmbed(nil, embedTitleAnimeSubs, desc, msgDMsFooter)}})
}

func runRemove(ic *interaction.Context) error {
	desc, logErr := unsubCore(ic.Context(), storeOf(ic.DB), ic.Invoker.ID, animeArg(ic))
	return finish(ic, desc, logErr)
}

func runClear(ic *interaction.Context) error {
	list, err := loadList(ic.Context(), storeOf(ic.DB), ic.Invoker.ID)
	if err != nil {
		return finish(ic, "Could not load your subscriptions.", err)
	}
	if len(list) == 0 {
		return answer(ic, msgNoSubscriptions)
	}
	question := "Remove your subscription?"
	if len(list) > 1 {
		question = fmt.Sprintf("Remove all %d subscriptions?", len(list))
	}
	return ic.EditReply(interaction.Response{
		Embeds:     []*discordgo.MessageEmbed{commands.NewEmbed(nil, embedTitleAnimeSubs, question)},
		Components: clearButtons(ic.Invoker.ID),
	})
}

// clearButtons are the confirmation buttons. Their custom IDs carry the author's ID and the choice only, so they
// stay far under 100 characters and never depend on state that could expire.
func clearButtons(authorID string) []discordgo.MessageComponent {
	return []discordgo.MessageComponent{discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		discordgo.Button{Label: "Remove all", Style: discordgo.DangerButton, CustomID: clearPrefix + authorID + ":" + clearYes},
		discordgo.Button{Label: "Keep them", Style: discordgo.SecondaryButton, CustomID: clearPrefix + authorID + ":" + clearNo},
	}}}
}

func runList(ic *interaction.Context) error {
	list, err := loadList(ic.Context(), storeOf(ic.DB), ic.Invoker.ID)
	if err != nil {
		return finish(ic, "Could not load your subscriptions.", err)
	}
	if len(list) == 0 {
		return answer(ic, msgNoSubscriptions)
	}
	return ic.EditReply(renderPage(list, 0, ic.Invoker.ID))
}

func loadList(ctx context.Context, store subsStore, uid string) ([]*anime_subs.ShowSub, error) {
	doc, err := store.GetByID(ctx, uid)
	if err != nil || doc == nil {
		return nil, err
	}
	return docToSubList(doc), nil
}

// autocompleteOwn offers only the invoker's own subscriptions, matched anywhere in the title.
func autocompleteOwn(ctx context.Context, db *database.Client, _ *discordgo.Session, i *discordgo.InteractionCreate, _, typed string) []*discordgo.ApplicationCommandOptionChoice {
	list, err := loadList(ctx, storeOf(db), commands.UserID(i))
	if err != nil {
		logger.For("animesubs").Error("subscription autocomplete failed", "err", err)
		return nil
	}
	matched := commands.MatchAnywhere(list, typed, func(s *anime_subs.ShowSub) []string { return []string{s.Show} })
	out := make([]*discordgo.ApplicationCommandOptionChoice, 0, len(matched))
	for _, s := range matched {
		out = append(out, &discordgo.ApplicationCommandOptionChoice{Name: clip(s.Show), Value: clip(s.Show)})
	}
	return out
}

// clip cuts text to Discord's 100-character limit for choice names, option labels and option values.
func clip(s string) string {
	r := []rune(s)
	if len(r) <= maxChoiceLength {
		return s
	}
	return string(r[:maxChoiceLength])
}

// renderPage builds one page of the list: the embed, Previous/Next when there is more than one page, and the
// Remove picker for the entries on this page. page is clamped to the list.
func renderPage(list []*anime_subs.ShowSub, page int, authorID string) interaction.Response {
	n := len(list)
	totalPages := max((n+discord.MaxEmbedFields-1)/discord.MaxEmbedFields, 1)
	page = min(max(page, 0), totalPages-1)
	start := page * discord.MaxEmbedFields
	end := min(start+discord.MaxEmbedFields, n)

	var components []discordgo.MessageComponent
	if n > discord.MaxEmbedFields {
		components = append(components, commands.PaginationComponents(pageKey, page, n, authorID)...)
	}
	components = append(components, removePicker(list[start:end], page, authorID))
	emb := commands.NewEmbed(nil, embedTitleAnimeSubs, subsDescriptionForPage(list, start, page, totalPages, n), "Pick a show below to unsubscribe from it.")
	return interaction.Response{Embeds: []*discordgo.MessageEmbed{emb}, Components: components}
}

func removePicker(entries []*anime_subs.ShowSub, page int, authorID string) discordgo.MessageComponent {
	var options []discordgo.SelectMenuOption
	seen := make(map[string]bool, len(entries))
	for _, s := range entries {
		v := clip(s.Show)
		if seen[v] {
			continue
		}
		seen[v] = true
		options = append(options, discordgo.SelectMenuOption{Label: v, Value: v})
	}
	return discordgo.ActionsRow{Components: []discordgo.MessageComponent{discordgo.SelectMenu{
		MenuType:    discordgo.StringSelectMenu,
		CustomID:    fmt.Sprintf("%s%s:%d", pickerPrefix, authorID, page),
		Placeholder: "Remove a subscription",
		Options:     options,
	}}}
}

// RenderSubscriptionPage renders one page of the invoker's subscription list for pagination.
func RenderSubscriptionPage(ctx context.Context, db *database.Client, _ *discordgo.Session, _ *discordgo.InteractionCreate, page, _ int, authorID string) commands.Page {
	if authorID == "" {
		return commands.Page{Refusal: "Could not load subscriptions."}
	}
	list, err := loadList(ctx, storeOf(db), authorID)
	if err != nil {
		logger.For("animesubs").Error("subscription page failed", "err", err)
		return commands.Page{Refusal: "Could not load your subscriptions."}
	}
	if len(list) == 0 {
		return commands.Page{Refusal: msgNoSubscriptions + " Run /subscription list again."}
	}
	return commands.Page{Response: renderPage(list, page, authorID)}
}

// handleRemovePicker removes the chosen subscription and re-renders the page. Guard home: only the member whose
// list it is may remove from it, checked before the store is touched, whatever message the picker sits on.
func handleRemovePicker(ic *interaction.Context, _ *discordgo.Session) error {
	parts := strings.SplitN(strings.TrimPrefix(ic.CustomID, pickerPrefix), ":", 2)
	if len(parts) != 2 || len(ic.Values) != 1 {
		return ic.Refuse(msgStalePicker)
	}
	authorID := parts[0]
	page, err := strconv.Atoi(parts[1])
	if err != nil {
		return ic.Refuse(msgStalePicker)
	}
	if ic.Invoker.ID != authorID {
		return ic.Refuse(msgOnlyOwnerRemoves)
	}

	store := storeOf(ic.DB)
	list, err := loadList(ic.Context(), store, authorID)
	if err != nil {
		return finishRefusal(ic, "Could not load your subscriptions.", err)
	}
	var stored []string
	for _, s := range list {
		if clip(s.Show) == ic.Values[0] {
			stored = append(stored, s.Show)
		}
	}
	if len(stored) > 0 {
		if err := store.RemoveShows(ic.Context(), authorID, stored); err != nil {
			return finishRefusal(ic, "Could not update subscriptions.", err)
		}
		if list, err = loadList(ic.Context(), store, authorID); err != nil {
			return finishRefusal(ic, "Could not load your subscriptions.", err)
		}
	}
	if len(list) == 0 {
		// Explicit empty components: an omitted list would leave the old picker on the message.
		return ic.UpdateMessage(interaction.Response{
			Embeds:     []*discordgo.MessageEmbed{commands.NewEmbed(nil, embedTitleAnimeSubs, msgNoSubscriptions)},
			Components: []discordgo.MessageComponent{},
		})
	}
	return ic.UpdateMessage(renderPage(list, page, authorID))
}

// refuseAfter logs a failed step under logMsg, with the invoker's context, and refuses privately.
func refuseAfter(ic *interaction.Context, logMsg, text string, err error) error {
	logger.For("animesubs").Error(logMsg, "guild_id", ic.GuildID, "err", err)
	return ic.Refuse(text)
}

func finishRefusal(ic *interaction.Context, text string, err error) error {
	return refuseAfter(ic, "subscription picker failed", text, err)
}

// handleClearButton settles the clear confirmation. Guard home: only the member whose list it is may press,
// checked before the store is touched. Remove all clears whatever the member has now, not what was asked about.
func handleClearButton(ic *interaction.Context, _ *discordgo.Session) error {
	parts := strings.SplitN(strings.TrimPrefix(ic.CustomID, clearPrefix), ":", 2)
	if len(parts) != 2 || (parts[1] != clearYes && parts[1] != clearNo) {
		return ic.Refuse(msgStaleClear)
	}
	if ic.Invoker.ID != parts[0] {
		return ic.Refuse(msgOnlyOwnerClears)
	}
	msg := "Nothing was removed."
	if parts[1] == clearYes {
		if err := storeOf(ic.DB).ClearShows(ic.Context(), parts[0]); err != nil {
			return refuseAfter(ic, "subscription clear failed", "Could not clear subscriptions.", err)
		}
		msg = "All your subscriptions were removed."
	}
	// Explicit empty components: an omitted list would leave the buttons on the message.
	return ic.UpdateMessage(interaction.Response{
		Embeds:     []*discordgo.MessageEmbed{commands.NewEmbed(nil, embedTitleAnimeSubs, msg)},
		Components: []discordgo.MessageComponent{},
	})
}
