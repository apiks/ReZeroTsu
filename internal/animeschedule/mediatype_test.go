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
		{"donghua tv", `[{"name":"TV (Chinese)","route":"tv-chinese"}]`, MediaTV},
		{"donghua tv short", `[{"name":"TV Short (Chinese)","route":"tv-chinese-short"}]`, MediaTVShort},
		{"donghua ona", `[{"name":"ONA (Chinese)","route":"ona-chinese"}]`, MediaONA},
		{"donghua ova", `[{"name":"OVA (Chinese)","route":"ova-chinese"}]`, MediaOVA},
		{"donghua special", `[{"name":"Special (Chinese)","route":"special-chinese"}]`, MediaSpecial},
		{"donghua movie", `[{"name":"Movie (Chinese)","route":"movie-chinese"}]`, MediaMovie},
		{"first recognised route wins", `[{"name":"Weird","route":"weird"},{"name":"Movie (Chinese)","route":"movie-chinese"},{"name":"TV","route":"tv"}]`, MediaMovie},
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

func TestTypeLabelCarriesTheMediaTypeThenDonghua(t *testing.T) {
	tests := []struct {
		mt      MediaType
		donghua bool
		want    string
	}{
		{MediaTV, false, ""},
		{MediaTVShort, false, "TV Short"},
		{MediaONA, false, "ONA"},
		{MediaOVA, false, "OVA"},
		{MediaSpecial, false, "Special"},
		{MediaMovie, false, "Movie"},
		{MediaTV, true, "Donghua"},
		{MediaTVShort, true, "TV Short · Donghua"},
		{MediaONA, true, "ONA · Donghua"},
		{MediaOVA, true, "OVA · Donghua"},
		{MediaSpecial, true, "Special · Donghua"},
		{MediaMovie, true, "Movie · Donghua"},
	}
	for _, tc := range tests {
		e := ShowEntry{MediaType: tc.mt, Donghua: tc.donghua}
		if got := e.TypeLabel(); got != tc.want {
			t.Errorf("%+v label = %q, want %q", e, got, tc.want)
		}
	}
}

func TestDonghuaTimetableEntryIsItsBaseTypeAndDonghua(t *testing.T) {
	ts := time.Date(2025, 1, 6, 12, 0, 0, 0, time.UTC)
	a := TimetableAnime{Title: "S", Route: "s", AirType: "sub", EpisodeDate: ts, Donghua: true, // archcheck:allow the API's own field name
		MediaTypes: []MediaTypeRef{{Name: "Movie (Chinese)", Route: "movie-chinese"}}}
	got := processTimetable([]TimetableAnime{a})["2025-01-06"]
	if len(got) != 1 || got[0].MediaType != MediaMovie || !got[0].Donghua || got[0].TypeLabel() != "Movie · Donghua" {
		t.Fatalf("entry = %+v", got)
	}
}
