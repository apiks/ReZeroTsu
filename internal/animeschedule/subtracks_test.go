package animeschedule

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// selectDay lists "name:track:episode[tracks]" of the rows SelectRows returns for one 2026-11 day.
func selectDay(day int, set SubscriptionTracks) string {
	var out []string
	for _, r := range SelectRows(time.Date(2026, 11, day, 0, 0, 0, 0, time.UTC), set) {
		var ms []string
		for _, m := range r.Tracks {
			ms = append(ms, string(m))
		}
		out = append(out, fmt.Sprintf("%s:%s:%s[%s]", r.Name, r.Track, r.Episode, strings.Join(ms, "+")))
	}
	return strings.Join(out, ",")
}

func TestSelectRowsPicksEachSetsRowsOnTheFrierenWeek(t *testing.T) {
	frierenWeek(t)
	for _, tc := range []struct {
		set          SubscriptionTracks
		mon, fri, f2 string
	}{
		{SubscribeSub, "", "Frieren:sub:Ep 5[sub]", "Frieren:sub:Ep 6[sub]"},
		{SubscribeDub, "Frieren:dub:Ep 2[dub]", "", ""},
		{SubscribeRaw, "", "Frieren:raw:Ep 5[raw]", "Frieren:raw:Ep 6[raw]"},
		{SubscribeSubDub, "Frieren:dub:Ep 2[dub]", "Frieren:sub:Ep 5[sub]", "Frieren:sub:Ep 6[sub]"},
		{SubscribeEvery, "Frieren:dub:Ep 2[dub]", "Frieren:raw:Ep 5[raw],Frieren:sub:Ep 5[sub]", "Frieren:raw:Ep 6[raw],Frieren:sub:Ep 6[sub]"},
	} {
		if got := selectDay(2, tc.set); got != tc.mon {
			t.Errorf("%v Monday = %q, want %q", tc.set, got, tc.mon)
		}
		if got := selectDay(6, tc.set); got != tc.fri {
			t.Errorf("%v Friday = %q, want %q", tc.set, got, tc.fri)
		}
		if got := selectDay(13, tc.set); got != tc.f2 {
			t.Errorf("%v next Friday = %q, want %q", tc.set, got, tc.f2)
		}
	}
}

func TestSelectRowsSubFallsBackToRawPerWeekAndEveryTrackReturnsARawOnlyRowOnce(t *testing.T) {
	t.Cleanup(Seed(map[string][]ShowEntry{
		"2026-11-03": {row("Rawonly", "rawonly", TrackRaw, "Ep 1", 1), row("Mixed", "mixed", TrackRaw, "Ep 1", 2)},
		"2026-11-10": {row("Mixed", "mixed", TrackRaw, "Ep 2", 3), row("Mixed", "mixed", TrackSub, "Ep 2", 4)},
	}))
	if got, want := selectDay(3, SubscribeSub), "Rawonly:raw:Ep 1[sub],Mixed:raw:Ep 1[sub]"; got != want {
		t.Errorf("Sub in a week without a sub = %q, want %q", got, want)
	}
	if got, want := selectDay(10, SubscribeSub), "Mixed:sub:Ep 2[sub]"; got != want {
		t.Errorf("Sub in a week with a sub = %q, want %q", got, want)
	}
	if got, want := selectDay(3, SubscribeEvery), "Rawonly:raw:Ep 1[sub+raw],Mixed:raw:Ep 1[sub+raw]"; got != want {
		t.Errorf("Every track on raw-only rows = %q, want %q", got, want)
	}
	if got := selectDay(3, SubscribeDub); got != "" {
		t.Errorf("Dub on raw-only rows = %q, want none", got)
	}
}

// A show without a route is told apart by its title's match key, so the Sub chain still picks its sub row over its
// raw row, and falls back to raw only in a week it has no sub row.
func TestSelectRowsHandlesShowsWithoutARoute(t *testing.T) {
	t.Cleanup(Seed(map[string][]ShowEntry{
		"2026-11-03": {row("Routeless", "", TrackRaw, "Ep 1", 1), row("Other", "", TrackRaw, "Ep 1", 2)},
		"2026-11-04": {row("routeless ", "", TrackSub, "Ep 1", 3)},
		"2026-11-10": {row("Routeless", "", TrackRaw, "Ep 2", 4)},
	}))
	if got, want := selectDay(3, SubscribeSub), "Other:raw:Ep 1[sub]"; got != want {
		t.Errorf("Sub in a week the routeless show has a sub row = %q, want its raw row dropped: %q", got, want)
	}
	if got, want := selectDay(4, SubscribeSub), "routeless :sub:Ep 1[sub]"; got != want {
		t.Errorf("Sub on the sub row day = %q, want %q", got, want)
	}
	if got, want := selectDay(10, SubscribeSub), "Routeless:raw:Ep 2[sub]"; got != want {
		t.Errorf("Sub in a week without a sub row = %q, want the raw fallback %q", got, want)
	}
}

func TestSubscriptionTracksStoredFormReadsBack(t *testing.T) {
	if got, ok := ParseSubscriptionTracks(""); !ok || got != SubscribeSub {
		t.Errorf("empty = %v %v, want Sub", got, ok)
	}
	for _, s := range []SubscriptionTracks{SubscribeSub, SubscribeDub, SubscribeRaw, SubscribeSubDub, SubscribeEvery} {
		if got, ok := ParseSubscriptionTracks(s.String()); !ok || got != s {
			t.Errorf("%v does not round-trip: %v %v", s, got, ok)
		}
		if got, ok := ParseSubscriptionTracks(s.Stored()); !ok || got != s {
			t.Errorf("%v stored as %q does not read back: %v %v", s, s.Stored(), got, ok)
		}
	}
}

func TestSubscriptionTracksStoreSubAsEmptyAndTheRestByName(t *testing.T) {
	for set, want := range map[SubscriptionTracks]string{SubscribeSub: "", SubscribeDub: "dub", SubscribeRaw: "raw", SubscribeSubDub: "sub_dub", SubscribeEvery: "every"} {
		if got := set.Stored(); got != want {
			t.Errorf("%v stored = %q, want %q", set, got, want)
		}
	}
}
