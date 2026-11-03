package react

import (
	"context"
	"errors"
	"fmt"
	"sort"
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
	// pageKey names the reaction-role list's page renderer.
	pageKey = "reactrole"
	// pickerPrefix starts the custom ID of the Remove picker: reactrole-rm:<authorID>:<page>. It differs from
	// formPrefix ("reactrole:add:") at the dash, so the two never match each other.
	pickerPrefix = "reactrole-rm:"

	embedTitleList      = "Reaction roles"
	msgNoBindings       = "There are no reaction roles."
	msgOnlyOwnerRemoves = "Only the moderator who ran /reactrole list can remove from it."
	msgStalePicker      = "Could not read that choice. Run /reactrole list again."
)

func init() {
	commands.Add(&commands.Command{
		Name:       "reactrole",
		Desc:       "Manage reaction roles.",
		Visibility: commands.Private,
		Response:   commands.Deferred,
		// Guard home: the bot-side moderator check, until spec 7 moves it to Discord's per-command permissions.
		Permission: commands.PermMod,
		Context:    commands.ContextGuildOnly,
		Module:     "reacts",
		Subcommands: []*commands.Subcommand{
			{Name: "list", Desc: "Show every message with reaction roles.", Run: runList},
		},
	})
	// Restricted: the list and its picker belong to the moderator who ran it.
	commands.RegisterPaginationRenderer(pageKey, RenderReactrolePage)
	commands.AddComponent(pickerPrefix, handleRemovePicker)
}

// boundMessages lists the IDs of the messages that still have bindings, in ID order.
func boundMessages(m map[string]*guilds.ReactJoin) []string {
	ids := make([]string, 0, len(m))
	for id, e := range m {
		if e != nil && len(e.RoleEmojiMap) > 0 {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

func emptyList() interaction.Response {
	return interaction.Response{
		Embeds: []*discordgo.MessageEmbed{commands.NewEmbed(nil, embedTitleList, msgNoBindings)},
		// Explicit empty components: an omitted list would leave the old picker on the message.
		Components: []discordgo.MessageComponent{},
	}
}

// runList shows every message with reaction roles, privately, with the Remove picker.
// Guard homes, moved from /reacts-autorole: the server-only check is the command's ContextGuildOnly; the empty
// notice and the load-failure notice are below. Role names are now role mentions, and an entry links to its
// message when the channel is known.
func runList(ic *interaction.Context) error {
	m, err := bindStoreOf(ic.DB).GetReactJoinMap(ic.Context(), ic.GuildID)
	if err != nil {
		return ic.Fail(err, interaction.ErrorContext{})
	}
	ids := boundMessages(m)
	if len(ids) == 0 {
		return ic.EditReply(emptyList())
	}
	return ic.EditReply(renderPage(m, ids, 0, ic.Invoker.ID, ic.GuildID))
}

// entryValue is the field text of one message: its link or bare ID, then one "emoji → role" line per binding.
func entryValue(guildID, messageID string, entry *guilds.ReactJoin) string {
	var b strings.Builder
	if entry.ChannelID != "" {
		b.WriteString(messageLink(guildID, entry.ChannelID, messageID))
	} else {
		b.WriteString("`" + messageID + "`")
	}
	_, pairs := entryEmojis(entry)
	for _, p := range pairs {
		b.WriteString("\n" + p)
	}
	return truncateRunes(b.String(), discord.MaxEmbedFieldValueLength)
}

func truncateRunes(s string, limit int) string {
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return string(r[:limit-1]) + "…"
}

// renderPage builds one page: the embed, Previous/Next when there is more than one page, and the Remove picker for
// the messages on this page. page is clamped to the list.
func renderPage(m map[string]*guilds.ReactJoin, ids []string, page int, authorID, guildID string) interaction.Response {
	n := len(ids)
	totalPages := max((n+discord.MaxEmbedFields-1)/discord.MaxEmbedFields, 1)
	page = min(max(page, 0), totalPages-1)
	start := page * discord.MaxEmbedFields
	end := min(start+discord.MaxEmbedFields, n)

	var components []discordgo.MessageComponent
	desc := ""
	if n > discord.MaxEmbedFields {
		components = append(components, commands.PaginationComponents(pageKey, page, n, authorID)...)
		desc = fmt.Sprintf("Page %d of %d. Showing %d-%d of %d messages.", page+1, totalPages, start+1, end, n)
	}
	fields := make([]*discordgo.MessageEmbedField, 0, end-start)
	options := make([]discordgo.SelectMenuOption, 0, end-start)
	for _, id := range ids[start:end] {
		fields = append(fields, &discordgo.MessageEmbedField{Name: "Message ID: " + id, Value: entryValue(guildID, id, m[id])})
		options = append(options, discordgo.SelectMenuOption{Label: id, Value: id})
	}
	components = append(components, discordgo.ActionsRow{Components: []discordgo.MessageComponent{discordgo.SelectMenu{
		MenuType:    discordgo.StringSelectMenu,
		CustomID:    fmt.Sprintf("%s%s:%d", pickerPrefix, authorID, page),
		Placeholder: "Remove a message's reaction roles",
		Options:     options,
	}}})
	emb := commands.NewEmbedWithFields(nil, embedTitleList, desc, fields, "Pick a message below to remove all its reaction roles.")
	return interaction.Response{Embeds: []*discordgo.MessageEmbed{emb}, Components: components}
}

// RenderReactrolePage renders one page of the list for pagination.
// Guard homes, moved from the reacts-autorole renderer: the load failure and empty notices are below; the
// server-only check is the pagination handler's guild check for this key.
func RenderReactrolePage(ctx context.Context, db *database.Client, _ *discordgo.Session, i *discordgo.InteractionCreate, page, _ int, authorID string) commands.Page {
	m, err := bindStoreOf(db).GetReactJoinMap(ctx, i.GuildID)
	if err != nil {
		logger.For("react").Error("reactrole list page failed", "guild_id", i.GuildID, "err", err)
		return commands.Page{Refusal: "Could not load react joins."}
	}
	ids := boundMessages(m)
	if len(ids) == 0 {
		return commands.Page{Refusal: msgNoBindings + " Run /reactrole list again."}
	}
	return commands.Page{Response: renderPage(m, ids, page, authorID, i.GuildID)}
}

// handleRemovePicker removes every binding of the chosen message, as the Remove reaction roles right-click does,
// whether or not the message still exists: the record is deleted first, then the bot's reactions are taken off
// when the channel is known, and a message or channel that is gone does not matter. The page is then re-rendered.
// Guard homes: only the moderator whose list it is may remove from it, checked before the store is touched. A
// choice already gone just re-renders.
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

	store := bindStoreOf(ic.DB)
	m, err := store.GetReactJoinMap(ic.Context(), ic.GuildID)
	if err != nil {
		return ic.Fail(err, interaction.ErrorContext{})
	}
	messageID := ic.Values[0]
	if entry := m[messageID]; entry != nil {
		emojis, _ := entryEmojis(entry)
		if err := store.DeleteReactJoinEntry(ic.Context(), ic.GuildID, messageID); err != nil {
			return ic.Fail(err, interaction.ErrorContext{})
		}
		delete(m, messageID)
		if entry.ChannelID != "" {
			for _, e := range emojis {
				if err := ic.RemoveOwnReaction(entry.ChannelID, messageID, e); err != nil {
					if !messageGone(err) {
						logger.For("react").Error("reaction removal failed", "guild_id", ic.GuildID, "channel_id", entry.ChannelID, "message_id", messageID, "err", err)
					}
					break
				}
			}
		}
	}
	ids := boundMessages(m)
	if len(ids) == 0 {
		return ic.UpdateMessage(emptyList())
	}
	return ic.UpdateMessage(renderPage(m, ids, page, authorID, ic.GuildID))
}

// messageGone reports whether err says the message or its channel no longer exists.
func messageGone(err error) bool {
	var rest *discordgo.RESTError
	if !errors.As(err, &rest) || rest.Message == nil {
		return false
	}
	return rest.Message.Code == discordgo.ErrCodeUnknownMessage || rest.Message.Code == discordgo.ErrCodeUnknownChannel
}
