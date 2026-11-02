package bot

import (
	"context"
	"errors"
	"testing"
	"time"
)

const sendLimiterTestTimeout = 5 * time.Second

// drainedSendLimiter returns a limiter whose token bucket is empty, so the next Acquire has to wait.
// It is not started: no token comes back until the test calls Start, so waiters can be queued first.
func drainedSendLimiter(t *testing.T) *SendLimiter {
	t.Helper()
	l := NewSendLimiter()
	t.Cleanup(l.Stop)
	for i := 0; i < sendLimiterCap; i++ {
		if err := l.Acquire(context.Background(), PriorityFeeds); err != nil {
			t.Fatalf("draining token %d: %v", i, err)
		}
	}
	return l
}

// waitUntilQueued blocks until n callers are waiting inside the limiter.
func waitUntilQueued(t *testing.T, l *SendLimiter, n int) {
	t.Helper()
	deadline := time.Now().Add(sendLimiterTestTimeout)
	for {
		l.mu.Lock()
		queued := l.waiters.Len()
		l.mu.Unlock()
		if queued == n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("waited for %d queued callers, have %d", n, queued)
		}
		time.Sleep(time.Millisecond)
	}
}

func receiveWithin[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(sendLimiterTestTimeout):
		t.Fatalf("timed out waiting for %s", what)
		panic("unreachable")
	}
}

func TestSendLimiterServesHigherPriorityBeforeEarlierLowerPriority(t *testing.T) {
	l := drainedSendLimiter(t)

	served := make(chan int, 3)
	// Queue the lowest priority first, so arrival order is the opposite of priority order.
	for i, priority := range []int{PriorityFeeds, PriorityAnimeSubs, PriorityReminders} {
		go func() {
			if err := l.Acquire(context.Background(), priority); err == nil {
				served <- priority
			}
		}()
		waitUntilQueued(t, l, i+1)
	}

	l.Start()

	for _, want := range []int{PriorityReminders, PriorityAnimeSubs, PriorityFeeds} {
		if got := receiveWithin(t, served, "a waiter to be served"); got != want {
			t.Fatalf("served priority %d, want %d", got, want)
		}
	}
}

func TestSendLimiterStopReleasesEveryWaiterWithCancellation(t *testing.T) {
	l := drainedSendLimiter(t)
	l.Start()

	const waiting = 3
	results := make(chan error, waiting)
	for i := 0; i < waiting; i++ {
		go func() { results <- l.Acquire(context.Background(), PriorityFeeds) }()
	}
	waitUntilQueued(t, l, waiting)

	l.Stop()

	for i := 0; i < waiting; i++ {
		if err := receiveWithin(t, results, "a waiter to be released"); !errors.Is(err, context.Canceled) {
			t.Fatalf("released waiter got %v, want context.Canceled", err)
		}
	}
}

func TestSendLimiterStopTwiceIsHarmless(t *testing.T) {
	l := NewSendLimiter()
	l.Start()

	done := make(chan struct{})
	go func() {
		defer close(done)
		l.Stop()
		l.Stop()
	}()
	receiveWithin(t, done, "Stop to return twice")
}

func TestSendLimiterAcquireWithTokensReturnsImmediately(t *testing.T) {
	l := NewSendLimiter()
	l.Start()
	t.Cleanup(l.Stop)

	if err := l.Acquire(context.Background(), PriorityFeeds); err != nil {
		t.Fatalf("Acquire with a full bucket: %v", err)
	}
}

func TestSendLimiterEndedContextReturnsItsErrorAndLeavesTokenForOthers(t *testing.T) {
	l := drainedSendLimiter(t)

	ctx, cancel := context.WithCancel(context.Background())
	gaveUp := make(chan error, 1)
	go func() { gaveUp <- l.Acquire(ctx, PriorityReminders) }()
	waitUntilQueued(t, l, 1)

	other := make(chan error, 1)
	go func() { other <- l.Acquire(context.Background(), PriorityFeeds) }()
	waitUntilQueued(t, l, 2)

	cancel()
	if err := receiveWithin(t, gaveUp, "the cancelled waiter to return"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled waiter got %v, want context.Canceled", err)
	}
	waitUntilQueued(t, l, 1)

	// The cancelled caller outranked the other one, so a token spent on it would starve the other caller.
	l.Start()
	if err := receiveWithin(t, other, "the remaining waiter to be served"); err != nil {
		t.Fatalf("remaining waiter got %v, want a token", err)
	}
}

func TestSendLimiterDeadlineWhileWaitingReturnsDeadlineError(t *testing.T) {
	l := drainedSendLimiter(t)

	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()

	if err := l.Acquire(ctx, PriorityFeeds); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Acquire got %v, want context.DeadlineExceeded", err)
	}
}
