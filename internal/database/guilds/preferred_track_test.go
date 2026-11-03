package guilds

import (
	"reflect"
	"testing"

	"ReZeroTsu/internal/animeschedule"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestMissingPreferredTrackReadsAsSub(t *testing.T) {
	for stored, want := range map[string]animeschedule.PreferredTrack{
		"": animeschedule.PreferSub, "sub": animeschedule.PreferSub, "dub": animeschedule.PreferDub,
		"raw": animeschedule.PreferRaw, "junk": animeschedule.PreferSub,
	} {
		if got := (&GuildSettings{PreferredTrack: stored}).Preferred(); got != want {
			t.Errorf("stored %q reads as %v, want %v", stored, got, want)
		}
	}
	if got := DefaultGuildSettings().Preferred(); got != animeschedule.PreferSub {
		t.Errorf("default = %v, want Sub", got)
	}
}

func TestPreferredTrackWritesOnlyItsOwnSettingsKey(t *testing.T) {
	f, ok := reflect.TypeOf(GuildSettings{}).FieldByName("PreferredTrack")
	if !ok {
		t.Fatal("GuildSettings has no PreferredTrack")
	}
	if tag := f.Tag.Get("bson"); tag != preferredTrackField+",omitempty" {
		t.Errorf("bson tag = %q, want %q", tag, preferredTrackField+",omitempty")
	}
	got := settingsFieldUpdate(preferredTrackField, "dub")
	want := bson.M{"$set": bson.M{"guild_settings.preferred_track": "dub"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("update = %v, want %v", got, want)
	}
}
