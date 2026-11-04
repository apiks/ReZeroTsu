package guilds

import (
	"reflect"
	"testing"

	"ReZeroTsu/internal/animeschedule"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestMissingPreferredNameReadsAsTitle(t *testing.T) {
	for stored, want := range map[string]animeschedule.PreferredName{
		"": animeschedule.PreferTitle, "title": animeschedule.PreferTitle, "romaji": animeschedule.PreferRomaji,
		"english": animeschedule.PreferEnglish, "native": animeschedule.PreferNative, "junk": animeschedule.PreferTitle,
	} {
		if got := (&GuildSettings{PreferredName: stored}).PreferredShowName(); got != want {
			t.Errorf("stored %q reads as %v, want %v", stored, got, want)
		}
	}
	if got := DefaultGuildSettings().PreferredShowName(); got != animeschedule.PreferTitle {
		t.Errorf("default = %v, want Title", got)
	}
}

// A server's show settings are its donghua setting, preferred track and preferred name; a server with no settings
// reads as the default.
func TestShowSettingsBundleTheServersSettingsAndDefaultWithoutThem(t *testing.T) {
	var none *GuildSettings
	if got, want := none.ShowSettings(), animeschedule.DefaultShowSettings(); got != want {
		t.Errorf("no settings = %+v, want %+v", got, want)
	}
	got := (&GuildSettings{Donghua: false, PreferredTrack: "dub", PreferredName: "native"}).ShowSettings()
	want := animeschedule.ShowSettings{Donghua: false, Track: animeschedule.PreferDub, Name: animeschedule.PreferNative}
	if got != want {
		t.Errorf("settings = %+v, want %+v", got, want)
	}
}

// SetPreferredName needs a real Mongo, so this tests the update it sends: only guild_settings.preferred_name, holding
// the lower-case value ParsePreferredName reads back, never the title shown to members.
func TestPreferredNameWritesOnlyItsOwnSettingsKeyAsItsLowerCaseValue(t *testing.T) {
	f, ok := reflect.TypeOf(GuildSettings{}).FieldByName("PreferredName")
	if !ok {
		t.Fatal("GuildSettings has no PreferredName")
	}
	if tag := f.Tag.Get("bson"); tag != preferredNameField+",omitempty" {
		t.Errorf("bson tag = %q, want %q", tag, preferredNameField+",omitempty")
	}
	for name, stored := range map[animeschedule.PreferredName]string{
		animeschedule.PreferTitle: "title", animeschedule.PreferRomaji: "romaji",
		animeschedule.PreferEnglish: "english", animeschedule.PreferNative: "native",
	} {
		want := bson.M{"$set": bson.M{"guild_settings.preferred_name": stored}}
		if got := settingsFieldUpdate(preferredNameField, preferredNameValue(name)); !reflect.DeepEqual(got, want) {
			t.Errorf("update for %v = %v, want %v", name, got, want)
		}
		if back, ok := animeschedule.ParsePreferredName(stored); !ok || back != name {
			t.Errorf("stored %q does not read back as %v", stored, name)
		}
	}
}
