// Package feeds holds /reddit: add, remove and list a server's Reddit feeds. The delivery side is in
// internal/bot/feeds.go.
package feeds

import (
	"context"
	"strings"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/database/guilds"
	"ReZeroTsu/internal/interaction"
	"ReZeroTsu/internal/logger"

	"github.com/bwmarrin/discordgo"
)

const (
	embedTitle = "Reddit feeds"

	optFeed      = "feed"
	optChannel   = "channel"
	optSubreddit = "subreddit"
	optSort      = "sort"
	optAuthor    = "author"
	optTitle     = "title"
	optPin       = "pin"

	maxChoiceLength = 100
)

var feedChannelTypes = []discordgo.ChannelType{discordgo.ChannelTypeGuildText, discordgo.ChannelTypeGuildPublicThread, discordgo.ChannelTypeGuildPrivateThread}

// store is the part of the data layer /reddit uses: the guild's feed list and the delivery records of a feed.
type store interface {
	GetFeeds(ctx context.Context, guildID string) ([]guilds.FeedEntry, error)
	// RemoveExactFeed removes exactly the one entry, never its filtered siblings.
	RemoveExactFeed(ctx context.Context, guildID string, feed guilds.FeedEntry) (int64, error)
	// DeleteChecks forgets what was delivered for a feed (the feed check records).
	DeleteChecks(ctx context.Context, guildID, channelID, subreddit, postType, author, title string) error
}

type dbStore struct{ db *database.Client }

func (s dbStore) GetFeeds(ctx context.Context, guildID string) ([]guilds.FeedEntry, error) {
	return s.db.Guilds().GetFeeds(ctx, guildID)
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

func stringOption(ic *interaction.Context, name string) string {
	if o := commands.ParseOption(ic.Command.Options, name); o != nil {
		return o.StringValue()
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
