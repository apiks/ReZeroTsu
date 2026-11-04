package animeschedule

import (
	"encoding/json"
	"testing"
	"time"
)

func TestTimetableEntryMediaTypeFromRoute(t *testing.T) {
	tests := []struct {
		name  string
		field string
		want  MediaType
	}{
		{"tv", `[{"name":"TV","route":"tv"}]`, MediaTV},
		{"tv short", `[{"name":"TV Short","route":"tv-short"}]`, MediaTVShort},
		{"ona", `[{"name":"ONA","route":"ona"}]`, MediaONA},
		{"donghua", `[{"name":"ONA (Chinese)","route":"ona-chinese"}]`, MediaDonghua},
		{"ova", `[{"name":"OVA","route":"ova"}]`, MediaOVA},
		{"special", `[{"name":"Special","route":"special"}]`, MediaSpecial},
		{"movie", `[{"name":"Movie","route":"movie"}]`, MediaMovie},
		{"unknown route is TV", `[{"name":"Weird","route":"weird"}]`, MediaTV},
		{"empty list is TV", `[]`, MediaTV},
		{"missing is TV", ``, MediaTV},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"title":"S","route":"s","airType":"sub","episodeDate":"2025-01-06T12:00:00Z"`
			if tc.field != "" {
				body += `,"mediaTypes":` + tc.field
			}
			body += `}`
			var a TimetableAnime
			if err := json.Unmarshal([]byte(body), &a); err != nil {
				t.Fatal(err)
			}
			got := processTimetable([]TimetableAnime{a})["2025-01-06"]
			if len(got) != 1 || got[0].MediaType != tc.want {
				t.Fatalf("entries = %+v, want media type %v", got, tc.want)
			}
		})
	}
}

func TestDonghuaFlagIndependentOfMediaType(t *testing.T) {
	ts := time.Date(2025, 1, 6, 12, 0, 0, 0, time.UTC)
	a := TimetableAnime{Title: "S", Route: "s", AirType: "sub", EpisodeDate: ts, Donghua: true, // archcheck:allow the API's own field name
		MediaTypes: []MediaTypeRef{{Name: "TV", Route: "tv"}}}
	got := processTimetable([]TimetableAnime{a})["2025-01-06"]
	if len(got) != 1 || !got[0].Donghua || got[0].MediaType != MediaTV {
		t.Fatalf("entry = %+v", got)
	}
}
