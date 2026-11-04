package anime_subs

import (
	"reflect"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// SetPreferredName needs a real Mongo, so this tests the update it sends. It sets only preferred_name on an existing
// document, and the name it writes is the bson tag a stored document is read back under.
func TestSetPreferredNameWritesOnlyItsOwnFieldAndCreatesAMembersDocument(t *testing.T) {
	f, ok := reflect.TypeOf(AnimeSubs{}).FieldByName("PreferredName")
	if !ok {
		t.Fatal("AnimeSubs has no PreferredName")
	}
	if tag := f.Tag.Get("bson"); tag != preferredNameField+",omitempty" {
		t.Errorf("bson tag = %q, want %q", tag, preferredNameField+",omitempty")
	}
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
