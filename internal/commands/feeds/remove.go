package feeds

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/database/guilds"
	"ReZeroTsu/internal/interaction"

	"github.com/bwmarrin/discordgo"
)

func lower(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

func removeSubcommand() *commands.Subcommand {
	return &commands.Subcommand{
		Name: "remove",
		Desc: "Remove a Reddit feed from a channel.",
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionChannel, Name: optChannel, Description: "The channel to remove the feed from.", Required: true, ChannelTypes: feedChannelTypes, Autocomplete: true},
			{Type: discordgo.ApplicationCommandOptionString, Name: optSubreddit, Description: "The subreddit to remove.", Required: true, Autocomplete: true},
			{Type: discordgo.ApplicationCommandOptionString, Name: optPostType, Description: "Post type: hot, rising, or new. Omit to remove all three.", Autocomplete: true},
			{Type: discordgo.ApplicationCommandOptionString, Name: optAuthor, Description: "Author filter of the feed (optional)."},
			{Type: discordgo.ApplicationCommandOptionString, Name: optTitle, Description: "Title filter of the feed (optional)."},
		},
		Run:                runRemove,
		AutocompleteOption: autocompleteRemove,
	}
}

func autocompleteRemove(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate, optionName, focusedValue string) []*discordgo.ApplicationCommandOptionChoice {
	switch optionName {
	case optPostType:
		return autocompletePostType(ctx, db, s, i, optionName, focusedValue)
	case optSubreddit:
		return subredditChoices(ctx, storeOf(db), i.GuildID, focusedValue)
	case optChannel:
		return channelChoices(ctx, storeOf(db), channelName(s), i.GuildID, focusedValue)
	}
	return nil
}

// subredditChoices suggests the subreddits the server has feeds for, matched anywhere in "r/name".
func subredditChoices(ctx context.Context, st store, guildID, typed string) []*discordgo.ApplicationCommandOptionChoice {
	if guildID == "" {
		return nil
	}
	feeds, err := st.GetFeeds(ctx, guildID)
	if err != nil || len(feeds) == 0 {
		return nil
	}
	seen := make(map[string]struct{})
	for _, f := range feeds {
		if sub := normalizeSubreddit(f.Subreddit); sub != "" {
			seen[sub] = struct{}{}
		}
	}
	subs := make([]string, 0, len(seen))
	for sub := range seen {
		subs = append(subs, sub)
	}
	sort.Strings(subs)
	subs = commands.MatchAnywhere(subs, typed, func(sub string) []string { return []string{sub, "r/" + sub} })
	choices := make([]*discordgo.ApplicationCommandOptionChoice, len(subs))
	for j, sub := range subs {
		choices[j] = &discordgo.ApplicationCommandOptionChoice{Name: clip("r/"+sub, maxChoiceLength), Value: clip(sub, maxChoiceLength)}
	}
	return choices
}

// channelName looks a channel's name up through the session; a nil session or a channel the bot cannot see gives "".
func channelName(s *discordgo.Session) func(id string) string {
	return func(id string) string {
		if s == nil {
			return ""
		}
		if ch, err := s.Channel(id); err == nil && ch != nil {
			return ch.Name
		}
		return ""
	}
}

// channelChoices suggests the channels the server has feeds in. A choice shows the channel's name, or its ID when
// the name is not known, and matches anywhere in either.
func channelChoices(ctx context.Context, st store, nameOf func(id string) string, guildID, typed string) []*discordgo.ApplicationCommandOptionChoice {
	if guildID == "" {
		return nil
	}
	feeds, err := st.GetFeeds(ctx, guildID)
	if err != nil || len(feeds) == 0 {
		return nil
	}
	type channelChoice struct{ id, name string }
	seen := make(map[string]struct{})
	var list []channelChoice
	for _, f := range feeds {
		if f.ChannelID == "" {
			continue
		}
		if _, dup := seen[f.ChannelID]; dup {
			continue
		}
		seen[f.ChannelID] = struct{}{}
		display := "#" + f.ChannelID
		if n := nameOf(f.ChannelID); n != "" {
			display = "#" + n
		}
		list = append(list, channelChoice{id: f.ChannelID, name: display})
	}
	sort.Slice(list, func(a, b int) bool { return list[a].name < list[b].name })
	list = commands.MatchAnywhere(list, typed, func(c channelChoice) []string {
		return []string{c.name, strings.TrimPrefix(c.name, "#"), c.id}
	})
	choices := make([]*discordgo.ApplicationCommandOptionChoice, len(list))
	for j, c := range list {
		choices[j] = &discordgo.ApplicationCommandOptionChoice{Name: clip(c.name, maxChoiceLength), Value: c.id}
	}
	return choices
}

// runRemove removes the feeds matching the typed options.
// Guard homes, moved from /remove-reddit-feed:
//   - "only in a server": the command's ContextGuildOnly;
//   - the required-option, channel and blank-subreddit checks, and the post-type check, are below;
//   - "no set Reddit feeds" is the empty-list notice below, and "No matching feed found" the zero-removed one;
//   - the feed-load and remove failures are Fail, which logs them and shows "Something went wrong.";
//   - the delivery records of each removed post type are forgotten with the feed (DeleteChecks).
func runRemove(ic *interaction.Context) error {
	channelID := channelOption(ic)
	if channelID == "" || stringOption(ic, optSubreddit) == "" {
		return ic.Refuse("Pick a channel and give a subreddit.")
	}
	subreddit := normalizeSubreddit(stringOption(ic, optSubreddit))
	if subreddit == "" {
		return ic.Refuse("Please provide a valid subreddit.")
	}
	st := storeOf(ic.DB)
	feeds, err := st.GetFeeds(ic.Context(), ic.GuildID)
	if err != nil {
		return ic.Fail(err, interaction.ErrorContext{})
	}
	if len(feeds) == 0 {
		return answer(ic, msgNoFeeds)
	}
	postTypes := postTypeChoices
	pt, ok := postTypeOption(ic, "")
	if !ok {
		return ic.Refuse(msgInvalidPostType)
	}
	if pt != "" {
		postTypes = []string{pt}
	}
	author := lower(normalizeAuthor(stringOption(ic, optAuthor)))
	title := lower(stringOption(ic, optTitle))
	var removed int64
	for _, pt := range postTypes {
		n, err := st.RemoveFeed(ic.Context(), ic.GuildID, channelID, subreddit, pt, author, title)
		if err != nil {
			return ic.Fail(err, interaction.ErrorContext{})
		}
		removed += n
		if err := st.DeleteChecks(ic.Context(), ic.GuildID, channelID, subreddit, pt, author, title); err != nil {
			logStoreFailure(ic, "reddit DeleteChecks failed", err)
		}
	}
	switch {
	case removed == 0:
		return answer(ic, "No matching feed found. Check the channel and subreddit, and if you used an author or title, that they match exactly.")
	case len(postTypes) == 3:
		return answer(ic, "Removed r/"+subreddit+" from "+commands.ChannelMention(channelID)+" for all post types (hot, rising, new).")
	}
	return answer(ic, "Removed r/"+subreddit+" ("+postTypes[0]+") from "+commands.ChannelMention(channelID)+".")
}

// pickKey identifies a feed in the Remove picker: its channel, post type, subreddit and filters. The picker value
// is this cut to the select limit.
func pickKey(f guilds.FeedEntry) string {
	return strings.Join([]string{f.ChannelID, f.PostType, f.Subreddit, f.Author, f.Title}, "|")
}

func pickValue(f guilds.FeedEntry) string { return clip(pickKey(f), maxChoiceLength) }

// sameFeedScope reports whether RemoveFeed with f's fields also removes g: it matches on channel, subreddit and
// post type, and narrows by author and title only when they are not empty.
func sameFeedScope(f, g guilds.FeedEntry) bool {
	return f.ChannelID == g.ChannelID && f.Subreddit == g.Subreddit && f.PostType == g.PostType &&
		(f.Author == "" || f.Author == g.Author) && (f.Title == "" || f.Title == g.Title)
}

// removeChosen removes one feed of the list. RemoveFeed ignores an empty author or title, so removing an
// unfiltered feed would also remove its filtered siblings in the same channel; they are put back, and their
// delivery records are kept.
func removeChosen(ic *interaction.Context, st store, feeds []guilds.FeedEntry, chosen guilds.FeedEntry) error {
	var siblings []guilds.FeedEntry
	for _, g := range feeds {
		if g != chosen && sameFeedScope(chosen, g) {
			siblings = append(siblings, g)
		}
	}
	ctx := ic.Context()
	if _, err := st.RemoveFeed(ctx, ic.GuildID, chosen.ChannelID, chosen.Subreddit, chosen.PostType, chosen.Author, chosen.Title); err != nil {
		return err
	}
	for _, g := range siblings {
		if err := st.AddFeed(ctx, ic.GuildID, g); err != nil {
			return err
		}
	}
	if len(siblings) == 0 {
		if err := st.DeleteChecks(ctx, ic.GuildID, chosen.ChannelID, chosen.Subreddit, chosen.PostType, chosen.Author, chosen.Title); err != nil {
			logStoreFailure(ic, "reddit DeleteChecks failed", err)
		}
	}
	return nil
}

// ambiguousChoice is the private notice for a Remove choice that more than one feed clips to.
func ambiguousChoice(matches []guilds.FeedEntry) string {
	var b strings.Builder
	b.WriteString("More than one feed looks the same as far as the list can show it, so I removed nothing:")
	for i, f := range matches {
		if i == 5 {
			b.WriteString("\n...and " + strconv.Itoa(len(matches)-5) + " more")
			break
		}
		b.WriteString("\n- " + ellipsize(describe(f), 300))
	}
	b.WriteString("\nUse /reddit remove with the channel, subreddit and filters instead.")
	return b.String()
}
