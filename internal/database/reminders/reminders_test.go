package reminders

import (
	"reflect"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestPullRefsCondition(t *testing.T) {
	d1 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	d2 := d1.Add(time.Hour)
	tests := []struct {
		name string
		refs []RemindRef
		want bson.M
	}{
		{
			name: "single ref matches on id and date",
			refs: []RemindRef{{RemindID: 2, Date: d1}},
			want: bson.M{"$or": []bson.M{{"remind_id": 2, "date": d1}}},
		},
		{
			name: "several refs are alternatives, each pairing id with its own date",
			refs: []RemindRef{{RemindID: 1, Date: d1}, {RemindID: 2, Date: d2}},
			want: bson.M{"$or": []bson.M{{"remind_id": 1, "date": d1}, {"remind_id": 2, "date": d2}}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := pullRefsCondition(tc.refs); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("condition = %v, want %v", got, tc.want)
			}
		})
	}
}
