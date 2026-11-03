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

// maxListedFeeds is how many feeds the "several feeds" refusal names before it says "and N more".
const maxListedFeeds = 5

func lower(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

func removeSubcommand() *commands.Subcommand {
	return &commands.Subcommand{
		Name: "remove",
		Desc: "Remove a Reddit feed. Pick it from the suggestions, or type its subreddit.",
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionString, Name: optFeed, Description: "The feed to remove: pick a suggestion, or type a subreddit.", Required: true, Autocomplete: true},
		},
		Run:                runRemove,
		AutocompleteOption: autocompleteRemove,
	}
}

func autocompleteRemove(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate, optionName, focusedValue string) []*discordgo.ApplicationCommandOptionChoice {
	if optionName != optFeed {
		return nil
	}
	return feedChoices(ctx, storeOf(db), channelName(s), i.GuildID, focusedValue)
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

// portChannelName is channelName through the interaction port.
func portChannelName(p interaction.Port) func(id string) string {
	return func(id string) string {
		if ch, err := p.Channel(id); err == nil && ch != nil {
			return ch.Name
		}
		return ""
	}
}

// describeFeed words a feed for a moderator: `r/anime (hot) in #general · u/bob · "Episode"`. The channel shows
// its name when nameOf knows it, otherwise its ID.
func describeFeed(f guilds.FeedEntry, nameOf func(id string) string) string {
	channel := nameOf(f.ChannelID)
	if channel == "" {
		channel = f.ChannelID
	}
	desc := "r/" + f.Subreddit + " (" + f.PostType + ") in #" + channel
	if f.Author != "" {
		desc += " · u/" + f.Author
	}
	if f.Title != "" {
		desc += ` · "` + f.Title + `"`
	}
	return desc
}

// feedChoices suggests the server's feeds, each described in full and matched anywhere in that description. The
// choice value is the feed's short code.
func feedChoices(ctx context.Context, st store, nameOf func(id string) string, guildID, typed string) []*discordgo.ApplicationCommandOptionChoice {
	if guildID == "" {
		return nil
	}
	feeds, err := st.GetFeeds(ctx, guildID)
	if err != nil || len(feeds) == 0 {
		return nil
	}
	type feedChoice struct{ code, desc string }
	list := make([]feedChoice, len(feeds))
	for j, f := range feeds {
		list[j] = feedChoice{code: shortCode(f), desc: describeFeed(f, nameOf)}
	}
	sort.SliceStable(list, func(a, b int) bool { return list[a].desc < list[b].desc })
	list = commands.MatchAnywhere(list, typed, func(c feedChoice) []string { return []string{c.desc} })
	choices := make([]*discordgo.ApplicationCommandOptionChoice, len(list))
	for j, c := range list {
		choices[j] = &discordgo.ApplicationCommandOptionChoice{Name: clip(c.desc), Value: c.code}
	}
	return choices
}

// runRemove removes the feed named by the feed option: a short code from the suggestions removes exactly that
// feed; any other text is read as a subreddit, which removes its feed when there is exactly one.
// Guard homes, moved from the five-option /reddit remove:
//   - "only in a server": the command's ContextGuildOnly;
//   - the required-option and blank-subreddit checks are below;
//   - "no set Reddit feeds" is the empty-list notice below, and "No matching feed found" is now "No feed for r/x";
//   - the feed-load and remove failures are Fail, which logs them and shows "Something went wrong.";
//   - the delivery records are forgotten with the feed unless a sibling shares them (removeChosen).
//
// Removing every sort of a subreddit at once is dropped on purpose.
func runRemove(ic *interaction.Context) error {
	typed := strings.TrimSpace(stringOption(ic, optFeed))
	if typed == "" {
		return ic.Refuse("Pick a feed to remove.")
	}
	st := storeOf(ic.DB)
	feeds, err := st.GetFeeds(ic.Context(), ic.GuildID)
	if err != nil {
		return ic.Fail(err, interaction.ErrorContext{})
	}
	if len(feeds) == 0 {
		return answer(ic, msgNoFeeds)
	}
	for _, f := range feeds {
		if shortCode(f) == typed {
			return removeAndConfirm(ic, st, feeds, f)
		}
	}
	subreddit := normalizeSubreddit(typed)
	if subreddit == "" {
		return ic.Refuse("Please provide a valid subreddit.")
	}
	var matches []guilds.FeedEntry
	for _, f := range feeds {
		if f.Subreddit == subreddit {
			matches = append(matches, f)
		}
	}
	switch len(matches) {
	case 0:
		return ic.Refuse("No feed for r/" + subreddit + ". See /reddit list.")
	case 1:
		return removeAndConfirm(ic, st, feeds, matches[0])
	}
	nameOf := portChannelName(ic.Port)
	lines := []string{"r/" + subreddit + " has " + strconv.Itoa(len(matches)) + " feeds. Pick one from the suggestions:"}
	for _, f := range matches[:min(len(matches), maxListedFeeds)] {
		lines = append(lines, "- "+describeFeed(f, nameOf))
	}
	if more := len(matches) - maxListedFeeds; more > 0 {
		lines = append(lines, "and "+strconv.Itoa(more)+" more")
	}
	return ic.Refuse(strings.Join(lines, "\n"))
}

func removeAndConfirm(ic *interaction.Context, st store, feeds []guilds.FeedEntry, chosen guilds.FeedEntry) error {
	if err := removeChosen(ic, st, feeds, chosen); err != nil {
		return ic.Fail(err, interaction.ErrorContext{})
	}
	return answer(ic, "Removed r/"+chosen.Subreddit+" ("+chosen.PostType+") from "+commands.ChannelMention(chosen.ChannelID)+".")
}

// sameFeedScope reports whether two feeds share their delivery records: the same channel, subreddit and sort,
// where an unfiltered feed also covers its filtered siblings.
func sameFeedScope(f, g guilds.FeedEntry) bool {
	return f.ChannelID == g.ChannelID && f.Subreddit == g.Subreddit && f.PostType == g.PostType &&
		(f.Author == "" || f.Author == g.Author) && (f.Title == "" || f.Title == g.Title)
}

// removeChosen removes exactly one feed of the list, in one update, so its filtered siblings in the same channel
// stay in place. The delivery records are forgotten only when the feed has no siblings that share them.
func removeChosen(ic *interaction.Context, st store, feeds []guilds.FeedEntry, chosen guilds.FeedEntry) error {
	var siblings []guilds.FeedEntry
	for _, g := range feeds {
		if g != chosen && sameFeedScope(chosen, g) {
			siblings = append(siblings, g)
		}
	}
	ctx := ic.Context()
	if _, err := st.RemoveExactFeed(ctx, ic.GuildID, chosen); err != nil {
		return err
	}
	if len(siblings) == 0 {
		if err := st.DeleteChecks(ctx, ic.GuildID, chosen.ChannelID, chosen.Subreddit, chosen.PostType, chosen.Author, chosen.Title); err != nil {
			logStoreFailure(ic, "reddit DeleteChecks failed", err)
		}
	}
	return nil
}
