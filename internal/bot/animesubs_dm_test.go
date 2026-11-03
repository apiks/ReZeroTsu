package bot

import (
	"context"
	"errors"
	"testing"
	"time"

	"ReZeroTsu/internal/animeschedule"
	"ReZeroTsu/internal/database/anime_subs"
)

func TestProcessAnimeSubUser(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	aired := now.Unix() - 600
	ep := func(name, route, episode string, air int64) animeschedule.ShowEntry {
		return animeschedule.ShowEntry{Name: name, Route: route, Episode: episode, AirTimeUnix: air, AirType: "sub"}
	}
	const user = "u1"
	newState := func(subs ...*anime_subs.ShowSub) *fakeSentState {
		st := newFakeSentState()
		st.AnimeSubs[user] = &anime_subs.AnimeSubs{ID: user, Shows: subs}
		return st
	}
	run := func(ctx context.Context, d *fakeDiscord, st *fakeSentState, shows ...animeschedule.ShowEntry) error {
		doc, _ := st.GetAnimeSubs(ctx, user)
		return processAnimeSubUser(ctx, d, newTestSenders(d), st, doc, shows, now)
	}
	bg := context.Background()
	dm := "dm-" + user

	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "first DM is recorded with air time and episode key, rerun sends nothing",
			run: func(t *testing.T) {
				d, st := newFakeDiscord(), newState(&anime_subs.ShowSub{Show: "Show A"})
				show := ep("Show A", "show-a", "Episode 1", aired)
				_ = run(bg, d, st, show)
				_ = run(bg, d, st, show)
				if n := len(d.deliveredTo(dm)); n != 1 {
					t.Fatalf("DMs = %d, want 1", n)
				}
				s := st.userShow(user, "Show A")
				if !s.Notified || s.LastNotifiedAirUnix != aired || s.LastNotifiedEpisode == "" {
					t.Errorf("stored sub = %+v", s)
				}
			},
		},
		{
			name: "same episode with a later air time sends no second DM",
			run: func(t *testing.T) {
				d, st := newFakeDiscord(), newState(&anime_subs.ShowSub{Show: "Show A"})
				_ = run(bg, d, st, ep("Show A", "show-a", "Episode 1", aired))
				_ = run(bg, d, st, ep("Show A", "show-a", "Episode 1", aired+300))
				if n := len(d.deliveredTo(dm)); n != 1 {
					t.Fatalf("DMs = %d, want 1", n)
				}
			},
		},
		{
			name: "next episode of the same show sends one DM",
			run: func(t *testing.T) {
				d, st := newFakeDiscord(), newState(&anime_subs.ShowSub{Show: "Show A"})
				_ = run(bg, d, st, ep("Show A", "show-a", "Episode 1", aired-1000))
				_ = run(bg, d, st, ep("Show A", "show-a", "Episode 2", aired))
				if n := len(d.deliveredTo(dm)); n != 2 {
					t.Fatalf("DMs = %d, want 2", n)
				}
			},
		},
		{
			name: "episode key match skips even when air time moved earlier than stored",
			run: func(t *testing.T) {
				key := episodeKey(ep("Show A", "show-a", "Episode 1", aired))
				d, st := newFakeDiscord(), newState(&anime_subs.ShowSub{Show: "Show A", Notified: true, LastNotifiedAirUnix: aired + 5000, LastNotifiedEpisode: key})
				_ = run(bg, d, st, ep("Show A", "show-a", "Episode 1", aired+9000))
				if n := len(d.deliveredTo(dm)); n != 0 {
					t.Fatalf("DMs = %d, want 0", n)
				}
			},
		},
		{
			name: "legacy document with only an air time behaves as before",
			run: func(t *testing.T) {
				d := newFakeDiscord()
				st := newState(&anime_subs.ShowSub{Show: "Show A", Notified: true, LastNotifiedAirUnix: aired})
				_ = run(bg, d, st, ep("Show A", "show-a", "Episode 1", aired))
				if n := len(d.deliveredTo(dm)); n != 0 {
					t.Fatalf("same air time: DMs = %d, want 0", n)
				}
				_ = run(bg, d, st, ep("Show A", "show-a", "Episode 2", aired+1))
				if n := len(d.deliveredTo(dm)); n != 1 {
					t.Fatalf("later air time: DMs = %d, want 1", n)
				}
			},
		},
		{
			name: "shutdown mid-run records every DM already sent and a rerun sends only the rest",
			run: func(t *testing.T) {
				d := newFakeDiscord(succeed(), blockUntilCancelled())
				st := newState(&anime_subs.ShowSub{Show: "Show A"}, &anime_subs.ShowSub{Show: "Show B"})
				shows := []animeschedule.ShowEntry{ep("Show A", "show-a", "Episode 1", aired), ep("Show B", "show-b", "Episode 1", aired)}
				ctx, cancel := context.WithCancel(bg)
				d.OnBlock = cancel
				err := run(ctx, d, st, shows...)
				if err == nil || !errors.Is(err, context.Canceled) {
					t.Logf("run err = %v", err)
				}
				if s := st.userShow(user, "Show A"); !s.Notified {
					t.Fatalf("Show A not recorded after shutdown: %+v", s)
				}
				if s := st.userShow(user, "Show B"); s.Notified {
					t.Fatalf("Show B recorded although never delivered: %+v", s)
				}
				_ = run(bg, d, st, shows...)
				if n := len(d.deliveredTo(dm)); n != 2 {
					t.Fatalf("total DMs = %d, want 2 (A once, B once)", n)
				}
			},
		},
		{
			name: "timeout after delivery is verified and recorded without resending",
			run: func(t *testing.T) {
				d, st := newFakeDiscord(timeoutAfterDelivery()), newState(&anime_subs.ShowSub{Show: "Show A"})
				show := ep("Show A", "show-a", "Episode 1", aired)
				_ = run(bg, d, st, show)
				_ = run(bg, d, st, show)
				if n := len(d.deliveredTo(dm)); n != 1 {
					t.Fatalf("DMs = %d, want 1", n)
				}
				if !st.userShow(user, "Show A").Notified {
					t.Error("not recorded")
				}
			},
		},
		{
			name: "timeout after delivery with failing verification is treated as delivered",
			run: func(t *testing.T) {
				d, st := newFakeDiscord(timeoutAfterDelivery()), newState(&anime_subs.ShowSub{Show: "Show A"})
				d.RecentErr = errors.New("boom")
				show := ep("Show A", "show-a", "Episode 1", aired)
				_ = run(bg, d, st, show)
				_ = run(bg, d, st, show)
				if n := len(d.deliveredTo(dm)); n != 1 {
					t.Fatalf("DMs = %d, want 1", n)
				}
				if !st.userShow(user, "Show A").Notified {
					t.Error("not recorded")
				}
			},
		},
		{
			name: "duplicate entries: an episode recorded on the earlier entry is not DMed again",
			run: func(t *testing.T) {
				show := ep("Show A", "show-a", "Episode 1", aired)
				d := newFakeDiscord()
				st := newState(
					&anime_subs.ShowSub{Show: "Show A", Notified: true, LastNotifiedAirUnix: aired, LastNotifiedEpisode: episodeKey(show)},
					&anime_subs.ShowSub{Show: "show a"},
				)
				_ = run(bg, d, st, show)
				if n := len(d.deliveredTo(dm)); n != 0 {
					t.Fatalf("DMs = %d, want 0", n)
				}
			},
		},
		{
			name: "duplicate entries: the episode is not DMed again after the collapse",
			run: func(t *testing.T) {
				show := ep("Show A", "show-a", "Episode 2", aired)
				d := newFakeDiscord()
				st := newState(
					&anime_subs.ShowSub{Show: "Show A", Notified: true, LastNotifiedAirUnix: aired - 7*24*3600},
					&anime_subs.ShowSub{Show: " show a "},
					&anime_subs.ShowSub{Show: "SHOW A", Notified: true, LastNotifiedAirUnix: aired - 14*24*3600},
				)
				_ = run(bg, d, st, show)
				st.collapseUserShows(user)
				_ = run(bg, d, st, show)
				if n := len(d.deliveredTo(dm)); n != 1 {
					t.Fatalf("DMs = %d, want 1", n)
				}
				doc, _ := st.GetAnimeSubs(bg, user)
				if len(doc.Shows) != 1 {
					t.Fatalf("shows after collapse = %d, want 1", len(doc.Shows))
				}
			},
		},
		{
			name: "timeout without delivery is not recorded and is retried next run",
			run: func(t *testing.T) {
				d, st := newFakeDiscord(timeoutWithoutDelivery()), newState(&anime_subs.ShowSub{Show: "Show A"})
				show := ep("Show A", "show-a", "Episode 1", aired)
				_ = run(bg, d, st, show)
				if st.userShow(user, "Show A").Notified {
					t.Fatal("recorded although not delivered")
				}
				_ = run(bg, d, st, show)
				if n := len(d.deliveredTo(dm)); n != 1 {
					t.Fatalf("DMs = %d, want 1", n)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.run)
	}
}

// cancelOnOpenDM cancels the run (shutdown) while the DM channel is being created, failing it with the
// context error like the real context-aware OpenDM.
type cancelOnOpenDM struct {
	*fakeDiscord
	cancel context.CancelFunc
}

func (c *cancelOnOpenDM) OpenDM(ctx context.Context, _ string) (string, error) {
	c.cancel()
	return "", ctx.Err()
}

func TestProcessAnimeSubUserShutdownDuringOpenDMLogsNoError(t *testing.T) {
	logs := captureLogs(t)
	now := time.Unix(1_700_000_000, 0).UTC()
	st := newFakeSentState()
	st.AnimeSubs["u1"] = &anime_subs.AnimeSubs{ID: "u1", Shows: []*anime_subs.ShowSub{{Show: "Show A"}}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := &cancelOnOpenDM{fakeDiscord: newFakeDiscord(), cancel: cancel}
	doc, _ := st.GetAnimeSubs(ctx, "u1")
	show := animeschedule.ShowEntry{Name: "Show A", Route: "show-a", Episode: "Episode 1", AirTimeUnix: now.Unix() - 600, AirType: "sub"}
	err := processAnimeSubUser(ctx, d, newTestSenders(d), st, doc, []animeschedule.ShowEntry{show}, now)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if n := logs.count("ERROR"); n != 0 {
		t.Errorf("error logs = %d, want 0 on shutdown", n)
	}
}

func TestProcessAnimeSubUserMatchesByRouteFirst(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	aired := now.Unix() - 600
	const user = "u1"
	dm := "dm-" + user
	ep := func(name, route string) animeschedule.ShowEntry {
		return animeschedule.ShowEntry{Name: name, Route: route, Episode: "Episode 2", AirTimeUnix: aired, AirType: "sub"}
	}
	run := func(sub *anime_subs.ShowSub, show animeschedule.ShowEntry) (*fakeDiscord, *fakeSentState) {
		d, st := newFakeDiscord(), newFakeSentState()
		st.AnimeSubs[user] = &anime_subs.AnimeSubs{ID: user, Shows: []*anime_subs.ShowSub{sub}}
		doc, _ := st.GetAnimeSubs(context.Background(), user)
		_ = processAnimeSubUser(context.Background(), d, newTestSenders(d), st, doc, []animeschedule.ShowEntry{show}, now)
		return d, st
	}

	t.Run("a renamed show still gets its DM through the route", func(t *testing.T) {
		d, _ := run(&anime_subs.ShowSub{Show: "Old Title", Route: "show-a"}, ep("New Title", "show-a"))
		if n := len(d.deliveredTo(dm)); n != 1 {
			t.Fatalf("DMs = %d, want 1", n)
		}
	})
	t.Run("a subscription with a route is not matched by title", func(t *testing.T) {
		d, st := run(&anime_subs.ShowSub{Show: "Show A", Route: "show-a"}, ep("Show A", "other-route"))
		if n := len(d.deliveredTo(dm)); n != 0 {
			t.Fatalf("DMs = %d, want 0", n)
		}
		if got := st.userShow(user, "Show A").Route; got != "show-a" {
			t.Errorf("route = %q, want it unchanged", got)
		}
	})
	t.Run("a title match saves the entry's route", func(t *testing.T) {
		d, st := run(&anime_subs.ShowSub{Show: "Show A"}, ep("show a", "show-a"))
		if n := len(d.deliveredTo(dm)); n != 1 {
			t.Fatalf("DMs = %d, want 1", n)
		}
		if got := st.userShow(user, "Show A").Route; got != "show-a" {
			t.Errorf("route = %q, want show-a", got)
		}
	})
	t.Run("the route is saved even when the episode was already notified", func(t *testing.T) {
		show := ep("Show A", "show-a")
		d, st := run(&anime_subs.ShowSub{Show: "Show A", Notified: true, LastNotifiedAirUnix: aired}, show)
		if n := len(d.deliveredTo(dm)); n != 0 {
			t.Fatalf("DMs = %d, want 0", n)
		}
		if got := st.userShow(user, "Show A").Route; got != "show-a" {
			t.Errorf("route = %q, want show-a", got)
		}
	})
}
