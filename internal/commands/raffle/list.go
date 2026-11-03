package raffle

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/database/guilds"
	"ReZeroTsu/internal/discord"
	"ReZeroTsu/internal/interaction"
	"ReZeroTsu/internal/logger"

	"github.com/bwmarrin/discordgo"
)

const (
	// pageKey names the raffle list's page renderer.
	pageKey = "raffle"
	// pickerPrefix starts the custom ID of the Remove picker: raffle-rm:<authorID>:<page>. It differs from the
	// button prefix "raffle:" at the dash, so the two never match each other.
	pickerPrefix = "raffle-rm:"

	embedTitleList      = "Raffles"
	msgNoRaffles        = "There are no raffles."
	msgOnlyOwnerRemoves = "Only the moderator who ran /raffle list can remove from it."
	msgStalePicker      = "Could not read that choice. Run /raffle list again."
	maxChoiceLength     = 100
)

// listStore is the part of the guild repository /raffle list and its Remove picker use. DeleteRaffle is the
// call /remove-raffle made; the picker closes the raffle message first, then deletes the record.
type listStore interface {
	GetRaffles(ctx context.Context, guildID string) ([]*guilds.Raffle, error)
	DeleteRaffle(ctx context.Context, guildID, name string) error
}

// listStoreOf picks the store of a database client; tests replace it.
var listStoreOf = func(db *database.Client) listStore { return db.Guilds() }

func listSubcommand() *commands.Subcommand {
	return &commands.Subcommand{
		Name: "list",
		Desc: "Show all raffles and their entrant counts.",
		Run:  runList,
	}
}

func init() {
	// Restricted: the list and its picker belong to the moderator who ran it, so another member's press is refused.
	commands.RegisterPaginationRenderer(pageKey, RenderRafflePage)
	commands.AddComponent(pickerPrefix, handleRemovePicker)
}

func answer(ic *interaction.Context, desc string) error {
	return ic.EditReply(interaction.Response{Embeds: []*discordgo.MessageEmbed{commands.NewEmbed(nil, embedTitleList, desc)}})
}

// runList shows every raffle of the server privately, with the Remove picker.
// Guard homes, moved from /show-raffles: the server-only check is the command's ContextGuildOnly; the empty list
// notice and the load-failure notice are below. /my-raffles ("raffles you joined") is gone with no equivalent:
// members use the buttons, and the moderator list shows every raffle.
func runList(ic *interaction.Context) error {
	raffles, err := listStoreOf(ic.DB).GetRaffles(ic.Context(), ic.GuildID)
	if err != nil {
		return ic.Fail(err, interaction.ErrorContext{})
	}
	if len(raffles) == 0 {
		return answer(ic, msgNoRaffles)
	}
	return ic.EditReply(renderPage(raffles, 0, ic.Invoker.ID, ic.GuildID))
}

func clip(s string) string {
	r := []rune(s)
	if len(r) <= maxChoiceLength {
		return s
	}
	return string(r[:maxChoiceLength])
}

func entrantCount(r *guilds.Raffle) string {
	if len(r.ParticipantIDs) == 1 {
		return "1 entrant"
	}
	return strconv.Itoa(len(r.ParticipantIDs)) + " entrants"
}

func pageFields(raffles []*guilds.Raffle, guildID string) []*discordgo.MessageEmbedField {
	fields := make([]*discordgo.MessageEmbedField, 0, len(raffles))
	for _, r := range raffles {
		name := []rune(r.Name)
		if len(name) > discord.MaxEmbedFieldNameLength {
			name = append(name[:discord.MaxEmbedFieldNameLength-1], '…')
		}
		value := entrantCount(r)
		if r.MessageID != "" {
			value += " - " + messageLink(guildID, r.ChannelID, r.MessageID)
		}
		fields = append(fields, &discordgo.MessageEmbedField{Name: string(name), Value: value})
	}
	return fields
}

// renderPage builds one page of the list: the embed, Previous/Next when there is more than one page, and the
// Remove picker for the raffles on this page. page is clamped to the list.
func renderPage(raffles []*guilds.Raffle, page int, authorID, guildID string) interaction.Response {
	n := len(raffles)
	totalPages := max((n+discord.MaxEmbedFields-1)/discord.MaxEmbedFields, 1)
	page = min(max(page, 0), totalPages-1)
	start := page * discord.MaxEmbedFields
	end := min(start+discord.MaxEmbedFields, n)

	var components []discordgo.MessageComponent
	desc := ""
	if n > discord.MaxEmbedFields {
		components = append(components, commands.PaginationComponents(pageKey, page, n, authorID)...)
		desc = fmt.Sprintf("Page %d of %d. Showing %d-%d of %d.", page+1, totalPages, start+1, end, n)
	}
	components = append(components, removePicker(raffles[start:end], page, authorID))
	emb := commands.NewEmbedWithFields(nil, embedTitleList, desc, pageFields(raffles[start:end], guildID), "Pick a raffle below to remove it.")
	return interaction.Response{Embeds: []*discordgo.MessageEmbed{emb}, Components: components}
}

func removePicker(entries []*guilds.Raffle, page int, authorID string) discordgo.MessageComponent {
	var options []discordgo.SelectMenuOption
	seen := make(map[string]bool, len(entries))
	for _, r := range entries {
		v := clip(r.Name)
		if seen[v] {
			continue
		}
		seen[v] = true
		options = append(options, discordgo.SelectMenuOption{Label: v, Value: v})
	}
	return discordgo.ActionsRow{Components: []discordgo.MessageComponent{discordgo.SelectMenu{
		MenuType:    discordgo.StringSelectMenu,
		CustomID:    fmt.Sprintf("%s%s:%d", pickerPrefix, authorID, page),
		Placeholder: "Remove a raffle",
		Options:     options,
	}}}
}

// RenderRafflePage renders one page of the raffle list for pagination.
// Guard homes, moved from the show-raffles renderer: the load failure and empty notices are below; the server-only
// check is the pagination handler's guild check for this key.
func RenderRafflePage(ctx context.Context, db *database.Client, _ *discordgo.Session, i *discordgo.InteractionCreate, page, _ int, authorID string) commands.Page {
	raffles, err := listStoreOf(db).GetRaffles(ctx, i.GuildID)
	if err != nil {
		logger.For("raffle").Error("raffle list page failed", "guild_id", i.GuildID, "err", err)
		return commands.Page{Refusal: "Could not load raffles."}
	}
	if len(raffles) == 0 {
		return commands.Page{Refusal: msgNoRaffles + " Run /raffle list again."}
	}
	return commands.Page{Response: renderPage(raffles, page, authorID, i.GuildID)}
}

// disabledButtons is the Enter and Leave row of a message that no longer takes entries.
func disabledButtons() discordgo.MessageComponent {
	return discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		discordgo.Button{Label: "Enter", Style: discordgo.SuccessButton, CustomID: buttonPrefix + actionEnter, Disabled: true},
		discordgo.Button{Label: "Leave", Style: discordgo.SecondaryButton, CustomID: buttonPrefix + actionLeave, Disabled: true},
	}}
}

// handleRemovePicker removes the chosen raffle the way /raffle remove (runRemove) does: its raffle message is
// closed with closedMessage (closeRaffleMessage), then the record is deleted, then the page is re-rendered. As in
// runRemove, a message or channel that no longer exists does not block the removal, and any other edit failure is
// explained and keeps the raffle. A choice that more than one raffle clips to removes nothing.
// Guard homes: only the moderator whose list it is may remove from it, checked before the store is touched,
// whatever message the picker sits on (the list is private, so only that moderator can see it either way). The
// "No such raffle" check of /remove-raffle is the lookup below: a choice already gone just re-renders.
func handleRemovePicker(ic *interaction.Context, _ *discordgo.Session) error {
	parts := strings.SplitN(strings.TrimPrefix(ic.CustomID, pickerPrefix), ":", 2)
	if len(parts) != 2 || len(ic.Values) != 1 {
		return ic.Refuse(msgStalePicker)
	}
	authorID := parts[0]
	page, err := strconv.Atoi(parts[1])
	if err != nil || ic.GuildID == "" || ic.Invoker == nil {
		return ic.Refuse(msgStalePicker)
	}
	if ic.Invoker.ID != authorID {
		return ic.Refuse(msgOnlyOwnerRemoves)
	}

	store := listStoreOf(ic.DB)
	raffles, err := store.GetRaffles(ic.Context(), ic.GuildID)
	if err != nil {
		return pickerFailed(ic, "Could not load raffles.", err)
	}
	var matches []*guilds.Raffle
	for _, r := range raffles {
		if r != nil && clip(r.Name) == ic.Values[0] {
			matches = append(matches, r)
		}
	}
	if len(matches) > 1 {
		// Legacy names have no length limit, so two can clip alike. Removing the first would be a guess.
		return ic.Refuse(ambiguousChoice(matches))
	}
	if len(matches) == 1 {
		chosen := matches[0]
		if _, err := closeRaffleMessage(ic, chosen); err != nil {
			return ic.Fail(err, raffleErrorContext(chosen.ChannelID))
		}
		if err := store.DeleteRaffle(ic.Context(), ic.GuildID, chosen.Name); err != nil {
			return pickerFailed(ic, "Could not remove raffle.", err)
		}
		if raffles, err = store.GetRaffles(ic.Context(), ic.GuildID); err != nil {
			return pickerFailed(ic, "Could not load raffles.", err)
		}
	}
	if len(raffles) == 0 {
		// Explicit empty components: an omitted list would leave the old picker on the message.
		return ic.UpdateMessage(interaction.Response{
			Embeds:     []*discordgo.MessageEmbed{commands.NewEmbed(nil, embedTitleList, msgNoRaffles)},
			Components: []discordgo.MessageComponent{},
		})
	}
	return ic.UpdateMessage(renderPage(raffles, page, authorID, ic.GuildID))
}

// ambiguousChoice is the private notice for a Remove choice that more than one raffle clips to.
func ambiguousChoice(matches []*guilds.Raffle) string {
	const shown, nameLimit = 5, 300
	var b strings.Builder
	b.WriteString("More than one raffle has that name as far as the list can show it, so I removed nothing:")
	for i, r := range matches {
		if i == shown {
			fmt.Fprintf(&b, "\n...and %d more", len(matches)-shown)
			break
		}
		b.WriteString("\n- " + truncate(r.Name, nameLimit))
	}
	b.WriteString("\nUse /raffle remove with the full name instead.")
	return b.String()
}

func pickerFailed(ic *interaction.Context, text string, err error) error {
	logger.For("raffle").Error("raffle picker failed", "guild_id", ic.GuildID, "err", err)
	return ic.Refuse(text)
}
