package bot

import (
	"testing"

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
		{"sub and dub kept by the sub track", "sub_dub",
			one(trackRow("A", "a", animeschedule.TrackDub, true)), one(trackRow("A", "a", animeschedule.TrackSub, false)), nil, nil, true},
		{"sub and dub kept by the dub track", "sub_dub",
			one(trackRow("A", "a", animeschedule.TrackSub, true)), nil, nil, nil, true},
		{"sub and dub removed when both ended", "sub_dub",
			one(trackRow("A", "a", animeschedule.TrackSub, true), trackRow("A", "a", animeschedule.TrackDub, true)), nil, nil, nil, false},
		{"raw kept while the show has no raw row", "raw",
			one(trackRow("A", "a", animeschedule.TrackSub, true)), nil, nil, nil, true},
		{"plain sub removed after the sub's final episode", "",
			one(trackRow("A", "a", animeschedule.TrackSub, true)), nil, nil, nil, false},
		{"plain sub is not kept by a dub row this week", "",
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
