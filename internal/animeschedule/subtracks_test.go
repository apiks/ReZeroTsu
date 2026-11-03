package animeschedule

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// selectDay lists "name:track:episode[members]" of the rows SelectRows returns for one 2026-11 day.
func selectDay(day int, set SubscriptionTracks) string {
	var out []string
	for _, r := range SelectRows(time.Date(2026, 11, day, 0, 0, 0, 0, time.UTC), set) {
		var ms []string
		for _, m := range r.Members {
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

func TestSubscriptionTracksStoredFormReadsBack(t *testing.T) {
	if got, ok := ParseSubscriptionTracks(""); !ok || got != SubscribeSub {
		t.Errorf("empty = %v %v, want Sub", got, ok)
	}
	for _, s := range []SubscriptionTracks{SubscribeSub, SubscribeDub, SubscribeRaw, SubscribeSubDub, SubscribeEvery} {
		if got, ok := ParseSubscriptionTracks(s.String()); !ok || got != s {
			t.Errorf("%v does not round-trip: %v %v", s, got, ok)
		}
	}
}
