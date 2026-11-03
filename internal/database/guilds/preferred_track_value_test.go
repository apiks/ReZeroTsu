package guilds

import (
	"reflect"
	"testing"

	"ReZeroTsu/internal/animeschedule"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// SetPreferredTrack needs a real Mongo, so this tests the update it sends: the lower-case value
// ParsePreferredTrack reads back, never the title shown to members.
func TestPreferredTrackIsStoredAsItsLowerCaseValue(t *testing.T) {
	for pref, stored := range map[animeschedule.PreferredTrack]string{
		animeschedule.PreferSub: "sub", animeschedule.PreferDub: "dub", animeschedule.PreferRaw: "raw",
	} {
		want := bson.M{"$set": bson.M{"guild_settings.preferred_track": stored}}
		if got := settingsFieldUpdate(preferredTrackField, preferredTrackValue(pref)); !reflect.DeepEqual(got, want) {
			t.Errorf("update for %v = %v, want %v", pref, got, want)
		}
		if back, ok := animeschedule.ParsePreferredTrack(stored); !ok || back != pref {
			t.Errorf("stored %q does not read back as %v", stored, pref)
		}
	}
}
