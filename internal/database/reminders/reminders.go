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
	// FailingSince is when Discord first rejected delivering this reminder; zero while it has not failed.
	FailingSince time.Time `bson:"failing_since,omitempty"`
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
// Uses the index on reminders.date. Callers remove the reminders they delivered with RemoveByRefs.
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

// Append atomically adds rm to the user's document, creating it when missing (is_guild is only set on creation).
// It never rewrites other reminders, so concurrent removals and appends are not lost. Two simultaneous
// appends by the same user may compute the same RemindID; that rare collision is accepted.
func (r *Repo) Append(ctx context.Context, id string, isGuild bool, rm RemindMe) error {
	update := bson.M{
		"$push":        bson.M{"reminders": rm},
		"$setOnInsert": bson.M{"is_guild": isGuild, "premium": false},
	}
	if _, err := r.coll.UpdateOne(ctx, bson.M{"id": id}, update, options.UpdateOne().SetUpsert(true)); err != nil {
		return fmt.Errorf("Append: %w", err)
	}
	return nil
}

// RemoveByRemindIDs atomically pulls the reminders with the given remind_ids from the user's document, then
// deletes the document if it has no reminders left. Reminders not named are left untouched.
func (r *Repo) RemoveByRemindIDs(ctx context.Context, id string, remindIDs []int) error {
	if len(remindIDs) == 0 {
		return nil
	}
	return r.pullThenDeleteIfEmpty(ctx, "RemoveByRemindIDs", id, bson.M{"remind_id": bson.M{"$in": remindIDs}})
}

// pullThenDeleteIfEmpty pulls the reminders matching cond from the user's document in one update, then deletes
// the document if it has no reminders left. op prefixes returned errors.
func (r *Repo) pullThenDeleteIfEmpty(ctx context.Context, op, id string, cond bson.M) error {
	if _, err := r.coll.UpdateOne(ctx, bson.M{"id": id}, bson.M{"$pull": bson.M{"reminders": cond}}); err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	if _, err := r.coll.DeleteOne(ctx, bson.M{"id": id, "reminders": bson.M{"$size": 0}}); err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	return nil
}

// RemindRef identifies one reminder by its ID and due time. remind_id alone is not enough: IDs are max+1 and are
// reused after a deletion, so the due time tells the listed reminder apart from a newer one that took its ID.
type RemindRef struct {
	RemindID int
	Date     time.Time
}

// pullRefsCondition is the $pull condition matching only reminders whose remind_id and date both equal a ref's.
func pullRefsCondition(refs []RemindRef) bson.M {
	conds := make([]bson.M, 0, len(refs))
	for _, ref := range refs {
		conds = append(conds, bson.M{"remind_id": ref.RemindID, "date": ref.Date})
	}
	return bson.M{"$or": conds}
}

// RemoveByRefs atomically pulls the reminders matching both remind_id and date of the given refs from the user's
// document, then deletes the document if it has no reminders left. A newer reminder that re-used a removed ID
// (different date) and all other reminders are left untouched.
func (r *Repo) RemoveByRefs(ctx context.Context, id string, refs []RemindRef) error {
	if len(refs) == 0 {
		return nil
	}
	return r.pullThenDeleteIfEmpty(ctx, "RemoveByRefs", id, pullRefsCondition(refs))
}

// markFailingFilter is the array filter (identifier r) matching reminders whose remind_id and date both equal a
// ref's and whose failing_since is unset. A null equality matches both a missing and a null field.
func markFailingFilter(refs []RemindRef) bson.M {
	conds := make([]bson.M, 0, len(refs))
	for _, ref := range refs {
		conds = append(conds, bson.M{"r.remind_id": ref.RemindID, "r.date": ref.Date})
	}
	return bson.M{"$or": conds, "r.failing_since": nil}
}

// MarkFailingSince sets failing_since to since, in one update, on each reminder matching both remind_id and date
// of a ref, unless it is already set: the first failure's time is kept across later failures.
func (r *Repo) MarkFailingSince(ctx context.Context, id string, refs []RemindRef, since time.Time) error {
	if len(refs) == 0 {
		return nil
	}
	update := bson.M{"$set": bson.M{"reminders.$[r].failing_since": since}}
	opts := options.UpdateOne().SetArrayFilters([]any{markFailingFilter(refs)})
	if _, err := r.coll.UpdateOne(ctx, bson.M{"id": id}, update, opts); err != nil {
		return fmt.Errorf("MarkFailingSince: %w", err)
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
