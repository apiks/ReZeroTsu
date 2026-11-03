package bot

import (
	"context"
	"testing"
	"time"

	"ReZeroTsu/internal/animeschedule"
	"ReZeroTsu/internal/database/anime_subs"
)

func trackRow(name, route string, track animeschedule.Track, final bool) animeschedule.ShowEntry {
	return animeschedule.ShowEntry{Name: name, Route: route, Track: track, Final: final, Episode: "Ep 1"}
}

func TestKeepRosterJudgesEachTrack(t *testing.T) {
	one := func(es ...animeschedule.ShowEntry) []animeschedule.ShowEntry { return es }
	tests := []struct {
		name             string
		tracks           string
		last, this, next []animeschedule.ShowEntry
		upcoming         []animeschedule.UpcomingShow
		want             bool
	}{
		{"dub kept while no dub row exists, sub run over and off the upcoming list", "dub",
			one(trackRow("A", "a", animeschedule.TrackSub, true)), nil, nil, nil, true},
		{"dub kept while its dub airs this week", "dub",
			nil, one(trackRow("A", "a", animeschedule.TrackSub, false), trackRow("A", "a", animeschedule.TrackDub, false)), nil, nil, true},
		{"dub kept on a break after a non-final dub row", "dub",
			one(trackRow("A", "a", animeschedule.TrackDub, false)), nil, nil, nil, true},
		{"dub removed after its final episode aired last week", "dub",
			one(trackRow("A", "a", animeschedule.TrackSub, false), trackRow("A", "a", animeschedule.TrackDub, true)), nil, nil, nil, false},
		{"dub removed when only the sub airs and a dub row exists elsewhere", "dub",
			one(trackRow("A", "a", animeschedule.TrackDub, true)), one(trackRow("A", "a", animeschedule.TrackSub, false)), nil, nil, false},
		{"dub kept while the show is upcoming", "dub",
			one(trackRow("A", "a", animeschedule.TrackDub, true)), nil, nil, []animeschedule.UpcomingShow{{Name: "A", Route: "a"}}, true},
		{"Sub and Dub subscription kept by the sub track", "sub_dub",
			one(trackRow("A", "a", animeschedule.TrackDub, true)), one(trackRow("A", "a", animeschedule.TrackSub, false)), nil, nil, true},
		{"Sub and Dub subscription kept by the dub track", "sub_dub",
			one(trackRow("A", "a", animeschedule.TrackSub, true)), nil, nil, nil, true},
		{"Sub and Dub subscription removed when both tracks ended", "sub_dub",
			one(trackRow("A", "a", animeschedule.TrackSub, true), trackRow("A", "a", animeschedule.TrackDub, true)), nil, nil, nil, false},
		{"raw kept while the show has no raw row", "raw",
			one(trackRow("A", "a", animeschedule.TrackSub, true)), nil, nil, nil, true},
		{"plain Sub subscription removed after the sub track's final episode", "",
			one(trackRow("A", "a", animeschedule.TrackSub, true)), nil, nil, nil, false},
		{"plain Sub subscription is not kept by a dub row this week", "",
			nil, one(trackRow("A", "a", animeschedule.TrackDub, false)), nil, nil, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r, ok := rosterFixture(t, true, tc.last, tc.this, tc.next, tc.upcoming)
			if !ok {
				t.Fatal("roster not ready")
			}
			sub := anime_subs.ShowSub{Show: "A", Route: "a", Tracks: tc.tracks}
			if got := r.keeps(&sub); got != tc.want {
				t.Errorf("keeps = %v, want %v", got, tc.want)
			}
		})
	}
}

// Until the upcoming list has loaded and this and next week are fetched, a Dub subscription must survive: a
// roster built from a partial load holds none of the strict rows that keep it.
func TestCleanupRemovesNothingWhileTheRosterIsNotReady(t *testing.T) {
	entry := []animeschedule.ShowEntry{{Name: "Filler", Route: "filler"}}
	tests := []struct {
		name           string
		upcomingLoaded bool
		this, next     []animeschedule.ShowEntry
	}{
		{"upcoming list not loaded", false, entry, entry},
		{"this week not fetched", true, nil, entry},
		{"next week not fetched", true, entry, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Cleanup(animeschedule.SetClock(func() time.Time { return time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC) }))
			monday := animeschedule.DateInWeek(time.Monday)
			t.Cleanup(animeschedule.Seed(map[string][]animeschedule.ShowEntry{
				animeschedule.DateKey(monday):                  tc.this,
				animeschedule.DateKey(monday.AddDate(0, 0, 7)): tc.next,
			}))
			t.Cleanup(animeschedule.SeedUpcoming(nil, tc.upcomingLoaded))
			st := newFakeUserSubs(&anime_subs.AnimeSubs{ID: "u", Shows: []*anime_subs.ShowSub{{Show: "A", Route: "a", Tracks: "dub"}}})
			updated, ready, err := cleanupWhenReady(context.Background(), st)
			if err != nil {
				t.Fatal(err)
			}
			if ready || updated != 0 || len(st.removed["u"]) != 0 {
				t.Errorf("ready = %v, updated = %d, removed = %v; want a skipped cleanup that removed nothing", ready, updated, st.removed["u"])
			}
		})
	}
}
