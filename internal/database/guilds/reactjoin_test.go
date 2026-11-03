package guilds

import (
	"reflect"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestReactJoinRecordWithoutAChannelIDLoads(t *testing.T) {
	old, err := bson.Marshal(bson.M{
		"channel_id": "m1", // the legacy field that holds the message ID
		"react_join": bson.M{"role_emoji": []bson.M{{"r1": []string{"🎉"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var w ReactJoinWrap
	if err := bson.Unmarshal(old, &w); err != nil {
		t.Fatal(err)
	}
	messageID, entry := reactJoinFromWrap(w)
	want := &ReactJoin{RoleEmojiMap: []map[string][]string{{"r1": {"🎉"}}}}
	if messageID != "m1" || !reflect.DeepEqual(entry, want) {
		t.Errorf("loaded %q %+v, want m1 %+v", messageID, entry, want)
	}
}

func TestReactJoinChannelIDRoundTrips(t *testing.T) {
	entry := &ReactJoin{ChannelID: "c1", RoleEmojiMap: []map[string][]string{{"r1": {"🎉", "party:123"}}}}
	data, err := bson.Marshal(reactJoinToWrap("m1", entry))
	if err != nil {
		t.Fatal(err)
	}
	var w ReactJoinWrap
	if err := bson.Unmarshal(data, &w); err != nil {
		t.Fatal(err)
	}
	messageID, got := reactJoinFromWrap(w)
	if messageID != "m1" || !reflect.DeepEqual(got, entry) {
		t.Errorf("round trip %q %+v, want m1 %+v", messageID, got, entry)
	}
}

func TestReactJoinWithoutAChannelIDWritesNoChannelField(t *testing.T) {
	data, err := bson.Marshal(reactJoinToWrap("m1", &ReactJoin{}))
	if err != nil {
		t.Fatal(err)
	}
	var raw bson.M
	if err := bson.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if _, has := raw["message_channel_id"]; has {
		t.Errorf("record %v carries message_channel_id although none is known", raw)
	}
}
