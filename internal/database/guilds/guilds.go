package guilds

import (
	"context"
	"fmt"

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

// Role is a role reference for command_roles and VoiceCha.
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
}

// ReactJoinWrap is one react_join_map entry; ChannelID holds the message ID (legacy).
type ReactJoinWrap struct {
	ChannelID string         `bson:"channel_id"`
	ReactJoin ReactJoinMongo `bson:"react_join"`
}

type ReactJoinMongo struct {
	RoleEmojiMap []map[string][]string `bson:"role_emoji"`
}

type ReactJoin struct {
	RoleEmojiMap []map[string][]string
}

type RaffleMongo struct {
	Name           string   `bson:"name"`
	ParticipantIDs []string `bson:"participant_ids,omitempty"`
	ReactMessageID string   `bson:"react_message_id,omitempty"`
}

type Raffle struct {
	Name           string
	ParticipantIDs []string
	ReactMessageID string
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

type GuildSettings struct {
	BotLogID     *ChannelRef `bson:"bot_log_id,omitempty"`
	CommandRoles []Role      `bson:"command_roles,omitempty"`
	VoiceChas    []VoiceCha  `bson:"voice_chas,omitempty"`
	ModOnly      bool        `bson:"mod_only,omitempty"`
	Donghua      bool        `bson:"donghua,omitempty"`
	ReactsModule bool        `bson:"reacts_module,omitempty"`
	PingMessage  string      `bson:"ping_message,omitempty"`
	Premium      bool        `bson:"premium,omitempty"`
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
		ReactsModule: true,
		PingMessage:  "Hmmm~ So this is what you do all day long?",
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

// SetGuildSettings updates guild_settings; upserts guild if missing. Nil uses DefaultGuildSettings().
func (r *Repo) SetGuildSettings(ctx context.Context, guildID string, settings *GuildSettings) error {
	if settings == nil {
		settings = DefaultGuildSettings()
	}
	filter := bson.M{"_id": guildID}
	update := bson.M{"$set": bson.M{"guild_settings": settings}}
	_, err := r.coll.UpdateOne(ctx, filter, update, options.UpdateOne().SetUpsert(true))
	if err != nil {
		return fmt.Errorf("SetGuildSettings: %w", err)
	}
	return nil
}

func (r *Repo) ClearBotLog(ctx context.Context, guildID string) error {
	settings, err := r.GetGuildSettings(ctx, guildID)
	if err != nil {
		return fmt.Errorf("ClearBotLog: %w", err)
	}
	if settings == nil {
		return nil
	}
	settings.BotLogID = nil
	if err := r.SetGuildSettings(ctx, guildID, settings); err != nil {
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
		out[w.ChannelID] = &ReactJoin{RoleEmojiMap: w.ReactJoin.RoleEmojiMap}
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
	wrap := ReactJoinWrap{
		ChannelID: messageID,
		ReactJoin: ReactJoinMongo{RoleEmojiMap: entry.RoleEmojiMap},
	}
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
		m := &g.Raffles[i]
		out[i] = &Raffle{
			Name:           m.Name,
			ParticipantIDs: append([]string(nil), m.ParticipantIDs...),
			ReactMessageID: m.ReactMessageID,
		}
	}
	return out, nil
}

// SetRaffle upserts a raffle by name.
func (r *Repo) SetRaffle(ctx context.Context, guildID string, raffle *Raffle) error {
	if raffle == nil {
		return nil
	}
	mongoRaffle := RaffleMongo{
		Name:           raffle.Name,
		ParticipantIDs: raffle.ParticipantIDs,
		ReactMessageID: raffle.ReactMessageID,
	}
	if mongoRaffle.ParticipantIDs == nil {
		mongoRaffle.ParticipantIDs = []string{}
	}
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

// RemoveFeed removes one feed matching channel, subreddit, post_type; optional author/title narrow match. Call three times for hot/rising/new when user omits post-type.
func (r *Repo) RemoveFeed(ctx context.Context, guildID, channelID, subreddit, postType, author, title string) (int64, error) {
	filter := bson.M{"_id": guildID}
	pull := bson.M{
		"subreddit":  subreddit,
		"channel_id": channelID,
		"post_type":  postType,
	}
	if author != "" {
		pull["author"] = author
	}
	if title != "" {
		pull["title"] = title
	}
	update := bson.M{"$pull": bson.M{"feeds": pull}}
	res, err := r.coll.UpdateOne(ctx, filter, update)
	if err != nil {
		return 0, fmt.Errorf("RemoveFeed: %w", err)
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

// RemoveFeedsBySubredditAndPostType removes all feeds for the subreddit and post type.
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

func (r *Repo) UpdateRaffleParticipant(ctx context.Context, guildID, raffleName, userID string, remove bool) error {
	filter := bson.M{"_id": guildID, "raffles.name": raffleName}
	var update bson.M
	if remove {
		update = bson.M{"$pull": bson.M{"raffles.$.participant_ids": userID}}
	} else {
		update = bson.M{"$addToSet": bson.M{"raffles.$.participant_ids": userID}}
	}
	_, err := r.coll.UpdateOne(ctx, filter, update)
	if err != nil {
		return fmt.Errorf("UpdateRaffleParticipant: %w", err)
	}
	return nil
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
