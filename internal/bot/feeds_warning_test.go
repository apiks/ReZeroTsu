package bot

import (
	"context"
	"strings"
	"testing"
	"time"

	"ReZeroTsu/internal/database/feed_checks"
	"ReZeroTsu/internal/database/guilds"
)

const wantFeedWarning = "**This Reddit feed stops on 13 November 2026.** Reddit is shutting down the RSS feeds I read, so all my Reddit feeds end that day. Nothing is needed from you."

var warningDay = time.Date(2026, time.November, 7, 9, 0, 0, 0, time.UTC)

// contents lists the content of every message delivered to channelID, in order.
func contents(d *fakeDiscord, channelID string) []string {
	var out []string
	for _, m := range d.deliveredTo(channelID) {
		out = append(out, m.Msg.Content)
	}
	return out
}

func countWarned(d *fakeDiscord, channelID string) int {
	n := 0
	for _, c := range contents(d, channelID) {
		if c == wantFeedWarning {
			n++
		}
	}
	return n
}

func TestFeedWarningOnFirstMessageOfTheDayOnly(t *testing.T) {
	setRedditNow(t, warningDay)
	d, st := newFakeDiscord(), newFakeSentState()
	feeds := []guilds.FeedEntry{entry("c1", false)}
	// 25 items: three messages in one run.
	runFeeds(context.Background(), d, st, feeds, parsedFor(25))
	got := contents(d, "c1")
	if len(got) != 3 || got[0] != wantFeedWarning || got[1] != "" || got[2] != "" {
		t.Fatalf("contents = %q", got)
	}
	if len(embedAuthorURLs(d, "c1")) != 25 {
		t.Fatalf("posts changed by the warning")
	}
}

func TestFeedWarningNotRepeatedAfterRestart(t *testing.T) {
	setRedditNow(t, warningDay)
	d, st := newFakeDiscord(), newFakeSentState()
	feeds := []guilds.FeedEntry{entry("c1", false)}
	runFeeds(context.Background(), d, st, feeds, parsedFor(1))
	// A restart forgets nothing the state holds; new posts arrive later the same day.
	setRedditNow(t, warningDay.Add(5*time.Hour))
	runFeeds(context.Background(), d, st, feeds, parsedFor(2))
	if got := countWarned(d, "c1"); got != 1 {
		t.Fatalf("warnings = %d, want 1", got)
	}
	if len(embedAuthorURLs(d, "c1")) != 2 {
		t.Fatalf("posts = %d, want 2", len(embedAuthorURLs(d, "c1")))
	}
}

func TestFeedWarningAgainOnTheNextUTCDay(t *testing.T) {
	setRedditNow(t, warningDay)
	d, st := newFakeDiscord(), newFakeSentState()
	feeds := []guilds.FeedEntry{entry("c1", false)}
	runFeeds(context.Background(), d, st, feeds, parsedFor(1))
	setRedditNow(t, time.Date(2026, time.November, 8, 0, 0, 1, 0, time.UTC))
	runFeeds(context.Background(), d, st, feeds, parsedFor(2))
	if got := countWarned(d, "c1"); got != 2 {
		t.Fatalf("warnings = %d, want 2", got)
	}
}

func TestFeedWarningOncePerChannelAcrossFeeds(t *testing.T) {
	setRedditNow(t, warningDay)
	d, st := newFakeDiscord(), newFakeSentState()
	feeds := []guilds.FeedEntry{
		{Subreddit: "sub", PostType: "new", ChannelID: "c1"},
		{Subreddit: "other", PostType: "hot", ChannelID: "c1"},
		{Subreddit: "sub", PostType: "new", ChannelID: "c2"},
	}
	parsed := parsedFor(2)
	parsed["other/hot"] = testFeed(2)
	runFeeds(context.Background(), d, st, feeds, parsed)
	if got := countWarned(d, "c1"); got != 1 {
		t.Errorf("c1 warnings = %d, want 1", got)
	}
	if got := countWarned(d, "c2"); got != 1 {
		t.Errorf("c2 warnings = %d, want 1", got)
	}
}

func TestFeedWarningOnPinnedMessage(t *testing.T) {
	setRedditNow(t, warningDay)
	d, st := newFakeDiscord(), newFakeSentState()
	runFeeds(context.Background(), d, st, []guilds.FeedEntry{entry("c1", true)}, parsedFor(2))
	got := contents(d, "c1")
	if len(got) != 2 || got[0] != wantFeedWarning || got[1] != "" {
		t.Fatalf("contents = %q", got)
	}
}

func TestFeedWarningMovesToNextSendWhenMessageDidNotLand(t *testing.T) {
	setRedditNow(t, warningDay)
	d, st := newFakeDiscord(rejectWith(99999)), newFakeSentState()
	feeds := []guilds.FeedEntry{entry("c1", false)}
	runFeeds(context.Background(), d, st, feeds, parsedFor(1))
	if len(st.FeedChecks) != 0 {
		t.Fatalf("recorded %d after a send that did not land", len(st.FeedChecks))
	}
	runFeeds(context.Background(), d, st, feeds, parsedFor(1))
	got := contents(d, "c1")
	if len(got) != 1 || got[0] != wantFeedWarning {
		t.Fatalf("contents = %q", got)
	}
}

func TestFeedWarningWrittenWithThePostsInOneWrite(t *testing.T) {
	setRedditNow(t, warningDay)
	d, st := newFakeDiscord(), newFakeSentState()
	runFeeds(context.Background(), d, st, []guilds.FeedEntry{entry("c1", false)}, parsedFor(2))
	if len(st.FeedCheckWrites) != 1 || st.FeedCheckWrites[0] != 3 {
		t.Fatalf("writes = %v, want one write of 2 posts and 1 warning", st.FeedCheckWrites)
	}
}

func TestFeedWarningRecordCannotClashAndIsSwept(t *testing.T) {
	setRedditNow(t, warningDay)
	d, st := newFakeDiscord(), newFakeSentState()
	runFeeds(context.Background(), d, st, []guilds.FeedEntry{entry("c1", false)}, parsedFor(1))
	var rec *feed_checks.FeedCheck
	for i, fc := range st.FeedChecks {
		if !strings.HasPrefix(fc.GUID, "t3_") {
			rec = &st.FeedChecks[i]
		}
	}
	if rec == nil {
		t.Fatal("no warning record")
	}
	if rec.Feed.ChannelID != "c1" || rec.GuildID != "g1" || rec.Date.IsZero() {
		t.Errorf("record %+v lacks channel, guild or date for removal and sweeping", *rec)
	}
	if !strings.Contains(rec.GUID, "2026-11-07") || !strings.Contains(rec.GUID, "c1") {
		t.Errorf("record key %q does not name the UTC date and channel", rec.GUID)
	}
	// Removing the channel's records takes it too.
	if _, err := st.DeleteFeedChecksByChannel(context.Background(), "g1", "c1"); err != nil {
		t.Fatal(err)
	}
	if len(st.FeedChecks) != 0 {
		t.Errorf("%d records left after the channel's records were removed", len(st.FeedChecks))
	}
}

func TestFeedWarningNotSentToAChannelWithoutPosts(t *testing.T) {
	setRedditNow(t, warningDay)
	d, st := newFakeDiscord(), newFakeSentState()
	runFeeds(context.Background(), d, st, []guilds.FeedEntry{entry("c1", false)}, parsedFor(0))
	if d.SendCalls != 0 || len(st.FeedChecks) != 0 {
		t.Fatalf("sends %d, records %d, want none", d.SendCalls, len(st.FeedChecks))
	}
}

// postChecks counts the feed checks that record a post, leaving the daily warning's record out.
func postChecks(st *fakeSentState) int {
	n := 0
	for _, fc := range st.FeedChecks {
		if fc.GUID != feedWarningKey(time.Now(), fc.Feed.ChannelID) {
			n++
		}
	}
	return n
}
