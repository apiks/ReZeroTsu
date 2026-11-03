package guilds

import (
	"context"
	"fmt"

	"ReZeroTsu/internal/animeschedule"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// ChannelRef is a channel reference in guild_settings.
type ChannelRef struct {
	Name   string `bson:"name,omitempty"`
	ID     string `bson:"id,omitempty"`
	RoleID string `bson:"role_id,omitempty"`
}

// Role is a role reference for VoiceCha.
type Role struct {
	Name     string `bson:"name,omitempty"`
	ID       string `bson:"id,omitempty"`
	Position int    `bson:"position,omitempty"`
}

// VoiceCha is a voice channel with optional roles.
type VoiceCha struct {
	Name  string `bson:"name,omitempty"`
	ID    string `bson:"id,omitempty"`
	Roles []Role `bson:"roles,omitempty"`
}

type Autopost struct {
	PostType string `bson:"post_type"`
	Name     string `bson:"name"`
	ID       string `bson:"id"`
	RoleID   string `bson:"role_id,omitempty"`
	// LastPostedDate is the UTC date (YYYY-MM-DD) the daily schedule was last confirmed posted; daily-schedule entries only.
	LastPostedDate string `bson:"last_posted_date,omitempty"`
}

// ReactJoinWrap is one react_join_map entry. MessageID is stored under the legacy key "channel_id".
type ReactJoinWrap struct {
	MessageID string         `bson:"channel_id"`
	ReactJoin ReactJoinMongo `bson:"react_join"`
	// MessageChannelID is the channel the message is in. Records saved before it was kept have none.
	MessageChannelID string `bson:"message_channel_id,omitempty"`
}

type ReactJoinMongo struct {
	RoleEmojiMap []map[string][]string `bson:"role_emoji"`
}

type ReactJoin struct {
	// ChannelID is the channel of the message, or "" for a record saved before it was kept.
	ChannelID    string
	RoleEmojiMap []map[string][]string
}

// reactJoinFromWrap is the message ID and entry a stored record stands for.
func reactJoinFromWrap(w ReactJoinWrap) (messageID string, entry *ReactJoin) {
	return w.MessageID, &ReactJoin{ChannelID: w.MessageChannelID, RoleEmojiMap: w.ReactJoin.RoleEmojiMap}
}

// reactJoinToWrap is the stored record of a message's entry.
func reactJoinToWrap(messageID string, entry *ReactJoin) ReactJoinWrap {
	return ReactJoinWrap{
		MessageID:        messageID,
		ReactJoin:        ReactJoinMongo{RoleEmojiMap: entry.RoleEmojiMap},
		MessageChannelID: entry.ChannelID,
	}
}

type RaffleMongo struct {
	Name           string   `bson:"name"`
	ParticipantIDs []string `bson:"participant_ids,omitempty"`
	ReactMessageID string   `bson:"react_message_id,omitempty"`
	// ChannelID and MessageID locate the raffle's button message; WinnerID is the latest draw's winner.
	// All three are optional, so records stored before button raffles load unchanged.
	ChannelID string `bson:"channel_id,omitempty"`
	MessageID string `bson:"message_id,omitempty"`
	WinnerID  string `bson:"winner_id,omitempty"`
}

type Raffle struct {
	Name           string
	ParticipantIDs []string
	ReactMessageID string
	// ChannelID and MessageID locate the button message; empty for a raffle that has none.
	ChannelID string
	MessageID string
	// WinnerID is the user ID of the latest draw's winner; empty before the first draw.
	WinnerID string
}

func raffleFromMongo(m *RaffleMongo) *Raffle {
	return &Raffle{
		Name:           m.Name,
		ParticipantIDs: append([]string(nil), m.ParticipantIDs...),
		ReactMessageID: m.ReactMessageID,
		ChannelID:      m.ChannelID,
		MessageID:      m.MessageID,
		WinnerID:       m.WinnerID,
	}
}

func raffleToMongo(r *Raffle) RaffleMongo {
	m := RaffleMongo{
		Name:           r.Name,
		ParticipantIDs: r.ParticipantIDs,
		ReactMessageID: r.ReactMessageID,
		ChannelID:      r.ChannelID,
		MessageID:      r.MessageID,
		WinnerID:       r.WinnerID,
	}
	if m.ParticipantIDs == nil {
		m.ParticipantIDs = []string{}
	}
	return m
}

// FeedEntry is one Reddit feed in a guild's feeds array.
type FeedEntry struct {
	Subreddit string `bson:"subreddit,omitempty"`
	Title     string `bson:"title,omitempty"`
	Author    string `bson:"author,omitempty"`
	Pin       bool   `bson:"pin,omitempty"`
	PostType  string `bson:"post_type,omitempty"`
	ChannelID string `bson:"channel_id,omitempty"`
}

// SameIdentity reports whether two entries are the same Reddit feed: channel, subreddit, sort, author and title.
// Auto-pin is a setting of the feed, not part of its identity.
func (f FeedEntry) SameIdentity(g FeedEntry) bool {
	return f.ChannelID == g.ChannelID && f.Subreddit == g.Subreddit && f.PostType == g.PostType &&
		f.Author == g.Author && f.Title == g.Title
}

type GuildSettings struct {
	BotLogID  *ChannelRef `bson:"bot_log_id,omitempty"`
	VoiceChas []VoiceCha  `bson:"voice_chas,omitempty"`
	Donghua   bool        `bson:"donghua,omitempty"`
	// PreferredTrack is "sub", "dub" or "raw"; missing reads as Sub (see Preferred).
	PreferredTrack string `bson:"preferred_track,omitempty"`
	PingMessage    string `bson:"ping_message,omitempty"`
	Premium        bool   `bson:"premium,omitempty"`
}

// Guild is a guild document; _id is the guild ID.
type Guild struct {
	ID            string          `bson:"_id"`
	Autoposts     []Autopost      `bson:"autoposts,omitempty"`
	GuildSettings *GuildSettings  `bson:"guild_settings,omitempty"`
	ReactJoinMap  []ReactJoinWrap `bson:"react_join_map,omitempty"`
	Raffles       []RaffleMongo   `bson:"raffles,omitempty"`
	Feeds         []FeedEntry     `bson:"feeds,omitempty"`
}

// DefaultGuildSettings returns the default guild_settings.
func DefaultGuildSettings() *GuildSettings {
	return &GuildSettings{
		PingMessage: "Hmmm~ So this is what you do all day long?",
	}
}

type Repo struct {
	coll *mongo.Collection
}

func NewRepo(coll *mongo.Collection) *Repo {
	return &Repo{coll: coll}
}

// GetGuild returns the guild for the ID, or nil if not found.
func (r *Repo) GetGuild(ctx context.Context, guildID string) (*Guild, error) {
	var g Guild
	err := r.coll.FindOne(ctx, bson.M{"_id": guildID}).Decode(&g)
	if err == mongo.ErrNoDocuments {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("GetGuild: %w", err)
	}
	return &g, nil
}

// GetGuildSettings returns guild_settings for the guild, or DefaultGuildSettings() if missing.
func (r *Repo) GetGuildSettings(ctx context.Context, guildID string) (*GuildSettings, error) {
	g, err := r.GetGuild(ctx, guildID)
	if err != nil {
		return nil, fmt.Errorf("GetGuildSettings: %w", err)
	}
	if g == nil || g.GuildSettings == nil {
		return DefaultGuildSettings(), nil
	}
	return g.GuildSettings, nil
}

// setSettingsField sets one guild_settings field and nothing else, so a write that lands between a read and this
// call is not lost. The guild document is created with default settings when missing.
func (r *Repo) setSettingsField(ctx context.Context, guildID, field string, value any) error {
	if err := r.EnsureGuild(ctx, guildID); err != nil {
		return err
	}
	// A legacy document without guild_settings gets the defaults first, so the one field does not stand alone.
	if _, err := r.coll.UpdateOne(ctx, bson.M{"_id": guildID, "guild_settings": bson.M{"$exists": false}},
		bson.M{"$set": bson.M{"guild_settings": DefaultGuildSettings()}}); err != nil {
		return err
	}
	_, err := r.coll.UpdateOne(ctx, bson.M{"_id": guildID}, settingsFieldUpdate(field, value))
	return err
}

// settingsFieldUpdate is the update of a one-field settings write. It never replaces the guild_settings
// subdocument, so keys the struct no longer has (command_roles, mod_only, reacts_module) stay stored.
func settingsFieldUpdate(field string, value any) bson.M {
	return bson.M{"$set": bson.M{"guild_settings." + field: value}}
}

// voiceChasField is the guild_settings key of GuildSettings.VoiceChas.
const voiceChasField = "voice_chas"

// SetVoiceChas sets guild_settings.voice_chas alone.
func (r *Repo) SetVoiceChas(ctx context.Context, guildID string, chas []VoiceCha) error {
	if chas == nil {
		chas = []VoiceCha{}
	}
	if err := r.setSettingsField(ctx, guildID, voiceChasField, chas); err != nil {
		return fmt.Errorf("SetVoiceChas: %w", err)
	}
	return nil
}

// SetBotLog sets guild_settings.bot_log_id alone; nil clears it.
func (r *Repo) SetBotLog(ctx context.Context, guildID string, ref *ChannelRef) error {
	var value any = ref
	if ref == nil {
		value = nil
	}
	if err := r.setSettingsField(ctx, guildID, "bot_log_id", value); err != nil {
		return fmt.Errorf("SetBotLog: %w", err)
	}
	return nil
}

// SetDonghua sets guild_settings.donghua alone.
func (r *Repo) SetDonghua(ctx context.Context, guildID string, on bool) error {
	if err := r.setSettingsField(ctx, guildID, "donghua", on); err != nil {
		return fmt.Errorf("SetDonghua: %w", err)
	}
	return nil
}

// preferredTrackField is the guild_settings key of GuildSettings.PreferredTrack.
const preferredTrackField = "preferred_track"

// Preferred is the server's preferred track; a missing or unknown value is Sub.
func (g *GuildSettings) Preferred() animeschedule.PreferredTrack {
	pref, _ := animeschedule.ParsePreferredTrack(g.PreferredTrack)
	return pref
}

// SetPreferredTrack sets guild_settings.preferred_track alone.
func (r *Repo) SetPreferredTrack(ctx context.Context, guildID string, pref animeschedule.PreferredTrack) error {
	if err := r.setSettingsField(ctx, guildID, preferredTrackField, pref.String()); err != nil {
		return fmt.Errorf("SetPreferredTrack: %w", err)
	}
	return nil
}

// SetPingMessage sets guild_settings.ping_message alone.
func (r *Repo) SetPingMessage(ctx context.Context, guildID, text string) error {
	if err := r.setSettingsField(ctx, guildID, "ping_message", text); err != nil {
		return fmt.Errorf("SetPingMessage: %w", err)
	}
	return nil
}

// ClearBotLog clears guild_settings.bot_log_id alone.
func (r *Repo) ClearBotLog(ctx context.Context, guildID string) error {
	if err := r.SetBotLog(ctx, guildID, nil); err != nil {
		return fmt.Errorf("ClearBotLog: %w", err)
	}
	return nil
}

// EnsureGuild creates a minimal guild with default settings if missing. Idempotent ($setOnInsert).
func (r *Repo) EnsureGuild(ctx context.Context, guildID string) error {
	filter := bson.M{"_id": guildID}
	update := bson.M{
		"$setOnInsert": bson.M{
			"_id":            guildID,
			"guild_settings": DefaultGuildSettings(),
			"autoposts":      []Autopost{},
			"react_join_map": []ReactJoinWrap{},
			"raffles":        []RaffleMongo{},
			"feeds":          []FeedEntry{},
		},
	}
	_, err := r.coll.UpdateOne(ctx, filter, update, options.UpdateOne().SetUpsert(true))
	if err != nil {
		return fmt.Errorf("EnsureGuild: %w", err)
	}
	return nil
}

// GetReactJoinMap returns the guild's react join map (messageID → ReactJoin), or empty if none.
func (r *Repo) GetReactJoinMap(ctx context.Context, guildID string) (map[string]*ReactJoin, error) {
	g, err := r.GetGuild(ctx, guildID)
	if err != nil {
		return nil, fmt.Errorf("GetReactJoinMap: %w", err)
	}
	if g == nil || len(g.ReactJoinMap) == 0 {
		return make(map[string]*ReactJoin), nil
	}
	out := make(map[string]*ReactJoin, len(g.ReactJoinMap))
	for _, w := range g.ReactJoinMap {
		messageID, entry := reactJoinFromWrap(w)
		out[messageID] = entry
	}
	return out, nil
}

// SaveReactJoinEntry sets or updates the react join entry for a message; upserts guild if missing.
func (r *Repo) SaveReactJoinEntry(ctx context.Context, guildID, messageID string, entry *ReactJoin) error {
	if entry == nil {
		entry = &ReactJoin{}
	}
	filter := bson.M{"_id": guildID}
	pull := bson.M{"$pull": bson.M{"react_join_map": bson.M{"channel_id": messageID}}}
	_, _ = r.coll.UpdateOne(ctx, filter, pull)
	wrap := reactJoinToWrap(messageID, entry)
	push := bson.M{"$push": bson.M{"react_join_map": wrap}}
	_, err := r.coll.UpdateOne(ctx, filter, push, options.UpdateOne().SetUpsert(true))
	if err != nil {
		return fmt.Errorf("SaveReactJoinEntry: %w", err)
	}
	return nil
}

func (r *Repo) DeleteReactJoinEntry(ctx context.Context, guildID, messageID string) error {
	filter := bson.M{"_id": guildID}
	update := bson.M{"$pull": bson.M{"react_join_map": bson.M{"channel_id": messageID}}}
	_, err := r.coll.UpdateOne(ctx, filter, update)
	if err != nil {
		return fmt.Errorf("DeleteReactJoinEntry: %w", err)
	}
	return nil
}

// GetRaffles returns all raffles for the guild, or empty slice if none.
func (r *Repo) GetRaffles(ctx context.Context, guildID string) ([]*Raffle, error) {
	g, err := r.GetGuild(ctx, guildID)
	if err != nil {
		return nil, fmt.Errorf("GetRaffles: %w", err)
	}
	if g == nil || len(g.Raffles) == 0 {
		return []*Raffle{}, nil
	}
	out := make([]*Raffle, len(g.Raffles))
	for i := range g.Raffles {
		out[i] = raffleFromMongo(&g.Raffles[i])
	}
	return out, nil
}

// SetRaffle upserts a raffle by name.
func (r *Repo) SetRaffle(ctx context.Context, guildID string, raffle *Raffle) error {
	if raffle == nil {
		return nil
	}
	mongoRaffle := raffleToMongo(raffle)
	filter := bson.M{"_id": guildID, "raffles.name": raffle.Name}
	update := bson.M{"$set": bson.M{"raffles.$": mongoRaffle}}
	res, err := r.coll.UpdateOne(ctx, filter, update)
	if err != nil {
		return fmt.Errorf("SetRaffle: %w", err)
	}
	if res.ModifiedCount > 0 {
		return nil
	}
	filter = bson.M{"_id": guildID}
	push := bson.M{"$push": bson.M{"raffles": mongoRaffle}}
	_, err = r.coll.UpdateOne(ctx, filter, push, options.UpdateOne().SetUpsert(true))
	if err != nil {
		return fmt.Errorf("SetRaffle: %w", err)
	}
	return nil
}

// SetRaffleWinner stores the latest draw's winner in one statement, leaving the entrants untouched.
func (r *Repo) SetRaffleWinner(ctx context.Context, guildID, raffleName, winnerID string) error {
	filter := bson.M{"_id": guildID, "raffles.name": raffleName}
	update := bson.M{"$set": bson.M{"raffles.$.winner_id": winnerID}}
	if _, err := r.coll.UpdateOne(ctx, filter, update); err != nil {
		return fmt.Errorf("SetRaffleWinner: %w", err)
	}
	return nil
}

func (r *Repo) DeleteRaffle(ctx context.Context, guildID, name string) error {
	filter := bson.M{"_id": guildID}
	update := bson.M{"$pull": bson.M{"raffles": bson.M{"name": name}}}
	_, err := r.coll.UpdateOne(ctx, filter, update)
	if err != nil {
		return fmt.Errorf("DeleteRaffle: %w", err)
	}
	return nil
}

// GetFeeds returns all Reddit feeds for the guild, or empty slice if none.
func (r *Repo) GetFeeds(ctx context.Context, guildID string) ([]FeedEntry, error) {
	g, err := r.GetGuild(ctx, guildID)
	if err != nil {
		return nil, fmt.Errorf("GetFeeds: %w", err)
	}
	if g == nil || len(g.Feeds) == 0 {
		return []FeedEntry{}, nil
	}
	out := make([]FeedEntry, len(g.Feeds))
	copy(out, g.Feeds)
	return out, nil
}

// AddFeed adds a Reddit feed; idempotent (same feed not duplicated).
func (r *Repo) AddFeed(ctx context.Context, guildID string, feed FeedEntry) error {
	if err := r.EnsureGuild(ctx, guildID); err != nil {
		return fmt.Errorf("AddFeed: %w", err)
	}
	filter := bson.M{"_id": guildID}
	update := bson.M{"$addToSet": bson.M{"feeds": feed}}
	_, err := r.coll.UpdateOne(ctx, filter, update)
	if err != nil {
		return fmt.Errorf("AddFeed: %w", err)
	}
	return nil
}

// SetFeedPin sets the auto-pin setting of exactly one feed, identified by channel, subreddit, sort, author and
// title (an empty author or title matches a missing field), in one update.
func (r *Repo) SetFeedPin(ctx context.Context, guildID string, feed FeedEntry, pin bool) error {
	match := bson.M{
		"subreddit":  feed.Subreddit,
		"channel_id": feed.ChannelID,
		"post_type":  feed.PostType,
		"author":     nil,
		"title":      nil,
	}
	if feed.Author != "" {
		match["author"] = feed.Author
	}
	if feed.Title != "" {
		match["title"] = feed.Title
	}
	_, err := r.coll.UpdateOne(ctx,
		bson.M{"_id": guildID, "feeds": bson.M{"$elemMatch": match}},
		bson.M{"$set": bson.M{"feeds.$.pin": pin}})
	if err != nil {
		return fmt.Errorf("SetFeedPin: %w", err)
	}
	return nil
}

// RemoveExactFeed pulls exactly one entry in one update: channel, subreddit and sort match, and author and
// title match their value, or a missing field when empty (nil matches an omitted omitempty field). An empty author
// or title never widens the match to its filtered siblings.
func (r *Repo) RemoveExactFeed(ctx context.Context, guildID string, feed FeedEntry) (int64, error) {
	pull := bson.M{
		"subreddit":  feed.Subreddit,
		"channel_id": feed.ChannelID,
		"post_type":  feed.PostType,
		"author":     nil,
		"title":      nil,
	}
	if feed.Author != "" {
		pull["author"] = feed.Author
	}
	if feed.Title != "" {
		pull["title"] = feed.Title
	}
	res, err := r.coll.UpdateOne(ctx, bson.M{"_id": guildID}, bson.M{"$pull": bson.M{"feeds": pull}})
	if err != nil {
		return 0, fmt.Errorf("RemoveExactFeed: %w", err)
	}
	return res.ModifiedCount, nil
}

// RemoveFeedsByChannel removes all feeds for the channel.
func (r *Repo) RemoveFeedsByChannel(ctx context.Context, guildID, channelID string) (int64, error) {
	filter := bson.M{"_id": guildID}
	update := bson.M{"$pull": bson.M{"feeds": bson.M{"channel_id": channelID}}}
	res, err := r.coll.UpdateOne(ctx, filter, update)
	if err != nil {
		return 0, fmt.Errorf("RemoveFeedsByChannel: %w", err)
	}
	return res.ModifiedCount, nil
}

// RemoveFeedsBySubredditAndPostType removes all Reddit feeds for the subreddit and sort (stored as post_type).
func (r *Repo) RemoveFeedsBySubredditAndPostType(ctx context.Context, guildID, subreddit, postType string) (int64, error) {
	filter := bson.M{"_id": guildID}
	update := bson.M{"$pull": bson.M{"feeds": bson.M{"subreddit": subreddit, "post_type": postType}}}
	res, err := r.coll.UpdateOne(ctx, filter, update)
	if err != nil {
		return 0, fmt.Errorf("RemoveFeedsBySubredditAndPostType: %w", err)
	}
	return res.ModifiedCount, nil
}

// GetAutopostByType returns the autopost for post_type (e.g. "dailyschedule"), or nil if not found.
func (r *Repo) GetAutopostByType(ctx context.Context, guildID, postType string) (*Autopost, error) {
	g, err := r.GetGuild(ctx, guildID)
	if err != nil {
		return nil, fmt.Errorf("GetAutopostByType: %w", err)
	}
	if g == nil || len(g.Autoposts) == 0 {
		return nil, nil
	}
	for i := range g.Autoposts {
		if g.Autoposts[i].PostType == postType {
			return &g.Autoposts[i], nil
		}
	}
	return nil, nil
}

// SetAutopost sets or removes the autopost for post_type; nil or empty ID removes (disables).
func (r *Repo) SetAutopost(ctx context.Context, guildID, postType string, ap *Autopost) error {
	if err := r.EnsureGuild(ctx, guildID); err != nil {
		return fmt.Errorf("SetAutopost: %w", err)
	}
	filter := bson.M{"_id": guildID}
	pull := bson.M{"$pull": bson.M{"autoposts": bson.M{"post_type": postType}}}
	if _, err := r.coll.UpdateOne(ctx, filter, pull); err != nil {
		return fmt.Errorf("SetAutopost: %w", err)
	}
	if ap == nil || ap.ID == "" {
		return nil
	}
	entry := Autopost{
		PostType: postType,
		Name:     ap.Name,
		ID:       ap.ID,
		RoleID:   ap.RoleID,
	}
	push := bson.M{"$push": bson.M{"autoposts": entry}}
	_, err := r.coll.UpdateOne(ctx, filter, push)
	if err != nil {
		return fmt.Errorf("SetAutopost: %w", err)
	}
	return nil
}

// MarkAutopostPosted sets last_posted_date on the guild's autopost entry for postType in one single-statement update.
func (r *Repo) MarkAutopostPosted(ctx context.Context, guildID, postType, date string) error {
	filter := bson.M{"_id": guildID, "autoposts.post_type": postType}
	update := bson.M{"$set": bson.M{"autoposts.$.last_posted_date": date}}
	if _, err := r.coll.UpdateOne(ctx, filter, update); err != nil {
		return fmt.Errorf("MarkAutopostPosted: %w", err)
	}
	return nil
}

// UpdateRaffleParticipant adds the user to the named raffle's entrants, or removes them, in one single-statement
// update. It reports whether a raffle matched, so a caller can tell a raffle deleted meanwhile from a no-op.
func (r *Repo) UpdateRaffleParticipant(ctx context.Context, guildID, raffleName, userID string, remove bool) (bool, error) {
	filter := bson.M{"_id": guildID, "raffles.name": raffleName}
	var update bson.M
	if remove {
		update = bson.M{"$pull": bson.M{"raffles.$.participant_ids": userID}}
	} else {
		update = bson.M{"$addToSet": bson.M{"raffles.$.participant_ids": userID}}
	}
	res, err := r.coll.UpdateOne(ctx, filter, update)
	if err != nil {
		return false, fmt.Errorf("UpdateRaffleParticipant: %w", err)
	}
	return res.MatchedCount > 0, nil
}

// ListBotLogChannelIDs returns all non-empty bot log channel IDs.
func (r *Repo) ListBotLogChannelIDs(ctx context.Context) ([]string, error) {
	filter := bson.M{
		"guild_settings.bot_log_id.id": bson.M{"$exists": true, "$ne": ""},
	}
	opts := options.Find().SetProjection(bson.M{"guild_settings.bot_log_id.id": 1})
	cursor, err := r.coll.Find(ctx, filter, opts)
	if err != nil {
		return nil, fmt.Errorf("ListBotLogChannelIDs: %w", err)
	}
	defer cursor.Close(ctx)
	var channelIDs []string
	seen := make(map[string]struct{})
	for cursor.Next(ctx) {
		var doc struct {
			GuildSettings *struct {
				BotLogID *ChannelRef `bson:"bot_log_id,omitempty"`
			} `bson:"guild_settings,omitempty"`
		}
		if err := cursor.Decode(&doc); err != nil {
			return nil, fmt.Errorf("ListBotLogChannelIDs decode: %w", err)
		}
		if doc.GuildSettings == nil || doc.GuildSettings.BotLogID == nil || doc.GuildSettings.BotLogID.ID == "" {
			continue
		}
		id := doc.GuildSettings.BotLogID.ID
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		channelIDs = append(channelIDs, id)
	}
	if err := cursor.Err(); err != nil {
		return nil, fmt.Errorf("ListBotLogChannelIDs cursor: %w", err)
	}
	return channelIDs, nil
}

func (r *Repo) EnsureIndexes(ctx context.Context) error {
	indexModels := []mongo.IndexModel{
		{Keys: bson.M{"guild_settings.bot_log_id": 1}, Options: options.Index()},
		{Keys: bson.M{"autoposts.post_type": 1}, Options: options.Index()},
		{Keys: bson.M{"react_join_map.channel_id": 1}, Options: options.Index()},
		{Keys: bson.M{"raffles.name": 1}, Options: options.Index()},
		{Keys: bson.M{"feeds.subreddit": 1}, Options: options.Index()},
	}
	_, err := r.coll.Indexes().CreateMany(ctx, indexModels)
	if err != nil {
		return fmt.Errorf("EnsureIndexes: %w", err)
	}
	return nil
}
