package anime_subs

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type ShowSub struct {
	Show string `bson:"show"`
	// Route is the AnimeSchedule route of the show; optional (legacy documents lack it). Matching trusts it over
	// the title (ADR-0002).
	Route               string `bson:"route,omitempty"`
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

// Seed upserts an anime_subs document by id, stamping last_seed_date with the given seedDate (YYYY-MM-DD, UTC).
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

// SetShowRoute records route on the named show, only where it has none yet, in a single update statement.
func (r *Repo) SetShowRoute(ctx context.Context, id, showName, route string) error {
	update := bson.M{"$set": bson.M{"shows.$[s].route": route}}
	opts := options.UpdateOne().SetArrayFilters([]any{bson.M{
		"s.show":  showName,
		"s.route": bson.M{"$in": bson.A{nil, ""}},
	}})
	if _, err := r.coll.UpdateOne(ctx, bson.M{"id": id}, update, opts); err != nil {
		return fmt.Errorf("SetShowRoute: %w", err)
	}
	return nil
}

// AppendShows adds shows to an existing document without rewriting its other entries or its seed date.
// Entries whose show name already exists (case-insensitive) are the caller's responsibility to filter out.
func (r *Repo) AppendShows(ctx context.Context, id string, shows []*ShowSub) error {
	if len(shows) == 0 {
		return nil
	}
	_, err := r.coll.UpdateOne(ctx, bson.M{"id": id}, mongo.Pipeline{
		{{Key: "$set", Value: bson.M{"shows": appendedShowsExpr(shows)}}},
	})
	if err != nil {
		return fmt.Errorf("AppendShows: %w", err)
	}
	return nil
}

// appendedShowsExpr is an aggregation expression yielding the document's shows with the given shows appended.
// A stored shows of null (written by older versions) or any other non-array counts as empty, which a plain
// $push would reject. The shows array is wrapped in $literal so a name starting with "$" is never read as a
// field path.
func appendedShowsExpr(shows []*ShowSub) bson.M {
	return bson.M{"$concatArrays": bson.A{
		bson.M{"$cond": bson.A{bson.M{"$isArray": "$shows"}, "$shows", bson.A{}}},
		bson.M{"$literal": shows},
	}}
}

// updateOneFunc runs one UpdateOne against the collection; it is the seam between the add-show logic and Mongo.
type updateOneFunc func(ctx context.Context, filter, update any, upsert bool) (*mongo.UpdateResult, error)

// AddShowIfAbsent adds sub to the user's document in a single upsert statement unless a
// subscription for the same show is already present, by the rule of ShowSub.Matches. It reports whether the show
// was added.
func (r *Repo) AddShowIfAbsent(ctx context.Context, id string, sub *ShowSub) (bool, error) {
	return addShowIfAbsent(ctx, func(ctx context.Context, filter, update any, upsert bool) (*mongo.UpdateResult, error) {
		return r.coll.UpdateOne(ctx, filter, update, options.UpdateOne().SetUpsert(upsert))
	}, id, sub)
}

func addShowIfAbsent(ctx context.Context, updateOne updateOneFunc, id string, sub *ShowSub) (bool, error) {
	// The rule of ShowSub.Matches (ADR-0002): an existing subscription with a route is the same show only on the
	// same route; one without a route is the same show on the same title.
	var same bson.A
	if sub.Route != "" {
		same = append(same, bson.M{"route": sub.Route})
	}
	same = append(same, bson.M{
		"route": bson.M{"$in": bson.A{nil, ""}},
		"show":  bson.Regex{Pattern: "^" + regexp.QuoteMeta(sub.Show) + "$", Options: "i"},
	})
	filter := bson.M{
		"id":    id,
		"shows": bson.M{"$not": bson.M{"$elemMatch": bson.M{"$or": same}}},
	}
	// A pipeline update: it tolerates a stored shows of null and fills the defaults a $setOnInsert would.
	// No "id" entry: a matched document already has it, and an upsert takes it from the equality filter.
	update := mongo.Pipeline{{{Key: "$set", Value: bson.M{
		"is_guild":       bson.M{"$ifNull": bson.A{"$is_guild", false}},
		"last_seed_date": bson.M{"$ifNull": bson.A{"$last_seed_date", ""}},
		"shows":          appendedShowsExpr([]*ShowSub{sub}),
	}}}}
	res, err := updateOne(ctx, filter, update, true)
	if err == nil {
		return res.ModifiedCount > 0 || res.UpsertedCount > 0, nil
	}
	if !mongo.IsDuplicateKeyError(err) {
		return false, fmt.Errorf("AddShowIfAbsent: %w", err)
	}
	// The filter matched no document, so the upsert tried to insert a duplicate id. Either the document exists
	// and already holds the show, or a concurrent first-time call created it just before us. Retry once without
	// the upsert: it adds the show in the second case and matches nothing in the first.
	res, err = updateOne(ctx, filter, update, false)
	if err != nil {
		return false, fmt.Errorf("AddShowIfAbsent: %w", err)
	}
	return res.MatchedCount > 0, nil
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

// CollapseDuplicateShows removes null (and other non-document) entries and entries without a non-blank string
// show, and keeps one entry per CollapseKey (see collapsedShowsExpr for which), in one pipeline update. It
// reports whether the document was modified. Being a single statement, it cannot lose a concurrent /sub or
// notification write.
func (r *Repo) CollapseDuplicateShows(ctx context.Context, id string) (bool, error) {
	res, err := r.coll.UpdateOne(ctx, bson.M{"id": id}, mongo.Pipeline{
		{{Key: "$set", Value: bson.M{"shows": collapsedShowsExpr()}}},
	})
	if err != nil {
		return false, fmt.Errorf("CollapseDuplicateShows: %w", err)
	}
	return res.ModifiedCount > 0, nil
}

// mongoTrimSet is the default character set of the $trim aggregation operator (MongoDB documentation).
const mongoTrimSet = "\x00 \t\n\v\f\r             "

// CollapseKey is the duplicate key of a show name exactly as collapsedShowsExpr computes it in Mongo: trimmed of
// $trim's default whitespace set, then lowercased ASCII-only like $toLower. An empty key marks an entry the
// collapse removes. Go code deciding whether a document needs a collapse must use this key, or it may flag a
// document Mongo leaves unchanged (forever) or miss one Mongo would change.
func CollapseKey(name string) string {
	s := strings.Trim(name, mongoTrimSet)
	b := []byte(s)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}

// MatchKey is the case-insensitive key that matches a subscribed show name to a schedule show name: trimmed like
// CollapseKey, but lowercased Unicode-aware. Names with the same CollapseKey always have the same MatchKey, so a
// collapse never merges entries the notifier tells apart.
func MatchKey(name string) string {
	return strings.ToLower(strings.Trim(name, mongoTrimSet))
}

// Matches reports whether the show with this route and title is the one s subscribes to (ADR-0002). A subscription
// with a route matches by route only; one without matches by title under MatchKey.
func (s *ShowSub) Matches(route, title string) bool {
	if s.Route != "" {
		return s.Route == route
	}
	return MatchKey(s.Show) == MatchKey(title)
}

// SupersedesDuplicate reports whether candidate, appearing after current in a document's shows, is the entry to
// keep for both: the one with the greater last-notified air time, ties going to the later entry. It is the rule
// collapsedShowsExpr applies, so the entry the notifier reads and updates is the one a collapse keeps.
func SupersedesDuplicate(current, candidate *ShowSub) bool {
	return candidate.LastNotifiedAirUnix >= current.LastNotifiedAirUnix
}

// collapsedShowsExpr is an aggregation expression yielding the document's shows without non-document entries,
// without entries whose show is missing, not a string or blank (CollapseKey ""), and with one entry per
// CollapseKey: the one SupersedesDuplicate picks (greatest last_notified_air_unix, missing counting as 0, ties
// going to the later entry), at the position of the group's first entry. A stored shows that is not an array
// counts as empty.
func collapsedShowsExpr() bson.M {
	// k is the entry's CollapseKey: $trim (default set) then the ASCII-only $toLower, "" for a non-string show.
	key := bson.M{"$cond": bson.A{
		bson.M{"$eq": bson.A{bson.M{"$type": "$$this.show"}, "string"}},
		bson.M{"$toLower": bson.M{"$trim": bson.M{"input": "$$this.show"}}},
		"",
	}}
	airUnix := func(entry string) bson.M {
		return bson.M{"$ifNull": bson.A{entry + ".last_notified_air_unix", 0}}
	}
	appendNew := bson.M{
		"keys": bson.M{"$concatArrays": bson.A{"$$value.keys", bson.A{"$$k"}}},
		"out":  bson.M{"$concatArrays": bson.A{"$$value.out", bson.A{"$$this"}}},
	}
	// replaceAtI puts this entry in place of the kept duplicate at index i.
	replaceAtI := bson.M{
		"keys": "$$value.keys",
		"out": bson.M{"$map": bson.M{
			"input": bson.M{"$range": bson.A{0, bson.M{"$size": "$$value.out"}}},
			"as":    "j",
			"in": bson.M{"$cond": bson.A{
				bson.M{"$eq": bson.A{"$$j", "$$i"}}, "$$this", bson.M{"$arrayElemAt": bson.A{"$$value.out", "$$j"}},
			}},
		}},
	}
	duplicate := bson.M{"$let": bson.M{
		"vars": bson.M{"kept": bson.M{"$arrayElemAt": bson.A{"$$value.out", "$$i"}}},
		"in": bson.M{"$cond": bson.A{
			bson.M{"$gte": bson.A{airUnix("$$this"), airUnix("$$kept")}}, replaceAtI, "$$value",
		}},
	}}
	step := bson.M{"$let": bson.M{
		"vars": bson.M{"k": key},
		"in": bson.M{"$cond": bson.A{
			bson.M{"$or": bson.A{
				bson.M{"$ne": bson.A{bson.M{"$type": "$$this"}, "object"}},
				bson.M{"$eq": bson.A{"$$k", ""}},
			}},
			"$$value",
			bson.M{"$let": bson.M{
				"vars": bson.M{"i": bson.M{"$indexOfArray": bson.A{"$$value.keys", "$$k"}}},
				"in":   bson.M{"$cond": bson.A{bson.M{"$eq": bson.A{"$$i", -1}}, appendNew, duplicate}},
			}},
		}},
	}}
	reduced := bson.M{"$reduce": bson.M{
		"input":        bson.M{"$cond": bson.A{bson.M{"$isArray": "$shows"}, "$shows", bson.A{}}},
		"initialValue": bson.M{"keys": bson.A{}, "out": bson.A{}},
		"in":           step,
	}}
	return bson.M{"$let": bson.M{"vars": bson.M{"r": reduced}, "in": "$$r.out"}}
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
		"s.show":                   showName,
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
