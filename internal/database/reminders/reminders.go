package reminders

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type RemindMe struct {
	Message        string    `bson:"message"`
	Date           time.Time `bson:"date"`
	CommandChannel string    `bson:"command_channel"`
	RemindID       int       `bson:"remind_id"`
	CreatedAt      time.Time `bson:"created_at,omitempty"`
}

// RemindMeSlice is a reminders document; key field is id (user or guild ID).
type RemindMeSlice struct {
	ID        string     `bson:"id"`
	IsGuild   bool       `bson:"is_guild"`
	Reminders []RemindMe `bson:"reminders"`
	Premium   bool       `bson:"premium"`
}

type Repo struct {
	coll *mongo.Collection
}

func NewRepo(coll *mongo.Collection) *Repo {
	return &Repo{coll: coll}
}

// ListDue returns all reminders documents that have at least one reminder with date <= asOf.
// Uses the index on reminders.date. Caller should split each doc into due vs remaining and Save remaining.
func (r *Repo) ListDue(ctx context.Context, asOf time.Time) ([]*RemindMeSlice, error) {
	cursor, err := r.coll.Find(ctx, bson.M{"reminders.date": bson.M{"$lte": asOf}})
	if err != nil {
		return nil, fmt.Errorf("ListDue: %w", err)
	}
	defer cursor.Close(ctx)
	var raw []RemindMeSlice
	if err := cursor.All(ctx, &raw); err != nil {
		return nil, fmt.Errorf("ListDue: %w", err)
	}
	out := make([]*RemindMeSlice, len(raw))
	for i := range raw {
		out[i] = &raw[i]
	}
	return out, nil
}

// GetByID returns the document for id, or nil if not found.
func (r *Repo) GetByID(ctx context.Context, id string) (*RemindMeSlice, error) {
	var doc RemindMeSlice
	err := r.coll.FindOne(ctx, bson.M{"id": id}).Decode(&doc)
	if err == mongo.ErrNoDocuments {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("GetByID: %w", err)
	}
	return &doc, nil
}

// Save upserts a reminders document by id. If slice is nil or has no reminders, the document is deleted.
func (r *Repo) Save(ctx context.Context, id string, slice *RemindMeSlice) error {
	if slice == nil || len(slice.Reminders) == 0 {
		_, err := r.coll.DeleteOne(ctx, bson.M{"id": id})
		if err != nil {
			return fmt.Errorf("Save: %w", err)
		}
		return nil
	}
	slice.ID = id
	filter := bson.M{"id": id}
	update := bson.M{"$set": slice}
	_, err := r.coll.UpdateOne(ctx, filter, update, options.UpdateOne().SetUpsert(true))
	if err != nil {
		return fmt.Errorf("Save: %w", err)
	}
	return nil
}

func (r *Repo) EnsureIndexes(ctx context.Context) error {
	indexModels := []mongo.IndexModel{
		{Keys: bson.M{"id": 1}, Options: options.Index().SetUnique(true)},
		{Keys: bson.M{"reminders.date": 1}, Options: options.Index()},
	}
	_, err := r.coll.Indexes().CreateMany(ctx, indexModels)
	if err != nil {
		return fmt.Errorf("EnsureIndexes: %w", err)
	}
	return nil
}
