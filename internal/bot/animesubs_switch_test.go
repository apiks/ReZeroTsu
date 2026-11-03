package bot

import (
	"context"
	"testing"
	"time"

	"ReZeroTsu/internal/animeschedule"
	"ReZeroTsu/internal/database/anime_subs"
)

func airingAt(name, ep string, track animeschedule.Track, offset int64) animeschedule.ShowEntry {
	row := trackedRow(name, ep, track)
	row.AirTimeUnix = trackedNow.Unix() + offset
	return row
}

// A server that changes its preferred track mid-day has no sent-state rows for the new chain; delivery must add
// them, recording the ones that already aired as posted and leaving the ones still to air due.
func TestChangingThePreferredTrackMidDayRecordsTheNewChainsRows(t *testing.T) {
	for _, tc := range []struct {
		name     string
		from, to animeschedule.Track
		pref     string
		want     string
	}{
		{"sub to dub", animeschedule.TrackSub, animeschedule.TrackDub, "dub", "**Ep 6** dubbed is out!"},
		{"dub to raw", animeschedule.TrackDub, animeschedule.TrackRaw, "raw", "**Ep 6** raw is out!"},
		{"raw to sub", animeschedule.TrackRaw, animeschedule.TrackSub, "sub", "**Ep 6** subbed is out!"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			chain := func(track animeschedule.Track) []animeschedule.ShowEntry {
				return []animeschedule.ShowEntry{airingAt("Frieren", "Ep 5", track, -600), airingAt("Frieren", "Ep 6", track, 3600)}
			}
			fn := byPreference(map[animeschedule.PreferredTrack][]animeschedule.ShowEntry{
				animeschedule.PreferSub: chain(animeschedule.TrackSub),
				animeschedule.PreferDub: chain(animeschedule.TrackDub),
				animeschedule.PreferRaw: chain(animeschedule.TrackRaw),
			})
			from := string(tc.from)
			st := trackedState(tc.pref,
				&anime_subs.ShowSub{Show: "Frieren", Track: from, Episode: "Ep 5", Guild: true, Notified: true},
				&anime_subs.ShowSub{Show: "Frieren", Track: from, Episode: "Ep 6", Guild: true})
			d := newFakeDiscord()
			processGuildNewEpisodes(context.Background(), d, newTestSenders(d), st, "g1", fn, trackedNow)
			if got := descriptions(d); len(got) != 0 {
				t.Fatalf("switch posted %q, want the earlier airing recorded and not reposted", got)
			}
			doc, _ := st.GetAnimeSubs(context.Background(), "c1")
			notified := map[string]bool{}
			for _, s := range doc.Shows {
				if s.Track == string(tc.to) {
					notified[s.Episode] = s.Notified
				}
			}
			if len(notified) != 2 || !notified["Ep 5"] || notified["Ep 6"] {
				t.Fatalf("new chain rows = %v, want Ep 5 notified and Ep 6 not (document %+v)", notified, doc.Shows)
			}
			later := trackedNow.Add(2 * time.Hour)
			processGuildNewEpisodes(context.Background(), d, newTestSenders(d), st, "g1", fn, later)
			processGuildNewEpisodes(context.Background(), d, newTestSenders(d), st, "g1", fn, later)
			if got := descriptions(d); len(got) != 1 || got[0] != tc.want {
				t.Errorf("posted %q once airing, want only %q", got, tc.want)
			}
		})
	}
}
