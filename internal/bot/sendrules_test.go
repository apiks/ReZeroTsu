package bot

import (
	"context"
	"errors"
	"testing"
	"time"
)

// A context error returned by Send after cancellation means the request was never made (production Send only
// observes ctx while waiting for a send limiter), so it is not delivered, not verified and never falls back,
// whatever the presume-on-verify-failure policy.
func TestDeliverWithRulesContextErrorFromSendIsNotSent(t *testing.T) {
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

// A context error while the run context is still live (a client-side request timeout) may hide a delivered
// message and is still verified.
func TestDeliverWithRulesLiveContextTimeoutIsVerified(t *testing.T) {
	d := newFakeDiscord(timeoutAfterDelivery())
	res, _ := deliverWithRules(context.Background(), d, "g1", "c1", nil, OutgoingMessage{Content: "x"}, matchRecentMessage, false, "test send")
	if !res.Delivered {
		t.Fatalf("result = %+v, want delivered after verification", res)
	}
	if d.RecentCalls != 1 {
		t.Errorf("verification reads = %d, want 1", d.RecentCalls)
	}
}
