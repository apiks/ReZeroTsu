package anime_subs

import (
	"reflect"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// An entry from before the tracks release has no track and no episode field; the filter must still select it for
// a row with neither, so marking a legacy entry notified keeps working.
func TestIdentityFilterMatchesEntriesMissingTheirTrackAndEpisode(t *testing.T) {
	missing := bson.M{"$in": bson.A{nil, ""}}
	got := identityFilter([]*ShowSub{{Show: "Foo"}, {Show: "Bar", Track: "dub", Episode: "Ep 2"}})
	want := bson.M{"$or": bson.A{
		bson.M{"$and": bson.A{bson.M{"s.show": "Foo"}, bson.M{"s.track": missing}, bson.M{"s.episode": missing}}},
		bson.M{"$and": bson.A{bson.M{"s.show": "Bar"}, bson.M{"s.track": "dub"}, bson.M{"s.episode": "Ep 2"}}},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("filter = %v, want %v", got, want)
	}
}
