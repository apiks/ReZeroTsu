package feeds

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

	"github.com/bwmarrin/discordgo"
)

const (
	// pageKey names the feed list's page renderer.
	pageKey = "reddit"
	// pickerPrefix starts the custom ID of the Remove picker: reddit-rm:<authorID>:<page>.
	pickerPrefix = "reddit-rm:"

	msgNoFeeds          = "There are no Reddit feeds. Add one with /reddit add."
	msgOnlyOwnerRemoves = "Only the moderator who ran /reddit list can remove from it."
	msgStalePicker      = "Could not read that choice. Run /reddit list again."
)

func listSubcommand() *commands.Subcommand {
	return &commands.Subcommand{
		Name: "list",
		Desc: "Show this server's Reddit feeds.",
		Run:  runList,
	}
}

// runList shows every feed of the server privately, with the Remove picker.
// Guard homes, moved from /reddit-feeds: the server-only check is the command's ContextGuildOnly; the empty list
// notice is below; the load failure is Fail.
func runList(ic *interaction.Context) error {
	feeds, err := storeOf(ic.DB).GetFeeds(ic.Context(), ic.GuildID)
	if err != nil {
		return ic.Fail(err, interaction.ErrorContext{})
	}
	if len(feeds) == 0 {
		return answer(ic, msgNoFeeds)
	}
	return ic.EditReply(renderPage(feeds, 0, ic.Invoker.ID))
}

// describe is a feed on one line: "r/anime (hot), from u/name, title starts "x", pinned".
func describe(f guilds.FeedEntry) string {
	s := "r/" + f.Subreddit + " (" + f.PostType + ") in " + commands.ChannelMention(f.ChannelID)
	if f.Author != "" {
		s += ", from u/" + f.Author
	}
	if f.Title != "" {
		s += ", title starts with " + f.Title
	}
	if f.Pin {
		s += ", pinned"
	}
	return s
}

// feedFields numbers each field from first (1-based), so the Remove picker option with the same number is the same feed.
func feedFields(feeds []guilds.FeedEntry, first int) []*discordgo.MessageEmbedField {
	fields := make([]*discordgo.MessageEmbedField, 0, len(feeds))
	for j, f := range feeds {
		value := commands.ChannelMention(f.ChannelID) + " | " + f.PostType
		if f.Author != "" {
			value += " | u/" + f.Author
		}
		if f.Title != "" {
			value += " | " + ellipsize(f.Title, 80)
		}
		if f.Pin {
			value += " | Pinned"
		}
		fields = append(fields, &discordgo.MessageEmbedField{
			Name:  ellipsize(strconv.Itoa(first+j)+". r/"+f.Subreddit, discord.MaxEmbedFieldNameLength),
			Value: ellipsize(value, discord.MaxEmbedFieldValueLength),
		})
	}
	return fields
}

// renderPage builds one page of the list (0-based, clamped): the embed with channel mentions, the paging buttons
// when there is more than one page, and the Remove picker for the feeds on the page.
func renderPage(feeds []guilds.FeedEntry, page int, authorID string) interaction.Response {
	n := len(feeds)
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
	components = append(components, removePicker(feeds[start:end], start, page, authorID))
	emb := commands.NewEmbedWithFields(nil, embedTitle, desc, feedFields(feeds[start:end], start+1), "Pick a feed below to remove it.")
	return interaction.Response{Embeds: []*discordgo.MessageEmbed{emb}, Components: components}
}

func removePicker(entries []guilds.FeedEntry, start, page int, authorID string) discordgo.MessageComponent {
	options := make([]discordgo.SelectMenuOption, 0, len(entries))
	seen := make(map[string]bool, len(entries))
	for j, f := range entries {
		v := pickValue(f)
		if seen[v] {
			continue
		}
		seen[v] = true
		label := strconv.Itoa(start+j+1) + ". r/" + f.Subreddit + " (" + f.PostType + ")"
		if f.Author != "" {
			label += " u/" + f.Author
		}
		if f.Title != "" {
			label += " " + f.Title
		}
		options = append(options, discordgo.SelectMenuOption{
			Label: clip(label),
			Value: v,
		})
	}
	return discordgo.ActionsRow{Components: []discordgo.MessageComponent{discordgo.SelectMenu{
		MenuType:    discordgo.StringSelectMenu,
		CustomID:    pickerPrefix + authorID + ":" + strconv.Itoa(page),
		Placeholder: "Remove a feed",
		Options:     options,
	}}}
}

// RenderFeedsPage renders one page of the feed list for pagination. Guard homes, moved from the reddit-feeds
// renderer: the load failure and empty notices are below; the server-only check is the pagination handler's guild
// check for this key.
func RenderFeedsPage(ctx context.Context, db *database.Client, _ *discordgo.Session, i *discordgo.InteractionCreate, page, _ int, authorID string) commands.Page {
	feeds, err := storeOf(db).GetFeeds(ctx, i.GuildID)
	if err != nil {
		return commands.Page{Refusal: interaction.SomethingWentWrong}
	}
	if len(feeds) == 0 {
		return commands.Page{Refusal: msgNoFeeds}
	}
	return commands.Page{Response: renderPage(feeds, page, authorID)}
}

// handleRemovePicker removes the feed chosen in the Remove picker and re-renders the page. Guard home: only the
// moderator whose list it is may remove from it, checked before the store is touched. A feed already gone just
// re-renders; a choice that more than one feed clips to removes nothing.
func handleRemovePicker(ic *interaction.Context, _ *discordgo.Session) error {
	authorID, pageText, ok := strings.Cut(strings.TrimPrefix(ic.CustomID, pickerPrefix), ":")
	page, err := strconv.Atoi(pageText)
	if !ok || err != nil || len(ic.Values) != 1 || ic.GuildID == "" || ic.Invoker == nil {
		return ic.Refuse(msgStalePicker)
	}
	if ic.Invoker.ID != authorID {
		return ic.Refuse(msgOnlyOwnerRemoves)
	}
	st := storeOf(ic.DB)
	feeds, err := st.GetFeeds(ic.Context(), ic.GuildID)
	if err != nil {
		return ic.Fail(err, interaction.ErrorContext{})
	}
	var matches []guilds.FeedEntry
	for _, f := range feeds {
		if pickValue(f) == ic.Values[0] {
			matches = append(matches, f)
		}
	}
	if len(matches) > 1 {
		return ic.Refuse(ambiguousChoice(matches))
	}
	if len(matches) == 1 {
		if err := removeChosen(ic, st, feeds, matches[0]); err != nil {
			return ic.Fail(err, interaction.ErrorContext{})
		}
		if feeds, err = st.GetFeeds(ic.Context(), ic.GuildID); err != nil {
			return ic.Fail(err, interaction.ErrorContext{})
		}
	}
	if len(feeds) == 0 {
		// Explicit empty components: an omitted list would leave the old picker on the message.
		return ic.UpdateMessage(interaction.Response{
			Embeds:     []*discordgo.MessageEmbed{commands.NewEmbed(nil, embedTitle, msgNoFeeds)},
			Components: []discordgo.MessageComponent{},
		})
	}
	return ic.UpdateMessage(renderPage(feeds, page, authorID))
}
