package bot

import (
	"context"
	"errors"
	"testing"
	"time"

	"ReZeroTsu/internal/animeschedule"
	"ReZeroTsu/internal/database/anime_subs"
	"ReZeroTsu/internal/database/guilds"
)

// Send-rule scenarios, driven through the per-target delivery functions (the channel sender has no test file of
// its own).

var testWebhook = &WebhookTarget{ID: "wh", Token: "t"}

// A Send that reports it never made the request (the send-limiter wait ended by shutdown) is not delivered, not
// verified and never falls back, whatever the presume-on-verify-failure policy. A failing verification read
// would make a presume policy report it delivered.
func TestSendRulesNotAttemptedSendIsNotDelivered(t *testing.T) {
	cancelOnBlock := func(d *fakeDiscord) context.Context {
		ctx, cancel := context.WithCancel(context.Background())
		d.OnBlock = cancel
		return ctx
	}
	check := func(t *testing.T, d *fakeDiscord) {
		t.Helper()
		if d.RecentCalls != 0 {
			t.Errorf("verification reads = %d, want 0", d.RecentCalls)
		}
		if d.SendCalls != 1 {
			t.Errorf("send calls = %d, want 1 (no fallback)", d.SendCalls)
		}
		if len(d.Delivered) != 0 {
			t.Errorf("delivered = %d, want 0", len(d.Delivered))
		}
	}

	for _, wh := range []*WebhookTarget{nil, testWebhook} {
		t.Run("new-episodes, presume policy", func(t *testing.T) {
			h := newGuildHarness(1, blockUntilCancelled())
			h.d.Webhook, h.d.RecentErr = wh, errors.New("read failed")
			h.run(cancelOnBlock(h.d))
			check(t, h.d)
			if h.notifiedCount() != 0 {
				t.Error("an unsent batch was recorded as notified")
			}
		})
		t.Run("daily schedule, no-presume policy", func(t *testing.T) {
			h := newScheduleHarness(blockUntilCancelled())
			h.fd.Webhook, h.fd.RecentErr = wh, errors.New("read failed")
			_ = h.run(cancelOnBlock(h.fd))
			check(t, h.fd)
			if h.lastPosted() != "" {
				t.Error("an unsent schedule was recorded as posted")
			}
		})
		t.Run("feeds, no-presume policy", func(t *testing.T) {
			d, st := newFakeDiscord(blockUntilCancelled()), newFakeSentState()
			d.Webhook, d.RecentErr = wh, errors.New("read failed")
			runFeeds(cancelOnBlock(d), d, st, []guilds.FeedEntry{entry("c1", false)}, parsedFor(1))
			check(t, d)
			if len(st.FeedChecks) != 0 {
				t.Error("an unsent feed item was recorded")
			}
		})
	}

	t.Run("episode DM, presume policy", func(t *testing.T) {
		now := time.Unix(1_700_000_000, 0).UTC()
		st := newFakeSentState()
		st.AnimeSubs["u1"] = &anime_subs.AnimeSubs{ID: "u1", Shows: []*anime_subs.ShowSub{{Show: "Show A"}}}
		d := newFakeDiscord(blockUntilCancelled())
		d.RecentErr = errors.New("read failed")
		ctx := cancelOnBlock(d)
		doc, _ := st.GetAnimeSubs(ctx, "u1")
		shows := []animeschedule.ShowEntry{{Name: "Show A", Route: "show-a", Episode: "Episode 1", AirTimeUnix: now.Unix() - 600, AirType: "sub"}}
		_ = processAnimeSubUser(ctx, d, newTestSenders(d), st, doc, shows, now)
		check(t, d)
		if st.userShow("u1", "Show A").Notified {
			t.Error("an unsent DM was recorded as notified")
		}
	})
	t.Run("reminder, presume policy", func(t *testing.T) {
		st := stateWith("u1", dueReminder(1, "hi"))
		d := newFakeDiscord(blockUntilCancelled())
		d.RecentErr = errors.New("read failed")
		_ = runSlice(t, cancelOnBlock(d), d, st, "u1")
		check(t, d)
		if got := st.reminderIDs("u1"); len(got) != 1 {
			t.Errorf("reminders left = %v, want the unsent one kept", got)
		}
	})
}

// After a channel-terminal webhook rejection that the bot account then delivered without presumption, the next
// run does not spend another request on the webhook. A gone webhook is only invalidated: the next run uses a
// webhook again. A presumed or unconfirmed fallback never avoids the webhook.
func TestGuildNewEpisodesRejectedWebhookAvoidedOnlyAfterConfirmedFallback(t *testing.T) {
	tests := []struct {
		name             string
		script           []step
		recentErr        error
		wantWebhookSends int // after two runs
		wantResolves     int // after two runs: a webhook the cache still holds is not resolved again
	}{
		{name: "missing access (50001)", script: []step{rejectWith(50001), succeed()}, wantWebhookSends: 1, wantResolves: 1},
		{name: "missing permissions (50013)", script: []step{rejectWith(50013), succeed()}, wantWebhookSends: 1, wantResolves: 1},
		{name: "unknown webhook (10015)", script: []step{rejectWith(10015), succeed()}, wantWebhookSends: 2, wantResolves: 2},
		{name: "invalid webhook token (50027)", script: []step{rejectWith(50027), succeed()}, wantWebhookSends: 2, wantResolves: 2},
		{name: "unknown fallback verified delivered", script: []step{rejectWith(50001), timeoutAfterDelivery()}, wantWebhookSends: 1, wantResolves: 1},
		{name: "unknown fallback presumed delivered", script: []step{rejectWith(50001), timeoutAfterDelivery()},
			recentErr: errors.New("read failed"), wantWebhookSends: 2, wantResolves: 1},
		{name: "unknown fallback verified missing", script: []step{rejectWith(50001), timeoutWithoutDelivery()}, wantWebhookSends: 2, wantResolves: 1},
		{name: "fallback rejected too", script: []step{rejectWith(50001), rejectWith(99999)}, wantWebhookSends: 2, wantResolves: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newGuildHarness(1, tc.script...)
			h.d.Webhook, h.d.RecentErr = testWebhook, tc.recentErr
			for run := 1; run <= 2; run++ {
				h.run(context.Background())
				h.markAllDue()
			}
			if h.d.WebhookSends != tc.wantWebhookSends {
				t.Errorf("webhook sends = %d, want %d", h.d.WebhookSends, tc.wantWebhookSends)
			}
			if h.d.ResolveCalls != tc.wantResolves {
				t.Errorf("resolve calls = %d, want %d", h.d.ResolveCalls, tc.wantResolves)
			}
		})
	}
}

// A client-side request timeout may hide a delivered message, so it is verified (on the detached context),
// whether or not the run is shutting down when it fires.
func TestGuildNewEpisodesClientTimeoutIsVerified(t *testing.T) {
	tests := []struct {
		name     string
		step     step
		shutdown bool
	}{
		{name: "live run context", step: timeoutAfterDelivery()},
		{name: "in-flight timeout during shutdown", step: inFlightTimeoutAtShutdown(), shutdown: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newGuildHarness(1, tc.step)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.shutdown {
				h.d.OnBlock = cancel
			}
			h.run(ctx)
			if h.notifiedCount() != 1 {
				t.Errorf("notified = %d, want 1 (verified delivered)", h.notifiedCount())
			}
			if h.d.RecentCalls != 1 {
				t.Errorf("verification reads = %d, want 1", h.d.RecentCalls)
			}
		})
	}
}

// DMs, reminders and pinned feed items post through the bot account and never look up a webhook.
func TestBotAccountOnlyPostsNeverLookUpWebhook(t *testing.T) {
	assertBotOnly := func(t *testing.T, d *fakeDiscord, wantDelivered int) {
		t.Helper()
		if d.ResolveCalls != 0 || d.WebhookSends != 0 {
			t.Errorf("webhook resolves = %d, webhook sends = %d, want none", d.ResolveCalls, d.WebhookSends)
		}
		if len(d.Delivered) != wantDelivered {
			t.Errorf("delivered = %d, want %d", len(d.Delivered), wantDelivered)
		}
	}
	t.Run("episode DM", func(t *testing.T) {
		now := time.Unix(1_700_000_000, 0).UTC()
		st := newFakeSentState()
		st.AnimeSubs["u1"] = &anime_subs.AnimeSubs{ID: "u1", Shows: []*anime_subs.ShowSub{{Show: "Show A"}}}
		d := newFakeDiscord()
		d.Webhook = testWebhook
		doc, _ := st.GetAnimeSubs(context.Background(), "u1")
		shows := []animeschedule.ShowEntry{{Name: "Show A", Route: "show-a", Episode: "Episode 1", AirTimeUnix: now.Unix() - 600, AirType: "sub"}}
		_ = processAnimeSubUser(context.Background(), d, newTestSenders(d), st, doc, shows, now)
		assertBotOnly(t, d, 1)
	})
	t.Run("reminder", func(t *testing.T) {
		st := stateWith("u1", dueReminder(1, "hi"))
		d := newFakeDiscord()
		d.Webhook = testWebhook
		_ = runSlice(t, context.Background(), d, st, "u1")
		assertBotOnly(t, d, 1)
	})
	t.Run("pinned feed item", func(t *testing.T) {
		d, st := newFakeDiscord(), newFakeSentState()
		d.Webhook = testWebhook
		runFeeds(context.Background(), d, st, []guilds.FeedEntry{entry("c1", true)}, parsedFor(1))
		assertBotOnly(t, d, 1)
		if len(d.Pinned) != 1 {
			t.Errorf("pinned = %v, want one", d.Pinned)
		}
	})
}
