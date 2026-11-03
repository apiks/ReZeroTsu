package bot

import (
	"context"
	"time"

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
	// Timestamp is when the message was posted; zero when unknown.
	Timestamp time.Time
}

// RunOperations is the Discord surface handed to delivery runs: everything a run may do besides post messages,
// which only the channel sender does (through RawDiscord).
type RunOperations interface {
	// OpenDM returns the DM channel ID for userID.
	OpenDM(ctx context.Context, userID string) (channelID string, err error)
	// CanSend reports whether the bot may post embeds to the channel.
	CanSend(channelID string) bool
	// GuildRole reports whether the role still exists in the guild, or that the state cannot tell.
	GuildRole(guildID, roleID string) rolePresence
	// PinReplacing pins messageID, first unpinning the bot's older pins for the same subreddit.
	PinReplacing(channelID, messageID, subreddit string)
	// ShardID is used for log attribution only.
	ShardID() int
}

// RawDiscord is the raw Discord surface handed only to the channel sender factory; delivery runs never hold it.
type RawDiscord interface {
	// Send posts msg to channelID through webhook, or through the bot account when webhook is nil.
	// It returns the new message ID when Discord reports one (webhook sends do not).
	Send(ctx context.Context, channelID string, webhook *WebhookTarget, msg OutgoingMessage) (messageID string, err error)
	// RecentMessages returns up to limit most recent messages in channelID, newest first.
	RecentMessages(ctx context.Context, channelID string, limit int) ([]ChannelMessage, error)
	// BotUserID is the bot's own user ID, used to recognise its messages when verifying an Unknown send.
	BotUserID() string

	// ResolveWebhook returns a usable webhook for the channel, or nil when none can be used. It does no caching:
	// the channel sender owns the webhook cache.
	ResolveWebhook(guildID, channelID string) *WebhookTarget
	// WebhookUsable reports from the cached state alone (no REST call) whether the bot may still use a webhook in
	// the channel: the channel is known and the bot has the permissions webhook delivery needs. The channel
	// sender asks before it uses a cached webhook, since a webhook send keeps working after the bot lost them.
	WebhookUsable(channelID string) bool
}

var (
	_ RunOperations = (*discordgoPort)(nil)
	_ RawDiscord    = (*discordgoPort)(nil)
)

// SentState is the narrow persistence surface the per-target delivery runs depend on:
// the "already sent" records plus the per-guild configuration read alongside them.
// Production wiring is the Mongo repositories; tests use an in-memory fake.
type SentState interface {
	GetAnimeSubs(ctx context.Context, id string) (*anime_subs.AnimeSubs, error)
	// MarkShowsNotified sets notified=true on the entries of the given identities (show, track, episode) in one
	// single-statement update.
	MarkShowsNotified(ctx context.Context, id string, shows []*anime_subs.ShowSub) error
	// SeedAnimeSubs fully rewrites the document, stamping seedDate (YYYY-MM-DD, UTC) as its last seed date.
	SeedAnimeSubs(ctx context.Context, id string, isGuild bool, shows []*anime_subs.ShowSub, seedDate string) error
	// AppendAnimeSubShows adds shows to an existing document without touching its other entries or seed date.
	AppendAnimeSubShows(ctx context.Context, id string, shows []*anime_subs.ShowSub) error
	DeleteAnimeSubs(ctx context.Context, id string) error

	GetAutopost(ctx context.Context, guildID, postType string) (*guilds.Autopost, error)
	// SetAutopost stores ap, or clears the autopost when ap is nil.
	SetAutopost(ctx context.Context, guildID, postType string, ap *guilds.Autopost) error
	GetGuildSettings(ctx context.Context, guildID string) (*guilds.GuildSettings, error)

	// SaveFeedChecks upserts the feed checks by (guild, GUID) in one round trip.
	SaveFeedChecks(ctx context.Context, guildID string, fcs []feed_checks.FeedCheck) error
	RemoveFeedsByChannel(ctx context.Context, guildID, channelID string) (int64, error)
	DeleteFeedChecksByChannel(ctx context.Context, guildID, channelID string) (int64, error)

	// --- ticket #11: reminders delivered once ---

	// ListDueReminders returns the reminders documents holding at least one reminder due at or before asOf.
	ListDueReminders(ctx context.Context, asOf time.Time) ([]*reminders.RemindMeSlice, error)
	// RemoveReminders atomically removes the reminders matching both the ID and the due time of the given refs
	// from the user's document and deletes the document once it is empty. Other reminders (including ones added
	// meanwhile, even with a re-used ID) are untouched.
	RemoveReminders(ctx context.Context, id string, refs []reminders.RemindRef) error
	// MarkRemindersFailing atomically records since as the first-failure time of the reminders matching the refs
	// (by ID and due time), leaving reminders that already have one unchanged.
	MarkRemindersFailing(ctx context.Context, id string, refs []reminders.RemindRef, since time.Time) error

	// --- ticket #9 (daily schedule) ---

	// MarkSchedulePosted sets last_posted_date (UTC YYYY-MM-DD) on the guild's daily-schedule autopost entry.
	MarkSchedulePosted(ctx context.Context, guildID, date string) error

	// --- ticket #7 (user DMs) ---

	// MarkUserShowNotified records one show of a user's document as notified, with the air time and episode key
	// of each given track member, in a single-statement update (matched by show name).
	MarkUserShowNotified(ctx context.Context, id, showName string, marks []anime_subs.Mark) error
	// SetUserShowRoute records route on one show of a user's document that has none, in a single-statement
	// update (matched by show name).
	SetUserShowRoute(ctx context.Context, id, showName, route string) error

	// --- ticket #10 (feeds) ---

	// ExistingFeedGUIDs returns the subset of guids that already have a feed check for the guild.
	ExistingFeedGUIDs(ctx context.Context, guildID string, guids []string) (map[string]struct{}, error)
}

// --- production wiring: discordgo ---

type discordgoPort struct {
	s *discordgo.Session
}

func newDiscordgoPort(s *discordgo.Session) *discordgoPort {
	return &discordgoPort{s: s}
}

// Send deliberately does not pass ctx to the REST call itself (ctx only gates the send limiters): aborting an
// in-flight send would turn a message that Discord may still post into an Unknown outcome.
func (p *discordgoPort) Send(ctx context.Context, channelID string, webhook *WebhookTarget, msg OutgoingMessage) (string, error) {
	if webhook != nil {
		w := &discordgo.Webhook{ID: webhook.ID, Token: webhook.Token}
		return "", SendBatchToChannel(ctx, p.s, w, webhook.ThreadID, channelID, msg.Content, msg.Embeds, true)
	}
	m, err := p.s.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{Content: msg.Content, Embeds: msg.Embeds}, noLibraryRetry)
	if err != nil {
		return "", err
	}
	if m == nil {
		return "", nil
	}
	return m.ID, nil
}

func (p *discordgoPort) OpenDM(ctx context.Context, userID string) (string, error) {
	ch, err := p.s.UserChannelCreate(userID, discordgo.WithContext(ctx))
	if err != nil {
		return "", err
	}
	return ch.ID, nil
}

func (p *discordgoPort) RecentMessages(ctx context.Context, channelID string, limit int) ([]ChannelMessage, error) {
	msgs, err := p.s.ChannelMessages(channelID, limit, "", "", "", discordgo.WithContext(ctx))
	if err != nil {
		return nil, err
	}
	out := make([]ChannelMessage, 0, len(msgs))
	for _, m := range msgs {
		if m == nil {
			continue
		}
		cm := ChannelMessage{ID: m.ID, Content: m.Content, Embeds: m.Embeds, Timestamp: m.Timestamp}
		if m.Author != nil {
			cm.AuthorID = m.Author.ID
		}
		out = append(out, cm)
	}
	return out, nil
}

func (p *discordgoPort) ResolveWebhook(_, channelID string) *WebhookTarget {
	return resolveChannelWebhook(p.s, channelID)
}

func (p *discordgoPort) WebhookUsable(channelID string) bool {
	_, _, ok := webhookChannelFromState(p.s, channelID)
	return ok
}

func (p *discordgoPort) CanSend(channelID string) bool { return canSendToChannel(p.s, channelID) }

func (p *discordgoPort) GuildRole(guildID, roleID string) rolePresence {
	if p.s.State == nil {
		return roleUnknown
	}
	g, err := p.s.State.Guild(guildID)
	if err != nil {
		// Not in the state (e.g. removed by an unavailable GUILD_DELETE): unknown, not absent.
		return roleUnknown
	}
	// The guild struct is owned by the state cache; read it under the state lock.
	p.s.State.RLock()
	defer p.s.State.RUnlock()
	return guildRolePresence(g, roleID)
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

func (m *mongoSentState) MarkShowsNotified(ctx context.Context, id string, shows []*anime_subs.ShowSub) error {
	return m.db.AnimeSubs().MarkShowsNotified(ctx, id, shows)
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

func (m *mongoSentState) SaveFeedChecks(ctx context.Context, guildID string, fcs []feed_checks.FeedCheck) error {
	return m.db.FeedChecks().SaveMany(ctx, guildID, fcs)
}

func (m *mongoSentState) RemoveFeedsByChannel(ctx context.Context, guildID, channelID string) (int64, error) {
	return m.db.Guilds().RemoveFeedsByChannel(ctx, guildID, channelID)
}

func (m *mongoSentState) DeleteFeedChecksByChannel(ctx context.Context, guildID, channelID string) (int64, error) {
	return m.db.FeedChecks().DeleteByChannelID(ctx, guildID, channelID)
}

func (m *mongoSentState) ListDueReminders(ctx context.Context, asOf time.Time) ([]*reminders.RemindMeSlice, error) {
	return m.db.Reminders().ListDue(ctx, asOf)
}

func (m *mongoSentState) RemoveReminders(ctx context.Context, id string, refs []reminders.RemindRef) error {
	return m.db.Reminders().RemoveByRefs(ctx, id, refs)
}

func (m *mongoSentState) MarkRemindersFailing(ctx context.Context, id string, refs []reminders.RemindRef, since time.Time) error {
	return m.db.Reminders().MarkFailingSince(ctx, id, refs, since)
}

func (m *mongoSentState) MarkSchedulePosted(ctx context.Context, guildID, date string) error {
	return m.db.Guilds().MarkAutopostPosted(ctx, guildID, postTypeDailySchedule, date)
}

func (m *mongoSentState) MarkUserShowNotified(ctx context.Context, id, showName string, marks []anime_subs.Mark) error {
	return m.db.AnimeSubs().MarkShowNotified(ctx, id, showName, marks)
}

func (m *mongoSentState) SetUserShowRoute(ctx context.Context, id, showName, route string) error {
	return m.db.AnimeSubs().SetShowRoute(ctx, id, showName, route)
}

func (m *mongoSentState) ExistingFeedGUIDs(ctx context.Context, guildID string, guids []string) (map[string]struct{}, error) {
	found, err := m.db.FeedChecks().ExistingGUIDs(ctx, guildID, guids)
	if err != nil {
		return nil, err
	}
	out := make(map[string]struct{}, len(found))
	for _, g := range found {
		out[g] = struct{}{}
	}
	return out, nil
}
