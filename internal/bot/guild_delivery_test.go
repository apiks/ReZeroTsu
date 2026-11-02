package bot

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"ReZeroTsu/internal/animeschedule"
	"ReZeroTsu/internal/database/anime_subs"
	"ReZeroTsu/internal/database/guilds"
)

// guildHarness is a guild "g1" with a new-episodes channel "c1" and n aired shows.
type guildHarness struct {
	d   *fakeDiscord
	st  *fakeSentState
	now time.Time
	fn  weekdayShows
}

func newGuildHarness(n int, script ...step) *guildHarness {
	now := time.Unix(1_700_000_000, 0).UTC()
	var shows []animeschedule.ShowEntry
	for i := 0; i < n; i++ {
		shows = append(shows, animeschedule.ShowEntry{
			Name: fmt.Sprintf("Show %d", i), Route: fmt.Sprintf("show-%d", i),
			Episode: "Episode 1", AirTimeUnix: now.Unix() - 600, AirType: "sub",
		})
	}
	st := newFakeSentState()
	st.Autoposts[apKey("g1", postTypeNewEpisodes)] = &guilds.Autopost{PostType: postTypeNewEpisodes, ID: "c1", RoleID: "r1"}
	// An existing document with nothing notified yet (a freshly seeded doc would start aired shows as notified).
	st.AnimeSubs["c1"] = &anime_subs.AnimeSubs{ID: "c1", IsGuild: true, LastSeedDate: now.Format("2006-01-02"), Shows: newGuildShowSubs(shows)}
	d := newFakeDiscord(script...)
	d.Roles["r1"] = true
	return &guildHarness{d: d, st: st, now: now, fn: func(bool) []animeschedule.ShowEntry { return shows }}
}

func (h *guildHarness) run(ctx context.Context) {
	processGuildNewEpisodes(ctx, h.d, h.st, "g1", h.fn, h.now)
}

func (h *guildHarness) notifiedCount() int {
	doc, _ := h.st.GetAnimeSubs(context.Background(), "c1")
	n := 0
	if doc != nil {
		for _, s := range doc.Shows {
			if s.Notified {
				n++
			}
		}
	}
	return n
}

func TestGuildNewEpisodesDeliveredAtMostOnce(t *testing.T) {
	tests := []struct {
		name          string
		script        []step
		recentErr     error
		webhook       *WebhookTarget
		wantDelivered []int // cumulative delivered messages after run 1, run 2
		wantSendCalls []int // cumulative Send calls after run 1, run 2
	}{
		{
			name: "timeout after delivery, verification succeeds: next run sends nothing",
			script: []step{timeoutAfterDelivery()}, wantDelivered: []int{1, 1}, wantSendCalls: []int{1, 1},
		},
		{
			name: "timeout after delivery, verification fails: next run sends nothing",
			script: []step{timeoutAfterDelivery()}, recentErr: errors.New("boom"),
			wantDelivered: []int{1, 1}, wantSendCalls: []int{1, 1},
		},
		{
			name: "timeout without delivery (verified missing): next run sends once",
			script: []step{timeoutWithoutDelivery()}, wantDelivered: []int{0, 1}, wantSendCalls: []int{1, 2},
		},
		{
			name: "5xx never causes a second send in the same run",
			script: []step{serverError()}, wantDelivered: []int{0, 1}, wantSendCalls: []int{1, 2},
		},
		{
			name: "5xx after delivery is not resent",
			script: []step{serverErrorAfterDelivery()}, wantDelivered: []int{1, 1}, wantSendCalls: []int{1, 1},
		},
		{
			name: "webhook timeout after delivery does not fall back",
			script: []step{timeoutAfterDelivery()}, webhook: &WebhookTarget{ID: "wh", Token: "t"},
			wantDelivered: []int{1, 1}, wantSendCalls: []int{1, 1},
		},
		{
			name: "unknown webhook falls back to the bot account exactly once",
			script: []step{rejectWith(10015)}, webhook: &WebhookTarget{ID: "wh", Token: "t"},
			wantDelivered: []int{1, 1}, wantSendCalls: []int{2, 2},
		},
		{
			name: "invalid webhook token falls back to the bot account exactly once",
			script: []step{rejectWith(50027)}, webhook: &WebhookTarget{ID: "wh", Token: "t"},
			wantDelivered: []int{1, 1}, wantSendCalls: []int{2, 2},
		},
		{
			name: "other rejected code on a webhook is not retried in the run",
			script: []step{rejectWith(99999)}, webhook: &WebhookTarget{ID: "wh", Token: "t"},
			wantDelivered: []int{0, 1}, wantSendCalls: []int{1, 2},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newGuildHarness(2, tt.script...)
			h.d.RecentErr = tt.recentErr
			h.d.Webhook = tt.webhook
			for i := 0; i < 2; i++ {
				h.run(context.Background())
				if got := len(h.d.Delivered); got != tt.wantDelivered[i] {
					t.Errorf("run %d: delivered = %d, want %d", i+1, got, tt.wantDelivered[i])
				}
				if h.d.SendCalls != tt.wantSendCalls[i] {
					t.Errorf("run %d: send calls = %d, want %d", i+1, h.d.SendCalls, tt.wantSendCalls[i])
				}
			}
		})
	}
}

func TestGuildNewEpisodesUnknownWebhookInvalidatesAndFallsBack(t *testing.T) {
	h := newGuildHarness(1, rejectWith(10015))
	h.d.Webhook = &WebhookTarget{ID: "wh", Token: "t"}
	h.run(context.Background())
	if len(h.d.Invalidated) != 1 {
		t.Errorf("invalidated = %v, want exactly one", h.d.Invalidated)
	}
	got := h.d.deliveredTo("c1")
	if len(got) != 1 || got[0].Webhook != nil {
		t.Fatalf("delivered = %+v, want one bot-account message", got)
	}
	if h.notifiedCount() != 1 {
		t.Errorf("notified = %d, want 1", h.notifiedCount())
	}
}

// A webhook rejected with a channel-terminal code (for example missing access on a private thread) is retried
// once through the bot account; the channel's autopost is only cleared when that attempt is rejected too.
func TestGuildNewEpisodesTerminalWebhookRejectionTriesBotAccount(t *testing.T) {
	tests := []struct {
		name          string
		script        []step
		wantDelivered int
		wantNotified  int
		wantCleared   bool
		wantInvalid   int
	}{
		{name: "webhook 50001 then bot success", script: []step{rejectWith(50001), succeed()}, wantDelivered: 1, wantNotified: 2},
		{name: "webhook 50013 then bot success", script: []step{rejectWith(50013), succeed()}, wantDelivered: 1, wantNotified: 2},
		{name: "webhook 50001 then bot 50001", script: []step{rejectWith(50001), rejectWith(50001)}, wantCleared: true, wantInvalid: 1},
		{name: "webhook 10003 then bot 10003", script: []step{rejectWith(10003), rejectWith(10003)}, wantCleared: true, wantInvalid: 1},
		{name: "webhook 50001 then bot non-terminal rejection", script: []step{rejectWith(50001), rejectWith(99999)}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newGuildHarness(2, tc.script...)
			h.d.Webhook = &WebhookTarget{ID: "wh", Token: "t"}
			h.run(context.Background())
			if h.d.SendCalls != 2 || h.d.WebhookSends != 1 {
				t.Errorf("sends = %d (webhook %d), want 2 (webhook 1)", h.d.SendCalls, h.d.WebhookSends)
			}
			got := h.d.deliveredTo("c1")
			if len(got) != tc.wantDelivered {
				t.Fatalf("delivered = %d, want %d", len(got), tc.wantDelivered)
			}
			if len(got) == 1 && got[0].Webhook != nil {
				t.Errorf("delivered through the webhook, want the bot account")
			}
			if n := h.notifiedCount(); n != tc.wantNotified {
				t.Errorf("notified = %d, want %d", n, tc.wantNotified)
			}
			ap, _ := h.st.GetAutopost(context.Background(), "g1", postTypeNewEpisodes)
			if (ap == nil) != tc.wantCleared {
				t.Errorf("autopost cleared = %v, want %v", ap == nil, tc.wantCleared)
			}
			if doc, _ := h.st.GetAnimeSubs(context.Background(), "c1"); (doc == nil) != tc.wantCleared {
				t.Errorf("sent state deleted = %v, want %v", doc == nil, tc.wantCleared)
			}
			if len(h.d.Invalidated) != tc.wantInvalid {
				t.Errorf("invalidated = %v, want %d", h.d.Invalidated, tc.wantInvalid)
			}
		})
	}
}

// After the bot account delivered a chunk the webhook was rejected for, later chunks skip the webhook.
func TestGuildNewEpisodesRejectedWebhookNotReusedForLaterChunks(t *testing.T) {
	h := newGuildHarness(11, rejectWith(50001))
	h.d.Webhook = &WebhookTarget{ID: "wh", Token: "t"}
	h.run(context.Background())
	if h.d.WebhookSends != 1 {
		t.Errorf("webhook attempts = %d, want 1", h.d.WebhookSends)
	}
	if got := len(h.d.deliveredTo("c1")); got != 2 {
		t.Errorf("delivered chunks = %d, want 2", got)
	}
}

func TestGuildNewEpisodesShutdownMidRun(t *testing.T) {
	// 11 due shows -> 2 batches. Batch 1 delivered, then shutdown while batch 2 is in flight.
	h := newGuildHarness(11, succeed(), blockUntilCancelled())
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	h.run(ctx)

	if len(h.d.Delivered) != 1 {
		t.Fatalf("delivered = %d, want 1", len(h.d.Delivered))
	}
	if got := h.notifiedCount(); got != 10 {
		t.Fatalf("notified after shutdown = %d, want 10 (the delivered batch)", got)
	}
	sendsAfterShutdown := h.d.SendCalls

	h.run(context.Background())
	if len(h.d.Delivered) != 2 {
		t.Errorf("after rerun delivered = %d, want 2 (only the missing batch sent)", len(h.d.Delivered))
	}
	if h.d.SendCalls != sendsAfterShutdown+1 {
		t.Errorf("rerun made %d sends, want 1", h.d.SendCalls-sendsAfterShutdown)
	}
	if h.notifiedCount() != 11 {
		t.Errorf("notified = %d, want 11", h.notifiedCount())
	}
}

func TestGuildNewEpisodesNoSendAfterCancel(t *testing.T) {
	h := newGuildHarness(2)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h.run(ctx)
	if h.d.SendCalls != 0 {
		t.Errorf("send calls = %d, want 0 after cancel", h.d.SendCalls)
	}
}

func TestSendWithRulesCancelledMidSendNeverFallsBack(t *testing.T) {
	d := newFakeDiscord(blockUntilCancelled())
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)
	res := sendWithRules(ctx, d, "g1", "c1", &WebhookTarget{ID: "wh", Token: "t"}, OutgoingMessage{})
	if res.Delivered {
		t.Error("reported delivered for an undelivered message")
	}
	if d.SendCalls != 1 {
		t.Errorf("send calls = %d, want 1 (no fallback on context error)", d.SendCalls)
	}
	if len(d.Invalidated) != 0 {
		t.Errorf("webhook invalidated on a context error: %v", d.Invalidated)
	}
}
