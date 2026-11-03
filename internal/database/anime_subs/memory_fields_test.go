package anime_subs

import (
	"reflect"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// The names memoryFields writes in an update must be the names a stored document is read back under, or a mark
// is written to a field the notifier never reads. Marshalling the struct shows its bson tags without a Mongo.
func TestMemoryFieldNamesAreTheBsonTagsOfTheMemoryFields(t *testing.T) {
	for _, track := range []string{"sub", "dub", "raw"} {
		s := &ShowSub{Show: "Frieren"}
		s.Remember(Mark{Track: track, AirUnix: 77, Episode: "key"})
		raw, err := bson.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		var doc bson.M
		if err := bson.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		air, episode := memoryFields(track)
		if doc[air] != int64(77) || doc[episode] != "key" {
			t.Errorf("%s: document %v lacks %s=77 and %s=key", track, doc, air, episode)
		}
	}
}

func TestMarkShowNotifiedUpdateWritesEachMarkUnderItsOwnFields(t *testing.T) {
	got := markShowNotifiedUpdate([]Mark{
		{Track: "sub", AirUnix: 100, Episode: "frieren|Ep 1"},
		{Track: "dub", AirUnix: 200, Episode: "frieren|dub|Ep 1"},
		{Track: "raw", AirUnix: 300, Episode: "frieren|raw|Ep 1"},
	})
	want := bson.M{"$set": bson.M{
		"shows.$[s].notified":                   true,
		"shows.$[s].last_notified_air_unix":     int64(100),
		"shows.$[s].last_notified_episode":      "frieren|Ep 1",
		"shows.$[s].last_notified_dub_air_unix": int64(200),
		"shows.$[s].last_notified_dub_episode":  "frieren|dub|Ep 1",
		"shows.$[s].last_notified_raw_air_unix": int64(300),
		"shows.$[s].last_notified_raw_episode":  "frieren|raw|Ep 1",
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("update = %v\nwant %v", got, want)
	}
}

func TestSetShowTracksUpdateSetsNotifiedOnlyForAMarkWithAnAirTime(t *testing.T) {
	cleared := setShowTracksUpdate("dub", []Mark{{Track: "dub"}})
	wantCleared := bson.M{"$set": bson.M{
		"shows.$[s].tracks":                     "dub",
		"shows.$[s].last_notified_dub_air_unix": int64(0),
		"shows.$[s].last_notified_dub_episode":  "",
	}}
	if !reflect.DeepEqual(cleared, wantCleared) {
		t.Errorf("clearing update = %v\nwant %v", cleared, wantCleared)
	}
	seeded := setShowTracksUpdate("every", []Mark{{Track: "dub"}, {Track: "raw", AirUnix: 50}})
	wantSeeded := bson.M{"$set": bson.M{
		"shows.$[s].tracks":                     "every",
		"shows.$[s].notified":                   true,
		"shows.$[s].last_notified_dub_air_unix": int64(0),
		"shows.$[s].last_notified_dub_episode":  "",
		"shows.$[s].last_notified_raw_air_unix": int64(50),
		"shows.$[s].last_notified_raw_episode":  "",
	}}
	if !reflect.DeepEqual(seeded, wantSeeded) {
		t.Errorf("seeding update = %v\nwant %v", seeded, wantSeeded)
	}
}
