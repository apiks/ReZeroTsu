package reminders

import (
	"reflect"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestMarkFailingFilter(t *testing.T) {
	d1 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	d2 := d1.Add(time.Hour)
	tests := []struct {
		name string
		refs []RemindRef
		want bson.M
	}{
		{
			name: "single ref, only reminders without failing_since",
			refs: []RemindRef{{RemindID: 2, Date: d1}},
			want: bson.M{
				"$or":             []bson.M{{"r.remind_id": 2, "r.date": d1}},
				"r.failing_since": nil,
			},
		},
		{
			name: "several refs are alternatives",
			refs: []RemindRef{{RemindID: 1, Date: d1}, {RemindID: 2, Date: d2}},
			want: bson.M{
				"$or":             []bson.M{{"r.remind_id": 1, "r.date": d1}, {"r.remind_id": 2, "r.date": d2}},
				"r.failing_since": nil,
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := markFailingFilter(tc.refs); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("filter = %v, want %v", got, tc.want)
			}
		})
	}
}
