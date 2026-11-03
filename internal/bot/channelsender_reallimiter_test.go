package bot

import (
	"context"
	"errors"
	"testing"
)

// The recording limiter honours shutdown by construction; the real send limiter does not always (its fast path never
// reads the context). These tests run the channel sender over the real limiter, so a wait that ends the way
// production ends it makes no send.
func TestChannelSenderPostWithRealSendLimiterMakesNoSendWhenTheWaitEnds(t *testing.T) {
	tests := []struct {
		name string
		end  func(l *SendLimiter, cancel context.CancelFunc)
	}{
		{"shutdown while queued", func(_ *SendLimiter, cancel context.CancelFunc) { cancel() }},
		{"limiter stopped while queued", func(l *SendLimiter, _ context.CancelFunc) { l.Stop() }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			limiter := drainedSendLimiter(t)
			d := newFakeDiscord()
			senders := newChannelSenderFactory(d, newWebhookCache(newFakeClock()), limiter)

			results := make(chan PostResult, 1)
			go func() {
				results <- senders.open("g1", "c1", newEpisodesPolicy()).post(ctx, OutgoingMessage{Content: "hello"})
			}()
			waitUntilQueued(t, limiter, 1)

			tc.end(limiter, cancel)

			res := receiveWithin(t, results, "the post to end")
			if res.Outcome != PostCancelled {
				t.Errorf("outcome = %v, want cancelled", res.Outcome)
			}
			if !errors.Is(res.Err, context.Canceled) {
				t.Errorf("err = %v, want context.Canceled", res.Err)
			}
			if d.SendCalls != 0 {
				t.Errorf("send calls = %d, want none", d.SendCalls)
			}
		})
	}
}
