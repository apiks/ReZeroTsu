package bot

import (
	"context"
	"testing"
	"time"

	"ReZeroTsu/internal/animeschedule"
	"ReZeroTsu/internal/database/anime_subs"
	"ReZeroTsu/internal/database/guilds"
)

// trackedNow is a fixed clock; rows below aired ten minutes before it.
var trackedNow = time.Unix(1_700_000_000, 0).UTC()

func trackedRow(name, ep string, track animeschedule.Track) animeschedule.ShowEntry {
	return animeschedule.ShowEntry{
		Name: name, Route: name, Episode: ep, AirTimeUnix: trackedNow.Unix() - 600, Track: track,
	}
}

// byPreference serves a different row list per preferred track, like the timetable's chain.
func byPreference(lists map[animeschedule.PreferredTrack][]animeschedule.ShowEntry) dayShowsFunc {
	return func(_ bool, pref animeschedule.PreferredTrack) []animeschedule.ShowEntry { return lists[pref] }
}

func trackedState(pref string, shows ...*anime_subs.ShowSub) *fakeSentState {
	st := newFakeSentState()
	st.Autoposts[apKey("g1", postTypeNewEpisodes)] = &guilds.Autopost{PostType: postTypeNewEpisodes, ID: "c1"}
	s := guilds.DefaultGuildSettings()
	s.PreferredTrack = pref
	st.Settings["g1"] = s
	if shows != nil {
		st.AnimeSubs["c1"] = &anime_subs.AnimeSubs{ID: "c1", IsGuild: true, LastSeedDate: trackedNow.Format("2006-01-02"), Shows: shows}
	}
	return st
}

func descriptions(d *fakeDiscord) []string {
	var out []string
	for _, m := range d.deliveredTo("c1") {
		for _, e := range m.Msg.Embeds {
			out = append(out, e.Description)
		}
	}
	return out
}

func TestNewEpisodesFollowThePreferredTrack(t *testing.T) {
	lists := map[animeschedule.PreferredTrack][]animeschedule.ShowEntry{
		animeschedule.PreferSub: {trackedRow("Frieren", "Ep 5", animeschedule.TrackSub), trackedRow("Rawonly", "Ep 1", animeschedule.TrackRaw)},
		animeschedule.PreferDub: {trackedRow("Frieren", "Ep 5", animeschedule.TrackDub), trackedRow("Rawonly", "Ep 1", animeschedule.TrackRaw)},
		animeschedule.PreferRaw: {trackedRow("Rawonly", "Ep 1", animeschedule.TrackRaw)},
	}
	for _, tc := range []struct {
		pref string
		want []string
	}{
		{"dub", []string{"**Ep 5** dubbed is out!", "**Ep 1** raw is out!"}},
		{"raw", []string{"**Ep 1** raw is out!"}},
		{"", []string{"**Ep 5** subbed is out!", "**Ep 1** raw is out!"}},
	} {
		t.Run("preference "+tc.pref, func(t *testing.T) {
			st := trackedState(tc.pref, &anime_subs.ShowSub{Show: "Frieren", Guild: true}, &anime_subs.ShowSub{Show: "Rawonly", Guild: true})
			d := newFakeDiscord()
			seedGuildNewEpisodesForShard(context.Background(), st, []string{"g1"}, byPreference(lists), trackedNow)
			processGuildNewEpisodes(context.Background(), d, newTestSenders(d), st, "g1", byPreference(lists), trackedNow)
			got := descriptions(d)
			if len(got) != len(tc.want) {
				t.Fatalf("posted %q, want %q", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("embed %d = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestSubAndDubOfOneShowOnOneDayArePostedOnceEach(t *testing.T) {
	rows := []animeschedule.ShowEntry{trackedRow("Frieren", "Ep 5", animeschedule.TrackSub), trackedRow("Frieren", "Ep 5", animeschedule.TrackDub)}
	fn := func(bool, animeschedule.PreferredTrack) []animeschedule.ShowEntry { return rows }
	st := trackedState("")
	// A new document starts aired rows as notified; start from a stale one so both are due.
	st.AnimeSubs["c1"] = &anime_subs.AnimeSubs{ID: "c1", IsGuild: true, LastSeedDate: "2023-11-13"}
	d := newFakeDiscord()
	seedGuildNewEpisodesForShard(context.Background(), st, []string{"g1"}, fn, trackedNow)
	doc, _ := st.GetAnimeSubs(context.Background(), "c1")
	if len(doc.Shows) != 2 || doc.Shows[0].Track != "sub" || doc.Shows[1].Track != "dub" {
		t.Fatalf("recorded %+v, want a sub entry and a dub entry apart", doc.Shows)
	}
	for i := 0; i < 2; i++ {
		processGuildNewEpisodes(context.Background(), d, newTestSenders(d), st, "g1", fn, trackedNow)
	}
	got := descriptions(d)
	if len(got) != 2 {
		t.Fatalf("posted %q, want the sub and the dub once each", got)
	}
	doc, _ = st.GetAnimeSubs(context.Background(), "c1")
	for _, s := range doc.Shows {
		if !s.Notified {
			t.Errorf("entry %+v not marked notified", s)
		}
	}
}

func TestDailyResetWritesTheTrackAndEpisodeIdentity(t *testing.T) {
	rows := []animeschedule.ShowEntry{trackedRow("Frieren", "Ep 5", animeschedule.TrackDub)}
	fn := func(bool, animeschedule.PreferredTrack) []animeschedule.ShowEntry { return rows }
	st := trackedState("", &anime_subs.ShowSub{Show: "Frieren", Guild: true, Notified: true})
	st.AnimeSubs["c1"].LastSeedDate = "2023-11-13"
	seedGuildNewEpisodesForShard(context.Background(), st, []string{"g1"}, fn, trackedNow)
	doc, _ := st.GetAnimeSubs(context.Background(), "c1")
	if len(doc.Shows) != 1 || doc.Shows[0].Track != "dub" || doc.Shows[0].Episode != "Ep 5" || doc.Shows[0].Notified {
		t.Errorf("reset entries = %+v, want one unnotified dub Ep 5", doc.Shows)
	}
}

func TestSameDayAppendUsesTheIdentity(t *testing.T) {
	// The show is already listed today, but its dub episode is a new airing.
	rows := []animeschedule.ShowEntry{trackedRow("Frieren", "Ep 5", animeschedule.TrackSub), trackedRow("Frieren", "Ep 5", animeschedule.TrackDub)}
	fn := func(bool, animeschedule.PreferredTrack) []animeschedule.ShowEntry { return rows }
	st := trackedState("", &anime_subs.ShowSub{Show: "Frieren", Track: "sub", Episode: "Ep 5", Guild: true, Notified: true})
	seedGuildNewEpisodesForShard(context.Background(), st, []string{"g1"}, fn, trackedNow)
	doc, _ := st.GetAnimeSubs(context.Background(), "c1")
	if len(doc.Shows) != 2 || doc.Shows[1].Track != "dub" || doc.Shows[1].Notified {
		t.Errorf("entries = %+v, want the listed sub plus an unnotified dub", doc.Shows)
	}
	// Seeding again appends nothing.
	seedGuildNewEpisodesForShard(context.Background(), st, []string{"g1"}, fn, trackedNow)
	if doc, _ = st.GetAnimeSubs(context.Background(), "c1"); len(doc.Shows) != 2 {
		t.Errorf("second seed left %d entries, want 2", len(doc.Shows))
	}
}

func TestDeployDayEntriesSuppressRepostsOfTheSubChainRow(t *testing.T) {
	// Written before this release: no track, no episode, notified.
	legacy := func() *fakeSentState {
		return trackedState("", &anime_subs.ShowSub{Show: "Frieren", Guild: true, Notified: true})
	}
	t.Run("a different episode of the posted show is not reposted", func(t *testing.T) {
		st := legacy()
		d := newFakeDiscord()
		fn := func(bool, animeschedule.PreferredTrack) []animeschedule.ShowEntry {
			return []animeschedule.ShowEntry{trackedRow("Frieren", "Ep 6", animeschedule.TrackSub)}
		}
		processGuildNewEpisodes(context.Background(), d, newTestSenders(d), st, "g1", fn, trackedNow)
		seedGuildNewEpisodesForShard(context.Background(), st, []string{"g1"}, fn, trackedNow)
		processGuildNewEpisodes(context.Background(), d, newTestSenders(d), st, "g1", fn, trackedNow)
		if got := descriptions(d); len(got) != 0 {
			t.Errorf("reposted %q", got)
		}
		if doc, _ := st.GetAnimeSubs(context.Background(), "c1"); len(doc.Shows) != 1 {
			t.Errorf("entries = %+v, want the legacy entry alone", doc.Shows)
		}
	})
	t.Run("the dub of a show whose sub was posted is still due", func(t *testing.T) {
		st := trackedState("dub", &anime_subs.ShowSub{Show: "Frieren", Guild: true, Notified: true})
		d := newFakeDiscord()
		fn := byPreference(map[animeschedule.PreferredTrack][]animeschedule.ShowEntry{
			animeschedule.PreferSub: {trackedRow("Frieren", "Ep 5", animeschedule.TrackSub)},
			animeschedule.PreferDub: {trackedRow("Frieren", "Ep 5", animeschedule.TrackDub)},
		})
		seedGuildNewEpisodesForShard(context.Background(), st, []string{"g1"}, fn, trackedNow)
		processGuildNewEpisodes(context.Background(), d, newTestSenders(d), st, "g1", fn, trackedNow)
		if got := descriptions(d); len(got) != 1 || got[0] != "**Ep 5** dubbed is out!" {
			t.Errorf("posted %q, want the dub alert", got)
		}
	})
}
