package bot

import (
	"container/heap"
	"context"
	"sync"
	"time"
)

// Send limiter priority; lower = higher priority.
const (
	PriorityReminders = 1
	PrioritySchedule  = 2
	PriorityAnimeSubs = 3
	PriorityFeeds     = 4
)

const (
	sendLimiterCap         = 40
	sendLimiterRefillEvery = 25 * time.Millisecond
)

const (
	webhookLimiterCap         = 5
	webhookLimiterRefillEvery = 200 * time.Millisecond
)

func runRefillLoop(stopCh <-chan struct{}, refill *time.Ticker, step func()) {
	for {
		select {
		case <-stopCh:
			return
		case <-refill.C:
			step()
		}
	}
}

// releaseRefillAndWaiters re-acquires mu, stops ticker, runs wakeAll with mu held. Used by Stop().
func releaseRefillAndWaiters(mu *sync.Mutex, refill *time.Ticker, wakeAll func()) {
	mu.Lock()
	defer mu.Unlock()
	if refill != nil {
		refill.Stop()
	}
	wakeAll()
}

// SendLimiter is a process-wide rate limiter for Discord sends; priority queue, fixed refill rate.
type SendLimiter struct {
	mu      sync.Mutex
	stopped bool
	tokens  int
	waiters waiterHeap
	refill  *time.Ticker
	stop    chan struct{}
}

// NewSendLimiter returns a limiter; call Start then Stop when done.
func NewSendLimiter() *SendLimiter {
	return &SendLimiter{
		tokens:  sendLimiterCap,
		waiters: make(waiterHeap, 0),
		stop:    make(chan struct{}),
	}
}

func (l *SendLimiter) Start() {
	l.refill = time.NewTicker(sendLimiterRefillEvery)
	go runRefillLoop(l.stop, l.refill, l.sendRefillStep)
}

// Stop stops refill and releases waiters with context.Canceled. Idempotent.
func (l *SendLimiter) Stop() {
	l.mu.Lock()
	if l.stopped {
		l.mu.Unlock()
		return
	}
	l.stopped = true
	close(l.stop)
	l.mu.Unlock()
	releaseRefillAndWaiters(&l.mu, l.refill, func() {
		for l.waiters.Len() > 0 {
			w := heap.Pop(&l.waiters).(*waiter)
			select {
			case w.ch <- context.Canceled:
			default:
			}
		}
	})
}

func (l *SendLimiter) sendRefillStep() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.tokens < sendLimiterCap {
		l.tokens++
	}
	for l.tokens > 0 && l.waiters.Len() > 0 {
		w := heap.Pop(&l.waiters).(*waiter)
		l.tokens--
		select {
		case w.ch <- nil:
		default:
			// waiter gave up (e.g. context canceled), put token back
			l.tokens++
		}
	}
}

// Acquire blocks until a token is available or ctx is done. Lower priority = served first.
func (l *SendLimiter) Acquire(ctx context.Context, priority int) error {
	l.mu.Lock()
	if l.tokens > 0 {
		l.tokens--
		l.mu.Unlock()
		return nil
	}
	ch := make(chan error, 1)
	heap.Push(&l.waiters, &waiter{priority: priority, ch: ch})
	l.mu.Unlock()

	select {
	case err := <-ch:
		return err
	case <-ctx.Done():
		// If a token was already sent while we were waiting, consume it and return to avoid leaking the token.
		select {
		case err := <-ch:
			return err
		default:
		}
		l.mu.Lock()
		for i := 0; i < l.waiters.Len(); i++ {
			if l.waiters[i].ch == ch {
				heap.Remove(&l.waiters, i)
				break
			}
		}
		l.mu.Unlock()
		// Non-blocking send so we don't block if runRefill already sent to ch.
		select {
		case ch <- ctx.Err():
		default:
		}
		return ctx.Err()
	}
}

type waiter struct {
	priority int
	ch       chan error
}

type waiterHeap []*waiter

func (h waiterHeap) Len() int           { return len(h) }
func (h waiterHeap) Less(i, j int) bool { return h[i].priority < h[j].priority }
func (h waiterHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }

func (h *waiterHeap) Push(x any) {
	*h = append(*h, x.(*waiter))
}

func (h *waiterHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[0 : n-1]
	return x
}

// WebhookLimiter is a FIFO rate limiter for webhook executes (e.g. 5/s).
type WebhookLimiter struct {
	mu      sync.Mutex
	stopped bool
	tokens  int
	waiters []chan error
	refill  *time.Ticker
	stop    chan struct{}
}

// NewWebhookLimiter returns a webhook limiter; call Start then Stop when done.
func NewWebhookLimiter() *WebhookLimiter {
	return &WebhookLimiter{
		tokens:  webhookLimiterCap,
		waiters: nil,
		stop:    make(chan struct{}),
	}
}

func (l *WebhookLimiter) Start() {
	l.refill = time.NewTicker(webhookLimiterRefillEvery)
	go runRefillLoop(l.stop, l.refill, l.webhookRefillStep)
}

// Stop stops refill and releases waiters with context.Canceled. Idempotent.
func (l *WebhookLimiter) Stop() {
	l.mu.Lock()
	if l.stopped {
		l.mu.Unlock()
		return
	}
	l.stopped = true
	close(l.stop)
	l.mu.Unlock()
	releaseRefillAndWaiters(&l.mu, l.refill, func() {
		for _, ch := range l.waiters {
			select {
			case ch <- context.Canceled:
			default:
			}
		}
		l.waiters = nil
	})
}

func (l *WebhookLimiter) webhookRefillStep() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.tokens < webhookLimiterCap {
		l.tokens++
	}
	for l.tokens > 0 && len(l.waiters) > 0 {
		ch := l.waiters[0]
		l.waiters = l.waiters[1:]
		l.tokens--
		select {
		case ch <- nil:
		default:
			l.tokens++
		}
	}
}

func (l *WebhookLimiter) Acquire(ctx context.Context) error {
	l.mu.Lock()
	if l.tokens > 0 {
		l.tokens--
		l.mu.Unlock()
		return nil
	}
	ch := make(chan error, 1)
	l.waiters = append(l.waiters, ch)
	l.mu.Unlock()

	select {
	case err := <-ch:
		return err
	case <-ctx.Done():
		// If a token was already sent while we were waiting, consume it and return to avoid leaking the token.
		select {
		case err := <-ch:
			return err
		default:
		}
		l.mu.Lock()
		for i := range l.waiters {
			if l.waiters[i] == ch {
				l.waiters = append(l.waiters[:i], l.waiters[i+1:]...)
				break
			}
		}
		l.mu.Unlock()
		// Non-blocking send so we don't block if runRefill already sent to ch.
		select {
		case ch <- ctx.Err():
		default:
		}
		return ctx.Err()
	}
}
