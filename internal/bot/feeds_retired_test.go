package bot

import (
	"context"
	"testing"
	"time"

	"ReZeroTsu/internal/database/guilds"
	"ReZeroTsu/internal/reddit"
)

func setRedditNow(t *testing.T, now time.Time) {
	t.Helper()
	old := reddit.Now
	reddit.Now = func() time.Time { return now }
	t.Cleanup(func() { reddit.Now = old })
}

func TestFeedsDeliverNothingFromTheCutoffEvenWithCachedItems(t *testing.T) {
	for _, at := range []time.Time{reddit.Cutoff, reddit.Cutoff.Add(72 * time.Hour)} {
		setRedditNow(t, at)
		d, st := newFakeDiscord(), newFakeSentState()
		runFeeds(context.Background(), d, st, []guilds.FeedEntry{entry("c1", false), entry("c2", true)}, parsedFor(3))
		if d.SendCalls != 0 || len(st.FeedChecks) != 0 {
			t.Errorf("at %v: sends %d, recorded %d", at, d.SendCalls, len(st.FeedChecks))
		}
	}
}

func TestFeedsDeliverUnchangedJustBeforeTheCutoff(t *testing.T) {
	setRedditNow(t, reddit.Cutoff.Add(-time.Second))
	d, st := newFakeDiscord(), newFakeSentState()
	runFeeds(context.Background(), d, st, []guilds.FeedEntry{entry("c1", false)}, parsedFor(3))
	if got := len(embedAuthorURLs(d, "c1")); got != 3 {
		t.Errorf("delivered %d items, want 3", got)
	}
}

func TestRedditFetchLoopEndsOnceTheCutoffHasPassed(t *testing.T) {
	setRedditNow(t, reddit.Cutoff)
	done := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { RunRedditFetchLoop(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the fetch loop is still running after the cutoff")
	}
}
