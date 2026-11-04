package animeschedule

import (
	"testing"

	"ReZeroTsu/internal/database/anime_subs"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// A member's stored document is read back through its bson tags; one stored before preferred names, or by a member
// who never chose, reads as Title.
func TestAMembersStoredPreferredNameReadsBackAndAMissingOneReadsAsTitle(t *testing.T) {
	decode := func(t *testing.T, doc bson.M) *anime_subs.AnimeSubs {
		t.Helper()
		raw, err := bson.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		var out anime_subs.AnimeSubs
		if err := bson.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
		return &out
	}
	if got := MemberPreferredName(decode(t, bson.M{"id": "u", "is_guild": false, "shows": bson.A{}})); got != PreferTitle {
		t.Errorf("document without a preferred name reads as %v, want Title", got)
	}
	if got := MemberPreferredName(nil); got != PreferTitle {
		t.Errorf("no document reads as %v, want Title", got)
	}
	for stored, want := range map[string]PreferredName{
		"title": PreferTitle, "romaji": PreferRomaji, "english": PreferEnglish, "native": PreferNative, "junk": PreferTitle,
	} {
		if got := MemberPreferredName(decode(t, bson.M{"id": "u", "preferred_name": stored})); got != want {
			t.Errorf("stored %q reads as %v, want %v", stored, got, want)
		}
	}
}
