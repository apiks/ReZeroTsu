package bot

import (
	"context"
	"strings"
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
		for _, ch := range []string{"c1", "c2"} {
			if got := contents(d, ch); len(got) != 1 || got[0] == "" {
				t.Errorf("at %v: %s got %q, want only the final message", at, ch, got)
			}
		}
		if len(st.FeedChecks) != 2 {
			t.Errorf("at %v: recorded %d, want 2 final message records", at, len(st.FeedChecks))
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

func TestRedditFetchLoopLogsTheRetirementOnceWhenItEnds(t *testing.T) {
	setRedditNow(t, reddit.Cutoff)
	logs := captureLogs(t)
	RunRedditFetchLoop(context.Background())
	if n := strings.Count(logs.buf.String(), "Reddit feeds retired"); n != 1 {
		t.Errorf("retirement logged %d times, want 1: %s", n, logs.buf.String())
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
