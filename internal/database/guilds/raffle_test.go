package guilds

import (
	"reflect"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestRaffleRecordWithoutButtonFieldsLoads(t *testing.T) {
	old, err := bson.Marshal(bson.M{"name": "Old", "participant_ids": []string{"a", "b"}, "react_message_id": "m1"})
	if err != nil {
		t.Fatal(err)
	}
	var m RaffleMongo
	if err := bson.Unmarshal(old, &m); err != nil {
		t.Fatal(err)
	}
	got := raffleFromMongo(&m)
	want := &Raffle{Name: "Old", ParticipantIDs: []string{"a", "b"}, ReactMessageID: "m1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("loaded %+v, want %+v", got, want)
	}
}

func TestRaffleButtonFieldsRoundTrip(t *testing.T) {
	r := &Raffle{Name: "New", ParticipantIDs: []string{"a"}, ChannelID: "c", MessageID: "m", WinnerID: "a"}
	data, err := bson.Marshal(raffleToMongo(r))
	if err != nil {
		t.Fatal(err)
	}
	var m RaffleMongo
	if err := bson.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	if got := raffleFromMongo(&m); !reflect.DeepEqual(got, r) {
		t.Errorf("round trip %+v, want %+v", got, r)
	}
}

func TestRaffleWithoutEntrantsStoresAnEmptyList(t *testing.T) {
	if m := raffleToMongo(&Raffle{Name: "x"}); m.ParticipantIDs == nil {
		t.Error("entrants must store as an empty list so the single-statement update has an array to add to")
	}
}
