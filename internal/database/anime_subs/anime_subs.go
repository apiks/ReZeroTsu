package anime_subs

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type ShowSub struct {
	Show                string `bson:"show"`
	Notified            bool   `bson:"notified"`
	Guild               bool   `bson:"guild"`
	LastNotifiedAirUnix int64  `bson:"last_notified_air_unix,omitempty"`
	// LastNotifiedEpisode is the show route plus episode label last DMed; optional (legacy documents lack it).
	LastNotifiedEpisode string `bson:"last_notified_episode,omitempty"`
}

// AnimeSubs is an anime_subs document; key field is id (not _id).
type AnimeSubs struct {
	ID           string     `bson:"id"`
	IsGuild      bool       `bson:"is_guild"`
	Shows        []*ShowSub `bson:"shows"`
	LastSeedDate string     `bson:"last_seed_date"`
}

type Repo struct {
	coll *mongo.Collection
}

func NewRepo(coll *mongo.Collection) *Repo {
	return &Repo{coll: coll}
}

// GetByID returns the document for id, or nil if not found.
func (r *Repo) GetByID(ctx context.Context, id string) (*AnimeSubs, error) {
	var doc AnimeSubs
	err := r.coll.FindOne(ctx, bson.M{"id": id}).Decode(&doc)
	if err == mongo.ErrNoDocuments {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("GetByID: %w", err)
	}
	return &doc, nil
}

// IterateUserSubs streams anime_subs documents where is_guild is false, calling fn for each document.
// Stops on first fn error or cursor error. Use for memory-efficient processing without loading all docs.
func (r *Repo) IterateUserSubs(ctx context.Context, fn func(*AnimeSubs) error) error {
	cursor, err := r.coll.Find(ctx, bson.M{"is_guild": false})
	if err != nil {
		return fmt.Errorf("IterateUserSubs: %w", err)
	}
	defer cursor.Close(ctx)
	for cursor.Next(ctx) {
		var doc AnimeSubs
		if err := cursor.Decode(&doc); err != nil {
			return fmt.Errorf("IterateUserSubs: %w", err)
		}
		if err := fn(&doc); err != nil {
			return fmt.Errorf("IterateUserSubs: %w", err)
		}
	}
	if err := cursor.Err(); err != nil {
		return fmt.Errorf("IterateUserSubs: %w", err)
	}
	return nil
}

// Set upserts an anime_subs document by id. Pass isGuild and shows; id is set from the argument.
func (r *Repo) Set(ctx context.Context, id string, isGuild bool, shows []*ShowSub) error {
	return r.Seed(ctx, id, isGuild, shows, time.Now().UTC().Format("2006-01-02"))
}

// Seed upserts an anime_subs document by id like Set, stamping last_seed_date with the given
// seedDate (YYYY-MM-DD, UTC) instead of today's date.
func (r *Repo) Seed(ctx context.Context, id string, isGuild bool, shows []*ShowSub, today string) error {
	filter := bson.M{"id": id}
	update := bson.M{
		"$set": bson.M{
			"id":             id,
			"is_guild":       isGuild,
			"shows":          shows,
			"last_seed_date": today,
		},
	}
	_, err := r.coll.UpdateOne(ctx, filter, update, options.UpdateOne().SetUpsert(true))
	if err != nil {
		return fmt.Errorf("Set: %w", err)
	}
	return nil
}

// MarkShowsNotified sets notified=true on the named shows of the document in a single update statement.
func (r *Repo) MarkShowsNotified(ctx context.Context, id string, showNames []string) error {
	if len(showNames) == 0 {
		return nil
	}
	update := bson.M{"$set": bson.M{"shows.$[s].notified": true}}
	opts := options.UpdateOne().SetArrayFilters([]any{bson.M{"s.show": bson.M{"$in": showNames}}})
	if _, err := r.coll.UpdateOne(ctx, bson.M{"id": id}, update, opts); err != nil {
		return fmt.Errorf("MarkShowsNotified: %w", err)
	}
	return nil
}

// MarkShowNotified records one show of the document as notified, with its air time and episode key,
// in a single update statement. The show is matched by exact name.
func (r *Repo) MarkShowNotified(ctx context.Context, id, showName string, airUnix int64, episodeKey string) error {
	update := bson.M{"$set": bson.M{
		"shows.$[s].notified":               true,
		"shows.$[s].last_notified_air_unix": airUnix,
		"shows.$[s].last_notified_episode":  episodeKey,
	}}
	opts := options.UpdateOne().SetArrayFilters([]any{bson.M{"s.show": showName}})
	if _, err := r.coll.UpdateOne(ctx, bson.M{"id": id}, update, opts); err != nil {
		return fmt.Errorf("MarkShowNotified: %w", err)
	}
	return nil
}

// AppendShows adds shows to an existing document without rewriting its other entries or its seed date.
// Entries whose show name already exists (case-insensitive) are the caller's responsibility to filter out.
func (r *Repo) AppendShows(ctx context.Context, id string, shows []*ShowSub) error {
	if len(shows) == 0 {
		return nil
	}
	_, err := r.coll.UpdateOne(ctx, bson.M{"id": id}, bson.M{"$push": bson.M{"shows": bson.M{"$each": shows}}})
	if err != nil {
		return fmt.Errorf("AppendShows: %w", err)
	}
	return nil
}

// AddShowIfAbsent adds sub to the user's document in a single upsert statement unless a show with the same
// name (case-insensitive) is already present. It reports whether the show was added.
func (r *Repo) AddShowIfAbsent(ctx context.Context, id string, sub *ShowSub) (bool, error) {
	filter := bson.M{
		"id": id,
		"shows": bson.M{"$not": bson.M{"$elemMatch": bson.M{
			"show": bson.Regex{Pattern: "^" + regexp.QuoteMeta(sub.Show) + "$", Options: "i"},
		}}},
	}
	update := bson.M{
		"$push":        bson.M{"shows": sub},
		"$setOnInsert": bson.M{"id": id, "is_guild": false, "last_seed_date": ""},
	}
	res, err := r.coll.UpdateOne(ctx, filter, update, options.UpdateOne().SetUpsert(true))
	if err != nil {
		// The document exists and already holds the show: the upsert tried to insert a duplicate id.
		if mongo.IsDuplicateKeyError(err) {
			return false, nil
		}
		return false, fmt.Errorf("AddShowIfAbsent: %w", err)
	}
	return res.ModifiedCount > 0 || res.UpsertedCount > 0, nil
}

// RemoveShows removes the shows with exactly the given names from the document in a single update statement.
func (r *Repo) RemoveShows(ctx context.Context, id string, showNames []string) error {
	if len(showNames) == 0 {
		return nil
	}
	update := bson.M{"$pull": bson.M{"shows": bson.M{"show": bson.M{"$in": showNames}}}}
	if _, err := r.coll.UpdateOne(ctx, bson.M{"id": id}, update); err != nil {
		return fmt.Errorf("RemoveShows: %w", err)
	}
	return nil
}

// ClearShows empties the document's shows in a single update statement, leaving the document otherwise intact.
func (r *Repo) ClearShows(ctx context.Context, id string) error {
	if _, err := r.coll.UpdateOne(ctx, bson.M{"id": id}, bson.M{"$set": bson.M{"shows": []*ShowSub{}}}); err != nil {
		return fmt.Errorf("ClearShows: %w", err)
	}
	return nil
}

// SetShowAirUnix records airUnix as the last notified air time of the named show, only where none is recorded yet.
func (r *Repo) SetShowAirUnix(ctx context.Context, id, showName string, airUnix int64) error {
	update := bson.M{"$set": bson.M{"shows.$[s].last_notified_air_unix": airUnix}}
	opts := options.UpdateOne().SetArrayFilters([]any{bson.M{
		"s.show":                    showName,
		"s.last_notified_air_unix": bson.M{"$not": bson.M{"$gt": 0}},
	}})
	if _, err := r.coll.UpdateOne(ctx, bson.M{"id": id}, update, opts); err != nil {
		return fmt.Errorf("SetShowAirUnix: %w", err)
	}
	return nil
}

func (r *Repo) DeleteByID(ctx context.Context, id string) error {
	_, err := r.coll.DeleteOne(ctx, bson.M{"id": id})
	if err != nil {
		return fmt.Errorf("DeleteByID: %w", err)
	}
	return nil
}

func (r *Repo) EnsureIndexes(ctx context.Context) error {
	indexModels := []mongo.IndexModel{
		{Keys: bson.M{"id": 1}, Options: options.Index().SetUnique(true)},
		{Keys: bson.M{"is_guild": 1}, Options: options.Index()},
	}
	_, err := r.coll.Indexes().CreateMany(ctx, indexModels)
	if err != nil {
		return fmt.Errorf("EnsureIndexes: %w", err)
	}
	return nil
}
