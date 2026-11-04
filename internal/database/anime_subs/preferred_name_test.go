package anime_subs

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// bsonNames is the stored names of AnimeSubs: each field's bson tag, without its options.
func bsonNames() map[string]bool {
	names := map[string]bool{}
	typ := reflect.TypeOf(AnimeSubs{})
	for i := range typ.NumField() {
		name, _, _ := strings.Cut(typ.Field(i).Tag.Get("bson"), ",")
		names[name] = true
	}
	return names
}

// SetPreferredName needs a real Mongo, so this tests the update it sends. It sets only preferred_name on an existing
// document, and a document it creates is a member's with no subscriptions.
func TestPreferredNameUpdateSetsOnlyItsOwnFieldAndCreatesAMembersDocument(t *testing.T) {
	got := preferredNameUpdate("english")
	want := bson.M{
		"$set": bson.M{"preferred_name": "english"},
		// A member with no document gets one holding the setting and no subscriptions.
		"$setOnInsert": bson.M{"is_guild": false, "shows": bson.A{}, "last_seed_date": ""},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("update = %v\nwant %v", got, want)
	}
}

// Every key the preferred-name write names, in its filter and under each operator of its update, is the bson tag of
// an AnimeSubs field, so a typo in a stored name fails here.
func TestPreferredNameWriteNamesOnlyStoredFields(t *testing.T) {
	stored := bsonNames()
	var capture struct{ filter, update any }
	if err := setPreferredName(context.Background(), func(_ context.Context, filter, update any, _ bool) (*mongo.UpdateResult, error) {
		capture.filter, capture.update = filter, update
		return &mongo.UpdateResult{}, nil
	}, "u1", "english"); err != nil {
		t.Fatal(err)
	}
	filter, _ := capture.filter.(bson.M)
	update, _ := capture.update.(bson.M)
	if len(filter) == 0 || len(update) == 0 {
		t.Fatalf("filter %v and update %v, want both written", capture.filter, capture.update)
	}
	for key := range filter {
		if !stored[key] {
			t.Errorf("filter key %q is no AnimeSubs bson tag", key)
		}
	}
	for op, fields := range update {
		for key := range fields.(bson.M) {
			if !stored[key] {
				t.Errorf("%s key %q is no AnimeSubs bson tag", op, key)
			}
		}
	}
}

// Setting a preferred name upserts the member's document by its id, so a member with none gets one.
func TestSetPreferredNameUpsertsTheMembersDocument(t *testing.T) {
	type call struct {
		filter, update any
		upsert         bool
	}
	var calls []call
	if err := setPreferredName(context.Background(), func(_ context.Context, filter, update any, upsert bool) (*mongo.UpdateResult, error) {
		calls = append(calls, call{filter, update, upsert})
		return &mongo.UpdateResult{UpsertedCount: 1}, nil
	}, "u1", "native"); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 {
		t.Fatalf("calls = %d, want one update", len(calls))
	}
	c := calls[0]
	if !c.upsert {
		t.Error("the update does not upsert, so a member with no document keeps none")
	}
	if !reflect.DeepEqual(c.filter, bson.M{"id": "u1"}) {
		t.Errorf("filter = %v, want the member's id", c.filter)
	}
	if !reflect.DeepEqual(c.update, preferredNameUpdate("native")) {
		t.Errorf("update = %v, want %v", c.update, preferredNameUpdate("native"))
	}
}

// /subscription clear empties the shows and keeps the member's preferred name.
func TestClearShowsLeavesThePreferredName(t *testing.T) {
	got := clearShowsUpdate()
	set, ok := got["$set"].(bson.M)
	if len(got) != 1 || !ok || len(set) != 1 {
		t.Fatalf("update = %v, want one $set of shows", got)
	}
	if shows, ok := set["shows"].([]*ShowSub); !ok || len(shows) != 0 {
		t.Errorf("update = %v, want shows set to an empty array", got)
	}
	if _, ok := set[preferredNameField]; ok {
		t.Errorf("update %v touches the preferred name", got)
	}
}
