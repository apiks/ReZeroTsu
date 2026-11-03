package bot

import (
	"context"
	"testing"
	"time"

	"ReZeroTsu/internal/animeschedule"
	"ReZeroTsu/internal/database/anime_subs"
	"ReZeroTsu/internal/database/guilds"
)

func TestGuildNewEpisodesSeeding(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC() // 2023-11-14
	today := now.Format("2006-01-02")
	aired := now.Unix() - 600
	future := now.Unix() + 3600
	one := animeschedule.ShowEntry{Name: "Aired One", Route: "aired-one", Episode: "Episode 1", AirTimeUnix: aired, AirType: "sub"}
	later := animeschedule.ShowEntry{Name: "Later", Route: "later", Episode: "Episode 3", AirTimeUnix: future, AirType: "sub"}
	delayed := animeschedule.ShowEntry{Name: "Delayed", Route: "delayed", Episode: "Episode 4", AirTimeUnix: aired, Delayed: "delayed", AirType: "sub"}
	late := animeschedule.ShowEntry{Name: "Late Listed", Route: "late-listed", Episode: "Episode 9", AirTimeUnix: future, AirType: "sub"}

	schedule := func(list ...animeschedule.ShowEntry) dayShowsFunc {
		return func(bool, animeschedule.PreferredTrack) []animeschedule.ShowEntry { return list }
	}
	newState := func() *fakeSentState {
		st := newFakeSentState()
		st.Autoposts[apKey("g1", postTypeNewEpisodes)] = &guilds.Autopost{PostType: postTypeNewEpisodes, ID: "c1"}
		return st
	}
	notifiedOf := func(t *testing.T, st *fakeSentState) map[string]bool {
		t.Helper()
		doc, _ := st.GetAnimeSubs(context.Background(), "c1")
		if doc == nil {
			t.Fatal("no anime subs doc for c1")
		}
		out := map[string]bool{}
		for _, s := range doc.Shows {
			out[s.Show] = s.Notified
		}
		return out
	}
	assertNotified := func(t *testing.T, got, want map[string]bool) {
		t.Helper()
		if len(got) != len(want) {
			t.Errorf("shows = %v, want %v", got, want)
		}
		for k, v := range want {
			if g, ok := got[k]; !ok || g != v {
				t.Errorf("notified[%q] = %v (present=%v), want %v", k, g, ok, v)
			}
		}
	}

	tests := []struct {
		name string
		run  func(t *testing.T, d *fakeDiscord, st *fakeSentState)
	}{
		{
			name: "newly created document marks aired non-delayed shows as notified and posts nothing",
			run: func(t *testing.T, d *fakeDiscord, st *fakeSentState) {
				processGuildNewEpisodes(context.Background(), d, newTestSenders(d), st, "g1", schedule(one, later, delayed), now)
				if len(d.Delivered) != 0 {
					t.Fatalf("delivered %d messages, want 0", len(d.Delivered))
				}
				assertNotified(t, notifiedOf(t, st), map[string]bool{"Aired One": true, "Later": false, "Delayed": false})
			},
		},
		{
			name: "disable then re-enable on the same day does not repost aired episodes",
			run: func(t *testing.T, d *fakeDiscord, st *fakeSentState) {
				sched := schedule(one, later)
				processGuildNewEpisodes(context.Background(), d, newTestSenders(d), st, "g1", sched, now)
				_ = st.DeleteAnimeSubs(context.Background(), "c1") // what /new-episodes enabled:false does
				processGuildNewEpisodes(context.Background(), d, newTestSenders(d), st, "g1", sched, now)
				if len(d.Delivered) != 0 {
					t.Fatalf("delivered %d messages, want 0", len(d.Delivered))
				}
			},
		},
		{
			name: "show that airs after the seed is announced once",
			run: func(t *testing.T, d *fakeDiscord, st *fakeSentState) {
				processGuildNewEpisodes(context.Background(), d, newTestSenders(d), st, "g1", schedule(one, later), now)
				after := now.Add(2 * time.Hour)
				processGuildNewEpisodes(context.Background(), d, newTestSenders(d), st, "g1", schedule(one, later), after)
				processGuildNewEpisodes(context.Background(), d, newTestSenders(d), st, "g1", schedule(one, later), after)
				got := d.deliveredTo("c1")
				if len(got) != 1 || len(got[0].Msg.Embeds) != 1 {
					t.Fatalf("delivered = %+v, want one message with one embed", got)
				}
			},
		},
		{
			name: "same-day seed appends newly listed shows without touching existing state",
			run: func(t *testing.T, d *fakeDiscord, st *fakeSentState) {
				st.AnimeSubs["c1"] = &anime_subs.AnimeSubs{ID: "c1", IsGuild: true, LastSeedDate: today, Shows: []*anime_subs.ShowSub{
					{Show: "Aired One", Notified: true, Guild: true, LastNotifiedAirUnix: 42},
					{Show: "Later", Notified: false, Guild: true},
				}}
				seedGuildNewEpisodesForShard(context.Background(), st, []string{"g1"}, schedule(one, later, late), now)
				assertNotified(t, notifiedOf(t, st), map[string]bool{"Aired One": true, "Later": false, "Late Listed": false})
				doc, _ := st.GetAnimeSubs(context.Background(), "c1")
				if doc.Shows[0].LastNotifiedAirUnix != 42 || doc.LastSeedDate != today {
					t.Errorf("existing entry or seed date rewritten: %+v", doc)
				}

				// the late show is announced once after it airs
				after := now.Add(2 * time.Hour)
				lateAired := late
				lateAired.AirTimeUnix = now.Unix() + 600
				processGuildNewEpisodes(context.Background(), d, newTestSenders(d), st, "g1", schedule(one, later, lateAired), after)
				processGuildNewEpisodes(context.Background(), d, newTestSenders(d), st, "g1", schedule(one, later, lateAired), after)
				if n := len(d.deliveredTo("c1")); n != 1 {
					t.Errorf("delivered %d messages, want 1", n)
				}
			},
		},
		{
			name: "same-day seed with nothing new leaves the document alone",
			run: func(t *testing.T, d *fakeDiscord, st *fakeSentState) {
				st.AnimeSubs["c1"] = &anime_subs.AnimeSubs{ID: "c1", IsGuild: true, LastSeedDate: today, Shows: []*anime_subs.ShowSub{
					{Show: "aired one", Notified: true, Guild: true},
				}}
				seedGuildNewEpisodesForShard(context.Background(), st, []string{"g1"}, schedule(one), now)
				if doc, _ := st.GetAnimeSubs(context.Background(), "c1"); len(doc.Shows) != 1 {
					t.Errorf("shows = %d, want 1", len(doc.Shows))
				}
			},
		},
		{
			name: "new-day rollover resets everything and posts episodes aired since midnight",
			run: func(t *testing.T, d *fakeDiscord, st *fakeSentState) {
				st.AnimeSubs["c1"] = &anime_subs.AnimeSubs{ID: "c1", IsGuild: true, LastSeedDate: "2023-11-13", Shows: []*anime_subs.ShowSub{
					{Show: "Aired One", Notified: true, Guild: true},
					{Show: "Old", Notified: true, Guild: true},
				}}
				seedGuildNewEpisodesForShard(context.Background(), st, []string{"g1"}, schedule(one, later), now)
				assertNotified(t, notifiedOf(t, st), map[string]bool{"Aired One": false, "Later": false})
				if doc, _ := st.GetAnimeSubs(context.Background(), "c1"); doc.LastSeedDate != today {
					t.Errorf("LastSeedDate = %q, want %q", doc.LastSeedDate, today)
				}
				processGuildNewEpisodes(context.Background(), d, newTestSenders(d), st, "g1", schedule(one, later), now)
				if n := len(d.deliveredTo("c1")); n != 1 {
					t.Errorf("delivered %d messages after rollover, want 1", n)
				}
			},
		},
		{
			name: "seed for a guild without an existing document marks aired shows notified",
			run: func(t *testing.T, d *fakeDiscord, st *fakeSentState) {
				seedGuildNewEpisodesForShard(context.Background(), st, []string{"g1"}, schedule(one, later), now)
				assertNotified(t, notifiedOf(t, st), map[string]bool{"Aired One": true, "Later": false})
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.run(t, newFakeDiscord(), newState())
		})
	}
}
