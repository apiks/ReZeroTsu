package feed_checks

import (
	"reflect"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// The BulkWrite itself is covered by manual acceptance; this pins one upsert per check keyed by guild and GUID.
func TestSaveManyModels(t *testing.T) {
	fcs := []FeedCheck{{GUID: "a", GuildID: "other"}, {GUID: "b"}}
	models := saveManyModels("g1", fcs)
	if len(models) != 2 {
		t.Fatalf("models = %d, want 2", len(models))
	}
	for i, m := range models {
		um, ok := m.(*mongo.UpdateOneModel)
		if !ok {
			t.Fatalf("model %d is %T, want *mongo.UpdateOneModel", i, m)
		}
		if um.Upsert == nil || !*um.Upsert {
			t.Errorf("model %d is not an upsert", i)
		}
		wantFilter := bson.M{"guild_id": "g1", "guid": fcs[i].GUID}
		if !reflect.DeepEqual(um.Filter, wantFilter) {
			t.Errorf("model %d filter = %v, want %v", i, um.Filter, wantFilter)
		}
		set := um.Update.(bson.M)["$set"].(FeedCheck)
		if set.GuildID != "g1" || set.GUID != fcs[i].GUID {
			t.Errorf("model %d $set = %+v, want guild g1 and guid %q", i, set, fcs[i].GUID)
		}
	}
}
