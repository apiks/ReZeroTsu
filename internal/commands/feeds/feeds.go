// Package feeds holds /reddit: add, remove and list a server's Reddit feeds. The delivery side is in
// internal/bot/feeds.go.
package feeds

import (
	"context"
	"slices"
	"strings"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/database/guilds"
	"ReZeroTsu/internal/interaction"
	"ReZeroTsu/internal/logger"
	"ReZeroTsu/internal/reddit"

	"github.com/bwmarrin/discordgo"
)

const (
	embedTitle = "Reddit feeds"

	optFeed      = "feed"
	optChannel   = "channel"
	optSubreddit = "subreddit"
	optPostType  = "post-type"
	optAuthor    = "author"
	optTitle     = "title"
	optPin       = "pin"

	maxChoiceLength = 100
)

var postTypeChoices = []string{"hot", "rising", "new"}

var feedChannelTypes = []discordgo.ChannelType{discordgo.ChannelTypeGuildText, discordgo.ChannelTypeGuildPublicThread, discordgo.ChannelTypeGuildPrivateThread}

// store is the part of the data layer /reddit uses: the guild's feed list and the delivery records of a feed.
type store interface {
	GetFeeds(ctx context.Context, guildID string) ([]guilds.FeedEntry, error)
	AddFeed(ctx context.Context, guildID string, feed guilds.FeedEntry) error
	// RemoveExactFeed removes exactly the one entry, never its filtered siblings.
	RemoveExactFeed(ctx context.Context, guildID string, feed guilds.FeedEntry) (int64, error)
	// DeleteChecks forgets what was delivered for a feed (the feed check records).
	DeleteChecks(ctx context.Context, guildID, channelID, subreddit, postType, author, title string) error
}

type dbStore struct{ db *database.Client }

func (s dbStore) GetFeeds(ctx context.Context, guildID string) ([]guilds.FeedEntry, error) {
	return s.db.Guilds().GetFeeds(ctx, guildID)
}

func (s dbStore) AddFeed(ctx context.Context, guildID string, feed guilds.FeedEntry) error {
	return s.db.Guilds().AddFeed(ctx, guildID, feed)
}

func (s dbStore) RemoveExactFeed(ctx context.Context, guildID string, feed guilds.FeedEntry) (int64, error) {
	return s.db.Guilds().RemoveExactFeed(ctx, guildID, feed)
}

func (s dbStore) DeleteChecks(ctx context.Context, guildID, channelID, subreddit, postType, author, title string) error {
	_, err := s.db.FeedChecks().DeleteByFeed(ctx, guildID, channelID, subreddit, postType, author, title)
	return err
}

// storeOf picks the store of a database client; tests replace it.
var storeOf = func(db *database.Client) store { return dbStore{db} }

// checkFeed asks Reddit whether a subreddit's feed of that type can be read; tests replace it so they do not use
// the network.
var checkFeed = func(ctx context.Context, subreddit, postType string) error {
	_, err := reddit.FetchWithRetry(ctx, subreddit, postType)
	return err
}

func init() {
	commands.Add(&commands.Command{
		Name:       "reddit",
		Desc:       "Add, remove and list Reddit feeds.",
		Visibility: commands.Private,
		Response:   commands.Deferred,
		// Guard home: the moderator check is Discord's default member permissions (Manage Server) and server overrides.
		Permission: commands.PermMod,
		Context:    commands.ContextGuildOnly,
		Module:     "reddit",
		Subcommands: []*commands.Subcommand{
			addSubcommand(),
			removeSubcommand(),
			listSubcommand(),
		},
	})
	// Restricted: the list and its picker belong to the moderator who ran it.
	commands.RegisterServerPaginationRenderer(pageKey, RenderFeedsPage)
	commands.AddComponent(pickerPrefix, handleRemovePicker)
}

func normalizeSubreddit(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "/r/")
	s = strings.TrimPrefix(s, "r/")
	return strings.ToLower(s)
}

func normalizeAuthor(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "/u/")
	s = strings.TrimPrefix(s, "u/")
	return s
}

func stringOption(ic *interaction.Context, name string) string {
	if o := commands.ParseOption(ic.Command.Options, name); o != nil {
		return o.StringValue()
	}
	return ""
}

// channelOption is the ID of the channel option, "" when it is missing.
func channelOption(ic *interaction.Context) string {
	o := commands.ParseOption(ic.Command.Options, optChannel)
	if o == nil {
		return ""
	}
	if ch := o.ChannelValue(nil); ch != nil {
		return ch.ID
	}
	return ""
}

// clip cuts text to at most n characters.
func clip(text string) string {
	const n = maxChoiceLength
	r := []rune(text)
	if len(r) <= n {
		return text
	}
	return string(r[:n])
}

// ellipsize cuts text to at most n characters, ending in an ellipsis when it was cut.
func ellipsize(text string, n int) string {
	r := []rune(text)
	if len(r) <= n {
		return text
	}
	return string(r[:n-1]) + "…"
}

func answer(ic *interaction.Context, text string) error {
	return ic.EditReply(interaction.Response{Embeds: []*discordgo.MessageEmbed{commands.NewEmbed(nil, embedTitle, text)}})
}

func logStoreFailure(ic *interaction.Context, what string, err error) {
	logger.For("commands").Error(what, "guild_id", ic.GuildID, "err", err)
}

// postTypeOption reads the post-type option. A missing or blank one gives def; an unknown one is not ok.
func postTypeOption(ic *interaction.Context, def string) (postType string, ok bool) {
	raw := strings.ToLower(strings.TrimSpace(stringOption(ic, optPostType)))
	if raw == "" {
		return def, true
	}
	return raw, slices.Contains(postTypeChoices, raw)
}

const msgInvalidPostType = "Invalid post type. Use one of: hot, rising, new."

func autocompletePostType(_ context.Context, _ *database.Client, _ *discordgo.Session, _ *discordgo.InteractionCreate, _, focusedValue string) []*discordgo.ApplicationCommandOptionChoice {
	names := commands.MatchAnywhere(postTypeChoices, focusedValue, func(n string) []string { return []string{n} })
	if len(names) == 0 {
		names = postTypeChoices
	}
	choices := make([]*discordgo.ApplicationCommandOptionChoice, 0, len(names))
	for _, name := range names {
		choices = append(choices, &discordgo.ApplicationCommandOptionChoice{Name: name, Value: name})
	}
	return choices
}
