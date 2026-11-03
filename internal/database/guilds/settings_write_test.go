package guilds

import (
	"reflect"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// Settings writes touch one guild_settings key, so keys the struct no longer has (command_roles, mod_only,
// reacts_module) survive in stored documents (spec #37: no migration, old values kept harmlessly). Every
// settings write goes through settingsFieldUpdate, including the voice-role list and ClearBotLog.
func TestSettingsFieldUpdateSetsOnlyThatKey(t *testing.T) {
	got := settingsFieldUpdate("voice_chas", []VoiceCha{})
	want := bson.M{"$set": bson.M{"guild_settings.voice_chas": []VoiceCha{}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("update = %v, want %v", got, want)
	}
}

func TestVoiceChasFieldIsTheBSONKeyOfTheStructField(t *testing.T) {
	f, ok := reflect.TypeOf(GuildSettings{}).FieldByName("VoiceChas")
	if !ok {
		t.Fatal("GuildSettings has no VoiceChas")
	}
	if tag := f.Tag.Get("bson"); tag != voiceChasField+",omitempty" {
		t.Errorf("bson tag = %q, want %q", tag, voiceChasField+",omitempty")
	}
}
