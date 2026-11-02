package anime_subs

import (
	"context"
	"fmt"
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
