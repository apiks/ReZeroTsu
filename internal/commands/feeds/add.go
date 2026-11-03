package feeds

import (
	"strings"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/database/guilds"
	"ReZeroTsu/internal/interaction"
	"ReZeroTsu/internal/reddit"

	"github.com/bwmarrin/discordgo"
)

func addSubcommand() *commands.Subcommand {
	return &commands.Subcommand{
		Name: "add",
		Desc: "Add a Reddit feed to a channel, with a sort (formerly post-type), optional filters and pin.",
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionChannel, Name: optChannel, Description: "The channel to add the feed to.", Required: true, ChannelTypes: feedChannelTypes},
			{Type: discordgo.ApplicationCommandOptionString, Name: optSubreddit, Description: "The subreddit (e.g. r/anime or anime).", Required: true},
			{Type: discordgo.ApplicationCommandOptionString, Name: optSort, Description: "Which listing to follow. Default: Hot.", Choices: []*discordgo.ApplicationCommandOptionChoice{
				{Name: "Hot", Value: "hot"}, {Name: "Rising", Value: "rising"}, {Name: "New", Value: "new"},
			}},
			{Type: discordgo.ApplicationCommandOptionString, Name: optAuthor, Description: "Filter to posts from this user (optional)."},
			{Type: discordgo.ApplicationCommandOptionString, Name: optTitle, Description: "Filter to posts starting with this title (optional)."},
			{Type: discordgo.ApplicationCommandOptionBoolean, Name: optPin, Description: "Auto-pin the latest post. Default: false."},
		},
		Run: runAdd,
	}
}

// feedUnavailableText words a failed availability check for the moderator. Reddit answers a private, banned or
// missing subreddit the same way, so one sentence covers the three.
func feedUnavailableText(err error, subreddit string) string {
	switch {
	case reddit.IsPermanent(err):
		return "r/" + subreddit + " is private, banned or does not exist, so I can't read its feed. Check the name and try again."
	case reddit.IsGlobalCooldown(err):
		return "Reddit is rate-limiting me right now. Try again in about a minute."
	}
	return "I couldn't reach Reddit to check r/" + subreddit + ". Try again in a moment."
}

// runAdd adds a feed after checking that Reddit serves it.
// Guard homes, moved from /add-reddit-feed:
//   - "only in a server": the command's ContextGuildOnly;
//   - "Channel and subreddit are required" and "Invalid or missing channel": the options are Required, and the
//     missing-channel and blank-subreddit checks below stay for a malformed interaction;
//   - the subreddit name rules (reddit.ValidateSubreddit) and the post-type check run before any Reddit request;
//   - the availability check (private or missing subreddit, rate limiting, unreachable) is checkFeed, which runs
//     before the feed is saved;
//   - the saved-feed failure is Fail, which logs it and shows "Something went wrong.". The old command also posted
//     "add-reddit-feed error: <err>" to the bot log channel; that copy is dropped on purpose, because it put a raw
//     error into a server channel and raw errors are never shown (the error is still logged);
//   - author and title filters are lowercased and the pin option is read exactly as before.
func runAdd(ic *interaction.Context) error {
	channelID := channelOption(ic)
	if channelID == "" || stringOption(ic, optSubreddit) == "" {
		return ic.Refuse("Pick a channel and give a subreddit.")
	}
	subreddit := normalizeSubreddit(stringOption(ic, optSubreddit))
	if subreddit == "" {
		return ic.Refuse("Please provide a valid subreddit (e.g. r/anime or anime).")
	}
	if err := reddit.ValidateSubreddit(subreddit); err != nil {
		return ic.Refuse(err.Error())
	}
	postType, ok := sortOption(ic)
	if !ok {
		return ic.Refuse(msgInvalidPostType)
	}
	pin := false
	if o := commands.ParseOption(ic.Command.Options, optPin); o != nil {
		pin = o.BoolValue()
	}
	feed := guilds.FeedEntry{
		Subreddit: subreddit,
		Title:     lower(stringOption(ic, optTitle)),
		Author:    lower(normalizeAuthor(stringOption(ic, optAuthor))),
		Pin:       pin,
		PostType:  postType,
		ChannelID: channelID,
	}
	st := storeOf(ic.DB)
	existing, err := st.GetFeeds(ic.Context(), ic.GuildID)
	if err != nil {
		return ic.Fail(err, interaction.ErrorContext{})
	}
	mention := commands.ChannelMention(channelID)
	for _, g := range existing {
		if !sameIdentity(g, feed) {
			continue
		}
		if g.Pin == feed.Pin {
			return answer(ic, "That feed is already set up in "+mention+".")
		}
		if err := st.SetFeedPin(ic.Context(), ic.GuildID, feed, feed.Pin); err != nil {
			return ic.Fail(err, interaction.ErrorContext{})
		}
		if feed.Pin {
			return answer(ic, "Updated r/"+subreddit+" in "+mention+": now pinning the latest post.")
		}
		return answer(ic, "Updated r/"+subreddit+" in "+mention+": no longer pinning the latest post.")
	}
	if err := checkFeed(ic.Context(), subreddit, postType); err != nil {
		return ic.Refuse(feedUnavailableText(err, subreddit))
	}
	if err := st.AddFeed(ic.Context(), ic.GuildID, feed); err != nil {
		return ic.Fail(err, interaction.ErrorContext{})
	}
	return answer(ic, addedText(feed))
}

// addedText describes the whole saved feed, leaving out the clauses of absent filters and pin.
func addedText(f guilds.FeedEntry) string {
	var clauses []string
	switch {
	case f.Author != "" && f.Title != "":
		clauses = append(clauses, "only posts by u/"+f.Author+` whose title starts with "`+f.Title+`"`)
	case f.Author != "":
		clauses = append(clauses, "only posts by u/"+f.Author)
	case f.Title != "":
		clauses = append(clauses, `only posts whose title starts with "`+f.Title+`"`)
	}
	if f.Pin {
		clauses = append(clauses, "pinning the latest")
	}
	s := "Added r/" + f.Subreddit + " (" + f.PostType + ") to " + commands.ChannelMention(f.ChannelID)
	if len(clauses) > 0 {
		s += ": " + strings.Join(clauses, ", ")
	}
	return s + ". New posts start appearing within a few minutes."
}
