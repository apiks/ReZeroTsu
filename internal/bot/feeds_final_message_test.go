package bot

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"ReZeroTsu/internal/database/feed_checks"
	"ReZeroTsu/internal/database/guilds"
	"ReZeroTsu/internal/discord"
	"ReZeroTsu/internal/reddit"
)

func finalFeed(channel, subreddit, sort string) guilds.FeedEntry {
	return guilds.FeedEntry{Subreddit: subreddit, PostType: sort, ChannelID: channel}
}

func finalText(feeds string) string {
	return "**Reddit feeds have stopped.** Reddit shut down its RSS feeds on 13 November 2026, so " + feeds + " won't post here any more."
}

func TestFinalMessageNamesTheChannelsFeedsOnceEach(t *testing.T) {
	tests := []struct {
		name  string
		feeds []guilds.FeedEntry
		want  string
	}{
		{"one feed", []guilds.FeedEntry{finalFeed("c1", "anime", "new")}, "r/anime (new)"},
		{"two feeds", []guilds.FeedEntry{finalFeed("c1", "anime", "new"), finalFeed("c1", "anime", "hot")}, "r/anime (new) and r/anime (hot)"},
		{"three feeds", []guilds.FeedEntry{finalFeed("c1", "anime", "new"), finalFeed("c1", "manga", "top"), finalFeed("c1", "anime", "hot")}, "r/anime (new), r/manga (top) and r/anime (hot)"},
		{"filters and pin are not shown and repeats collapse", []guilds.FeedEntry{
			{Subreddit: "anime", PostType: "new", ChannelID: "c1", Author: "bob", Pin: true},
			{Subreddit: "anime", PostType: "new", ChannelID: "c1", Title: "news"},
		}, "r/anime (new)"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			setRedditNow(t, reddit.Cutoff)
			d, st := newFakeDiscord(), newFakeSentState()
			runFeeds(context.Background(), d, st, tc.feeds, parsedFor(3))
			got := contents(d, "c1")
			if len(got) != 1 || got[0] != finalText(tc.want) {
				t.Errorf("messages = %q, want one %q", got, finalText(tc.want))
			}
		})
	}
}

func TestFinalMessageGoesToEachChannelOnce(t *testing.T) {
	setRedditNow(t, reddit.Cutoff.Add(time.Hour))
	d, st := newFakeDiscord(), newFakeSentState()
	feeds := []guilds.FeedEntry{finalFeed("c1", "anime", "new"), finalFeed("c2", "manga", "hot"), finalFeed("c1", "anime", "top")}
	runFeeds(context.Background(), d, st, feeds, parsedFor(3))
	if got := contents(d, "c1"); len(got) != 1 || got[0] != finalText("r/anime (new) and r/anime (top)") {
		t.Errorf("c1 messages = %q", got)
	}
	if got := contents(d, "c2"); len(got) != 1 || got[0] != finalText("r/manga (hot)") {
		t.Errorf("c2 messages = %q", got)
	}
}

func TestFinalMessageCutsALongListWithAndNMore(t *testing.T) {
	setRedditNow(t, reddit.Cutoff)
	d, st := newFakeDiscord(), newFakeSentState()
	var feeds []guilds.FeedEntry
	for i := 0; i < 200; i++ {
		feeds = append(feeds, finalFeed("c1", fmt.Sprintf("subreddit%03d", i), "new"))
	}
	runFeeds(context.Background(), d, st, feeds, nil)
	got := contents(d, "c1")
	if len(got) != 1 {
		t.Fatalf("messages = %d, want 1", len(got))
	}
	m := regexp.MustCompile(`^\*\*Reddit feeds have stopped\.\*\* Reddit shut down its RSS feeds on 13 November 2026, so r/subreddit000 \(new\), .* and (\d+) more won't post here any more\.$`).FindStringSubmatch(got[0])
	if m == nil {
		t.Fatalf("no \"and N more\" ending in %q", got[0])
	}
	n, _ := strconv.Atoi(m[1])
	shown := strings.Count(got[0], "(new)")
	if shown+n != 200 || n < 1 {
		t.Errorf("shown %d and %d more, want 200 in all", shown, n)
	}
	if len(got[0]) > discord.MaxContentLength {
		t.Errorf("message is %d long, limit %d", len(got[0]), discord.MaxContentLength)
	}
	if len(got[0])+len(", r/subreddit000 (new)") <= discord.MaxContentLength-len(" and 200 more") && n > 1 {
		t.Errorf("the list stopped early: %d characters used of %d", len(got[0]), discord.MaxContentLength)
	}
}

func TestFinalMessageIsNotSentTwiceEvenAfterARestart(t *testing.T) {
	setRedditNow(t, reddit.Cutoff)
	d, st := newFakeDiscord(), newFakeSentState()
	feeds := []guilds.FeedEntry{finalFeed("c1", "anime", "new")}
	runFeeds(context.Background(), d, st, feeds, parsedFor(3))
	// A restart keeps the stored records and loses everything else.
	d2 := newFakeDiscord()
	runFeeds(context.Background(), d2, st, feeds, parsedFor(3))
	runFeeds(context.Background(), d2, st, feeds, parsedFor(3))
	if len(contents(d, "c1")) != 1 || d2.SendCalls != 0 {
		t.Errorf("first run %d messages, later runs %d sends", len(contents(d, "c1")), d2.SendCalls)
	}
}

func TestFinalMessageIsNotSentBeforeTheCutoff(t *testing.T) {
	setRedditNow(t, reddit.Cutoff.Add(-time.Second))
	d, st := newFakeDiscord(), newFakeSentState()
	runFeeds(context.Background(), d, st, []guilds.FeedEntry{finalFeed("c1", "anime", "new")}, nil)
	if d.SendCalls != 0 || len(st.FeedChecks) != 0 {
		t.Errorf("sends %d, recorded %d", d.SendCalls, len(st.FeedChecks))
	}
}

func TestFinalMessageToAChannelTheBotCannotPostInIsSilentAndRetried(t *testing.T) {
	setRedditNow(t, reddit.Cutoff)
	logs := captureLogs(t)
	d, st := newFakeDiscord(rejectWith(discord.MissingPermissionsCode)), newFakeSentState()
	feeds := []guilds.FeedEntry{finalFeed("c1", "anime", "new")}
	runFeeds(context.Background(), d, st, feeds, nil)
	if got := contents(d, "c1"); len(got) != 0 || len(st.FeedChecks) != 0 {
		t.Fatalf("delivered %q, recorded %d", got, len(st.FeedChecks))
	}
	if n := logs.count("ERROR") + logs.count("WARN"); n != 0 {
		t.Errorf("warnings or errors logged: %s", logs.buf.String())
	}
	runFeeds(context.Background(), d, st, feeds, nil)
	if got := contents(d, "c1"); len(got) != 1 {
		t.Errorf("later run delivered %d messages, want 1", len(got))
	}
}

func TestFinalMessageRejectedForAGoneChannelDeletesNothingAndIsNotRetried(t *testing.T) {
	for name, code := range map[string]int{
		"deleted channel":      discord.UnknownChannelCode,
		"inaccessible channel": discord.MissingAccessCode,
		"non-text channel":     discord.ChannelNotTextCode,
	} {
		t.Run(name, func(t *testing.T) {
			setRedditNow(t, reddit.Cutoff)
			logs := captureLogs(t)
			d, st := newFakeDiscord(rejectWith(code)), newFakeSentState()
			st.FeedChecks = []feed_checks.FeedCheck{{GUID: "t3_old_c1", GuildID: "g1", Feed: feed_checks.Feed{GuildID: "g1", ChannelID: "c1"}, Date: reddit.Cutoff}}
			feeds := []guilds.FeedEntry{finalFeed("c1", "anime", "new")}
			runFeeds(context.Background(), d, st, feeds, nil)
			if len(st.FeedsPurged) != 0 {
				t.Errorf("feeds removed: %v", st.FeedsPurged)
			}
			if len(st.FeedChecks) != 2 || st.FeedChecks[0].GUID != "t3_old_c1" || st.FeedChecks[1].GUID != finalMessageRecordKey("c1") {
				t.Errorf("feed checks = %+v, want the old record kept and the final message record added", st.FeedChecks)
			}
			if n := logs.count("ERROR") + logs.count("WARN"); n != 0 {
				t.Errorf("warnings or errors logged: %s", logs.buf.String())
			}
			sends := d.SendCalls
			runFeeds(context.Background(), d, st, feeds, nil)
			if d.SendCalls != sends {
				t.Errorf("channel retried: %d sends after the first run's %d", d.SendCalls, sends)
			}
		})
	}
}

func TestFinalMessageRejectedForAnotherReasonIsLoggedKeptAndRetried(t *testing.T) {
	setRedditNow(t, reddit.Cutoff)
	logs := captureLogs(t)
	d, st := newFakeDiscord(rejectWith(99999)), newFakeSentState()
	feeds := []guilds.FeedEntry{finalFeed("c1", "anime", "new")}
	runFeeds(context.Background(), d, st, feeds, nil)
	if len(st.FeedsPurged) != 0 || len(st.FeedChecks) != 0 {
		t.Errorf("feeds removed %v, recorded %d, want nothing", st.FeedsPurged, len(st.FeedChecks))
	}
	if strings.Count(logs.buf.String(), `msg="feed send failed"`) != 1 {
		t.Errorf("want one warning: %s", logs.buf.String())
	}
	runFeeds(context.Background(), d, st, feeds, nil)
	if got := contents(d, "c1"); len(got) != 1 {
		t.Errorf("later run delivered %d messages, want 1", len(got))
	}
}

func TestFinalMessageRecordLeavesPostRecordsAlone(t *testing.T) {
	setRedditNow(t, reddit.Cutoff.Add(-time.Hour))
	d, st := newFakeDiscord(), newFakeSentState()
	feeds := []guilds.FeedEntry{entry("c1", false)}
	runFeeds(context.Background(), d, st, feeds, parsedFor(3))
	if got := postChecks(st); got != 3 {
		t.Fatalf("posts recorded = %d, want 3", got)
	}
	setRedditNow(t, reddit.Cutoff)
	runFeeds(context.Background(), d, st, feeds, parsedFor(3))
	seen := map[string]bool{}
	for _, fc := range st.FeedChecks {
		seen[fc.GUID] = true
	}
	if len(st.FeedChecks) != 5 || len(seen) != 5 {
		t.Errorf("records = %d (%d distinct), want 3 posts, 1 daily warning and 1 final message", len(st.FeedChecks), len(seen))
	}
}
