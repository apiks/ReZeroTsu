package feed_checks

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Feed is the embedded feed in a feed_check document.
type Feed struct {
	GuildID   string `bson:"guild_id,omitempty"`
	Subreddit string `bson:"subreddit,omitempty"`
	Title     string `bson:"title,omitempty"`
	Author    string `bson:"author,omitempty"`
	Pin       bool   `bson:"pin,omitempty"`
	PostType  string `bson:"post_type,omitempty"`
	ChannelID string `bson:"channel_id,omitempty"`
}

// FeedCheck is a feed_checks document. Compound key: (guild_id, guid).
type FeedCheck struct {
	GuildID string    `bson:"guild_id"`
	GUID    string    `bson:"guid"`
	Feed    Feed      `bson:"feed"`
	Date    time.Time `bson:"date"`
}

type Repo struct {
	coll *mongo.Collection
}

func NewRepo(coll *mongo.Collection) *Repo {
	return &Repo{coll: coll}
}

// Save upserts a feed check by (guild_id, guid).
func (r *Repo) Save(ctx context.Context, guildID string, fc FeedCheck) error {
	fc.GuildID = guildID
	filter := bson.M{"guild_id": guildID, "guid": fc.GUID}
	_, err := r.coll.UpdateOne(ctx, filter, bson.M{"$set": fc}, options.UpdateOne().SetUpsert(true))
	if err != nil {
		return fmt.Errorf("Save: %w", err)
	}
	return nil
}

// DeleteByChannelID deletes feed_checks for the guild and channel.
func (r *Repo) DeleteByChannelID(ctx context.Context, guildID, channelID string) (int64, error) {
	res, err := r.coll.DeleteMany(ctx, bson.M{"guild_id": guildID, "feed.channel_id": channelID})
	if err != nil {
		return 0, fmt.Errorf("DeleteByChannelID: %w", err)
	}
	return res.DeletedCount, nil
}

// DeleteBySubredditAndPostType deletes feed_checks for the guild, subreddit, and post type.
func (r *Repo) DeleteBySubredditAndPostType(ctx context.Context, guildID, subreddit, postType string) (int64, error) {
	res, err := r.coll.DeleteMany(ctx, bson.M{"guild_id": guildID, "feed.subreddit": subreddit, "feed.post_type": postType})
	if err != nil {
		return 0, fmt.Errorf("DeleteBySubredditAndPostType: %w", err)
	}
	return res.DeletedCount, nil
}

// DeleteByFeed deletes feed_checks matching the feed; optional author/title narrow the match.
func (r *Repo) DeleteByFeed(ctx context.Context, guildID, channelID, subreddit, postType, author, title string) (int64, error) {
	filter := bson.M{
		"guild_id":        guildID,
		"feed.channel_id": channelID,
		"feed.subreddit":  subreddit,
		"feed.post_type":  postType,
	}
	if author != "" {
		filter["feed.author"] = author
	}
	if title != "" {
		filter["feed.title"] = title
	}
	res, err := r.coll.DeleteMany(ctx, filter)
	if err != nil {
		return 0, fmt.Errorf("DeleteByFeed: %w", err)
	}
	return res.DeletedCount, nil
}

// ListGUIDsByGuildID returns only GUIDs for the guild, most recent first. limit <= 0 means default (e.g. 200).
// Used for memory-efficient dedupe: build a seen set from the returned slice without loading full FeedCheck docs.
func (r *Repo) ListGUIDsByGuildID(ctx context.Context, guildID string, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 200
	}
	opts := options.Find().SetSort(bson.M{"date": -1}).SetLimit(int64(limit)).SetProjection(bson.M{"guid": 1})
	cursor, err := r.coll.Find(ctx, bson.M{"guild_id": guildID}, opts)
	if err != nil {
		return nil, fmt.Errorf("ListGUIDsByGuildID: %w", err)
	}
	defer cursor.Close(ctx)
	var result []struct {
		GUID string `bson:"guid"`
	}
	if err := cursor.All(ctx, &result); err != nil {
		return nil, fmt.Errorf("ListGUIDsByGuildID: %w", err)
	}
	out := make([]string, len(result))
	for i := range result {
		out[i] = result[i].GUID
	}
	return out, nil
}

// DeleteOlderThan deletes feed_checks with date before the given time.
func (r *Repo) DeleteOlderThan(ctx context.Context, before time.Time) (int64, error) {
	res, err := r.coll.DeleteMany(ctx, bson.M{"date": bson.M{"$lt": before}})
	if err != nil {
		return 0, fmt.Errorf("DeleteOlderThan: %w", err)
	}
	return res.DeletedCount, nil
}

func (r *Repo) EnsureIndexes(ctx context.Context) error {
	indexModels := []mongo.IndexModel{
		{Keys: bson.D{{Key: "guild_id", Value: 1}, {Key: "guid", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "date", Value: -1}}, Options: options.Index()},
		{Keys: bson.D{{Key: "feed.subreddit", Value: 1}}, Options: options.Index()},
		{Keys: bson.D{{Key: "feed.channel_id", Value: 1}}, Options: options.Index()},
	}
	_, err := r.coll.Indexes().CreateMany(ctx, indexModels)
	if err != nil {
		return fmt.Errorf("EnsureIndexes: %w", err)
	}
	return nil
}
