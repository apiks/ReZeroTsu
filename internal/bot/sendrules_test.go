package bot

import (
	"context"
	"errors"
	"testing"
	"time"
)

// A Send that reports it never made the request (the send-limiter wait in SendBatchToChannel ended by shutdown)
// is not delivered, not verified and never falls back, whatever the presume-on-verify-failure policy.
func TestDeliverWithRulesNotAttemptedSendIsNotSent(t *testing.T) {
	tests := []struct {
		name    string
		webhook *WebhookTarget
		presume bool
	}{
		{name: "bot account, presume on verify failure", presume: true},
		{name: "bot account, no presume", presume: false},
		{name: "webhook, presume on verify failure", webhook: &WebhookTarget{ID: "wh", Token: "t"}, presume: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := newFakeDiscord(blockUntilCancelled())
			// A failing verification read would make the presume policy report it delivered.
			d.RecentErr = errors.New("read failed")
			ctx, cancel := context.WithCancel(context.Background())
			time.AfterFunc(20*time.Millisecond, cancel)
			res, _ := deliverWithRules(ctx, d, "g1", "c1", tc.webhook, OutgoingMessage{Content: "x"}, matchRecentMessage, tc.presume, "test send")
			if res.Delivered || res.Presumed {
				t.Errorf("result = %+v, want not delivered", res)
			}
			if !errors.Is(res.Err, context.Canceled) {
				t.Errorf("err = %v, want context.Canceled", res.Err)
			}
			if d.RecentCalls != 0 {
				t.Errorf("verification reads = %d, want 0", d.RecentCalls)
			}
			if d.SendCalls != 1 {
				t.Errorf("send calls = %d, want 1 (no fallback)", d.SendCalls)
			}
		})
	}
}

// Once the bot account delivered a message the webhook was rejected for, the channel's webhook is dropped, so the
// next send (a later run that looks the webhook up again) does not spend another rejected request on it.
func TestDeliverWithRulesRejectedWebhookNotOfferedAfterFallback(t *testing.T) {
	tests := []struct {
		name   string
		reject int
	}{
		{name: "missing access (50001)", reject: 50001},
		{name: "missing permissions (50013)", reject: 50013},
		{name: "unknown webhook (10015)", reject: 10015},
		{name: "invalid webhook token (50027)", reject: 50027},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := newFakeDiscord(rejectWith(tc.reject), succeed())
			d.Webhook = &WebhookTarget{ID: "wh", Token: "t"}
			for run := 1; run <= 2; run++ {
				res, _ := deliverWithRules(context.Background(), d, "g1", "c1", d.ChannelWebhook("g1", "c1"),
					OutgoingMessage{Content: "x"}, matchRecentMessage, true, "test send")
				if !res.Delivered {
					t.Fatalf("run %d: result = %+v, want delivered", run, res)
				}
			}
			if d.WebhookSends != 1 {
				t.Errorf("webhook sends = %d, want 1 (the rejected webhook must not be used again)", d.WebhookSends)
			}
			if d.SendCalls != 3 {
				t.Errorf("send calls = %d, want 3 (webhook, fallback, then bot account)", d.SendCalls)
			}
		})
	}
}

// A webhook rejection whose bot-account fallback is not delivered leaves the webhook to the caller's terminal
// handling: it is not dropped for being channel-terminal alone.
func TestDeliverWithRulesTerminalWebhookRejectionKeptWhenFallbackFails(t *testing.T) {
	d := newFakeDiscord(rejectWith(50001), rejectWith(99999))
	d.Webhook = &WebhookTarget{ID: "wh", Token: "t"}
	res, _ := deliverWithRules(context.Background(), d, "g1", "c1", d.Webhook, OutgoingMessage{Content: "x"}, matchRecentMessage, true, "test send")
	if res.Delivered {
		t.Fatalf("result = %+v, want not delivered", res)
	}
	if len(d.Avoided) != 0 || len(d.Invalidated) != 0 {
		t.Errorf("avoided = %v, invalidated = %v, want none", d.Avoided, d.Invalidated)
	}
}

// After a channel-terminal webhook rejection, the webhook is avoided only when the bot-account fallback is
// confirmed delivered (a clean send or a verified Unknown), never when delivery is merely presumed.
func TestDeliverWithRulesAvoidsWebhookOnlyOnConfirmedFallback(t *testing.T) {
	tests := []struct {
		name          string
		fallback      step
		recentErr     error
		wantDelivered bool
		wantPresumed  bool
		wantAvoided   int
	}{
		{name: "clean fallback send", fallback: succeed(), wantDelivered: true, wantAvoided: 1},
		{name: "unknown fallback verified delivered", fallback: timeoutAfterDelivery(), wantDelivered: true, wantAvoided: 1},
		{name: "unknown fallback presumed delivered", fallback: timeoutAfterDelivery(), recentErr: errors.New("read failed"), wantDelivered: true, wantPresumed: true},
		{name: "unknown fallback verified missing", fallback: timeoutWithoutDelivery()},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := newFakeDiscord(rejectWith(50001), tc.fallback)
			d.RecentErr = tc.recentErr
			d.Webhook = &WebhookTarget{ID: "wh", Token: "t"}
			res, _ := deliverWithRules(context.Background(), d, "g1", "c1", d.Webhook, OutgoingMessage{Content: "x"}, matchRecentMessage, true, "test send")
			if res.Delivered != tc.wantDelivered || res.Presumed != tc.wantPresumed {
				t.Fatalf("result = %+v, want delivered %v presumed %v", res, tc.wantDelivered, tc.wantPresumed)
			}
			if len(d.Avoided) != tc.wantAvoided {
				t.Errorf("avoided = %v, want %d", d.Avoided, tc.wantAvoided)
			}
		})
	}
}

// A client-side request timeout may hide a delivered message, so it is verified (on the detached context),
// whether or not the run is shutting down when it fires.
func TestDeliverWithRulesClientTimeoutIsVerified(t *testing.T) {
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
			d := newFakeDiscord(tc.step)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.shutdown {
				time.AfterFunc(20*time.Millisecond, cancel)
			}
			res, _ := deliverWithRules(ctx, d, "g1", "c1", nil, OutgoingMessage{Content: "x"}, matchRecentMessage, false, "test send")
			if !res.Delivered {
				t.Fatalf("result = %+v, want delivered after verification", res)
			}
			if d.RecentCalls != 1 {
				t.Errorf("verification reads = %d, want 1", d.RecentCalls)
			}
		})
	}
}
