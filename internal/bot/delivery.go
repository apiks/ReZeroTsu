package bot

import (
	"context"

	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/database/anime_subs"
	"ReZeroTsu/internal/database/feed_checks"
	"ReZeroTsu/internal/database/guilds"
	"ReZeroTsu/internal/database/reminders"

	"github.com/bwmarrin/discordgo"
)

// WebhookTarget identifies a webhook to send through (ThreadID set when the target channel is a thread).
type WebhookTarget struct {
	ID       string
	Token    string
	ThreadID string
}

// OutgoingMessage is what a delivery run wants to post.
type OutgoingMessage struct {
	Content string
	Embeds  []*discordgo.MessageEmbed
}

// ChannelMessage is a message read back from a channel.
type ChannelMessage struct {
	ID       string
	AuthorID string
	Content  string
	Embeds   []*discordgo.MessageEmbed
}

// DiscordPort is the narrow Discord surface the per-target delivery runs depend on.
// Production wiring is discordgo; tests use an in-memory fake.
type DiscordPort interface {
	// Send posts msg to channelID through webhook, or through the bot account when webhook is nil.
	// It returns the new message ID when Discord reports one (webhook sends do not).
	Send(ctx context.Context, channelID string, webhook *WebhookTarget, msg OutgoingMessage) (messageID string, err error)
	// OpenDM returns the DM channel ID for userID.
	OpenDM(ctx context.Context, userID string) (channelID string, err error)
	// RecentMessages returns up to limit most recent messages in channelID, newest first.
	RecentMessages(ctx context.Context, channelID string, limit int) ([]ChannelMessage, error)

	// ChannelWebhook returns a usable webhook for the channel, or nil when none can be used.
	ChannelWebhook(guildID, channelID string) *WebhookTarget
	// InvalidateChannelWebhook drops the cached webhook for the channel.
	InvalidateChannelWebhook(guildID, channelID string)
	// CanSend reports whether the bot may post embeds to the channel.
	CanSend(channelID string) bool
	// GuildHasRole reports whether the role still exists in the guild.
	GuildHasRole(guildID, roleID string) bool
	// PinReplacing pins messageID, first unpinning the bot's older pins for the same subreddit.
	PinReplacing(channelID, messageID, subreddit string)
	// ShardID is used for log attribution only.
	ShardID() int
	// BotUserID is the bot's own user ID, used to recognise its messages when verifying an Unknown send.
	BotUserID() string
}

// SentState is the narrow persistence surface the per-target delivery runs depend on:
// the "already sent" records plus the per-guild configuration read alongside them.
// Production wiring is the Mongo repositories; tests use an in-memory fake.
type SentState interface {
	GetAnimeSubs(ctx context.Context, id string) (*anime_subs.AnimeSubs, error)
	SetAnimeSubs(ctx context.Context, id string, isGuild bool, shows []*anime_subs.ShowSub) error
	// MarkShowsNotified sets notified=true for the named shows in one single-statement update.
	MarkShowsNotified(ctx context.Context, id string, showNames []string) error
	// SeedAnimeSubs fully rewrites the document, stamping seedDate (YYYY-MM-DD, UTC) as its last seed date.
	SeedAnimeSubs(ctx context.Context, id string, isGuild bool, shows []*anime_subs.ShowSub, seedDate string) error
	// AppendAnimeSubShows adds shows to an existing document without touching its other entries or seed date.
	AppendAnimeSubShows(ctx context.Context, id string, shows []*anime_subs.ShowSub) error
	DeleteAnimeSubs(ctx context.Context, id string) error

	GetAutopost(ctx context.Context, guildID, postType string) (*guilds.Autopost, error)
	// SetAutopost stores ap, or clears the autopost when ap is nil.
	SetAutopost(ctx context.Context, guildID, postType string, ap *guilds.Autopost) error
	GetGuildSettings(ctx context.Context, guildID string) (*guilds.GuildSettings, error)

	SaveFeedCheck(ctx context.Context, guildID string, fc feed_checks.FeedCheck) error
	RemoveFeedsByChannel(ctx context.Context, guildID, channelID string) (int64, error)
	DeleteFeedChecksByChannel(ctx context.Context, guildID, channelID string) (int64, error)

	// SaveReminders replaces the user's reminders document; nil or empty deletes it.
	SaveReminders(ctx context.Context, id string, slice *reminders.RemindMeSlice) error

	// --- ticket #7 (user DMs) ---

	// MarkUserShowNotified records one show of a user's document as notified with its air time and episode
	// key, in a single-statement update (matched by show name).
	MarkUserShowNotified(ctx context.Context, id, showName string, airUnix int64, episodeKey string) error
}

// --- production wiring: discordgo ---

type discordgoPort struct {
	s *discordgo.Session
}

func newDiscordgoPort(s *discordgo.Session) DiscordPort { return &discordgoPort{s: s} }

func (p *discordgoPort) Send(ctx context.Context, channelID string, webhook *WebhookTarget, msg OutgoingMessage) (string, error) {
	if webhook != nil {
		w := &discordgo.Webhook{ID: webhook.ID, Token: webhook.Token}
		return "", SendBatchToChannel(ctx, p.s, w, webhook.ThreadID, channelID, msg.Content, msg.Embeds, true)
	}
	m, err := p.s.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{Content: msg.Content, Embeds: msg.Embeds})
	if err != nil {
		return "", err
	}
	if m == nil {
		return "", nil
	}
	return m.ID, nil
}

func (p *discordgoPort) OpenDM(_ context.Context, userID string) (string, error) {
	ch, err := p.s.UserChannelCreate(userID)
	if err != nil {
		return "", err
	}
	return ch.ID, nil
}

func (p *discordgoPort) RecentMessages(_ context.Context, channelID string, limit int) ([]ChannelMessage, error) {
	msgs, err := p.s.ChannelMessages(channelID, limit, "", "", "")
	if err != nil {
		return nil, err
	}
	out := make([]ChannelMessage, 0, len(msgs))
	for _, m := range msgs {
		if m == nil {
			continue
		}
		cm := ChannelMessage{ID: m.ID, Content: m.Content, Embeds: m.Embeds}
		if m.Author != nil {
			cm.AuthorID = m.Author.ID
		}
		out = append(out, cm)
	}
	return out, nil
}

func (p *discordgoPort) ChannelWebhook(guildID, channelID string) *WebhookTarget {
	w, threadID, ok := getOrCreateChannelWebhook(p.s, guildID, channelID)
	if !ok || w == nil {
		return nil
	}
	return &WebhookTarget{ID: w.ID, Token: w.Token, ThreadID: threadID}
}

func (p *discordgoPort) InvalidateChannelWebhook(guildID, channelID string) {
	invalidateChannelWebhook(guildID, channelID)
}

func (p *discordgoPort) CanSend(channelID string) bool { return canSendToChannel(p.s, channelID) }

func (p *discordgoPort) GuildHasRole(guildID, roleID string) bool {
	g, err := p.s.State.Guild(guildID)
	if err != nil {
		return false
	}
	return roleExistsInGuild(g, roleID)
}

func (p *discordgoPort) PinReplacing(channelID, messageID, subreddit string) {
	handleFeedPinning(p.s, channelID, messageID, subreddit)
}

func (p *discordgoPort) ShardID() int { return p.s.ShardID }

func (p *discordgoPort) BotUserID() string {
	if p.s.State != nil && p.s.State.User != nil {
		return p.s.State.User.ID
	}
	return ""
}

// --- production wiring: Mongo repositories ---

type mongoSentState struct {
	db *database.Client
}

func newMongoSentState(db *database.Client) SentState { return &mongoSentState{db: db} }

func (m *mongoSentState) GetAnimeSubs(ctx context.Context, id string) (*anime_subs.AnimeSubs, error) {
	return m.db.AnimeSubs().GetByID(ctx, id)
}

func (m *mongoSentState) SetAnimeSubs(ctx context.Context, id string, isGuild bool, shows []*anime_subs.ShowSub) error {
	return m.db.AnimeSubs().Set(ctx, id, isGuild, shows)
}

func (m *mongoSentState) MarkShowsNotified(ctx context.Context, id string, showNames []string) error {
	return m.db.AnimeSubs().MarkShowsNotified(ctx, id, showNames)
}

func (m *mongoSentState) SeedAnimeSubs(ctx context.Context, id string, isGuild bool, shows []*anime_subs.ShowSub, seedDate string) error {
	return m.db.AnimeSubs().Seed(ctx, id, isGuild, shows, seedDate)
}

func (m *mongoSentState) AppendAnimeSubShows(ctx context.Context, id string, shows []*anime_subs.ShowSub) error {
	return m.db.AnimeSubs().AppendShows(ctx, id, shows)
}

func (m *mongoSentState) DeleteAnimeSubs(ctx context.Context, id string) error {
	return m.db.AnimeSubs().DeleteByID(ctx, id)
}

func (m *mongoSentState) GetAutopost(ctx context.Context, guildID, postType string) (*guilds.Autopost, error) {
	return m.db.Guilds().GetAutopostByType(ctx, guildID, postType)
}

func (m *mongoSentState) SetAutopost(ctx context.Context, guildID, postType string, ap *guilds.Autopost) error {
	return m.db.Guilds().SetAutopost(ctx, guildID, postType, ap)
}

func (m *mongoSentState) GetGuildSettings(ctx context.Context, guildID string) (*guilds.GuildSettings, error) {
	return m.db.Guilds().GetGuildSettings(ctx, guildID)
}

func (m *mongoSentState) SaveFeedCheck(ctx context.Context, guildID string, fc feed_checks.FeedCheck) error {
	return m.db.FeedChecks().Save(ctx, guildID, fc)
}

func (m *mongoSentState) RemoveFeedsByChannel(ctx context.Context, guildID, channelID string) (int64, error) {
	return m.db.Guilds().RemoveFeedsByChannel(ctx, guildID, channelID)
}

func (m *mongoSentState) DeleteFeedChecksByChannel(ctx context.Context, guildID, channelID string) (int64, error) {
	return m.db.FeedChecks().DeleteByChannelID(ctx, guildID, channelID)
}

func (m *mongoSentState) SaveReminders(ctx context.Context, id string, slice *reminders.RemindMeSlice) error {
	return m.db.Reminders().Save(ctx, id, slice)
}

func (m *mongoSentState) MarkUserShowNotified(ctx context.Context, id, showName string, airUnix int64, episodeKey string) error {
	return m.db.AnimeSubs().MarkShowNotified(ctx, id, showName, airUnix, episodeKey)
}
