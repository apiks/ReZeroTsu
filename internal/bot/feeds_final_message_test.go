package bot

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"ReZeroTsu/internal/database/guilds"
	"ReZeroTsu/internal/discord"
	"ReZeroTsu/internal/reddit"
)

func finalFeed(channel, subreddit, sort string) guilds.FeedEntry {
	return guilds.FeedEntry{Subreddit: subreddit, PostType: sort, ChannelID: channel}
}

// contentsTo lists the text of every message delivered to channel; a message with embeds reads "<embeds>".
func contentsTo(d *fakeDiscord, channel string) []string {
	var out []string
	for _, m := range d.deliveredTo(channel) {
		if len(m.Msg.Embeds) != 0 {
			out = append(out, "<embeds>")
			continue
		}
		out = append(out, m.Msg.Content)
	}
	return out
}

func finalText(feeds string) string {
	return "**Reddit feeds have stopped.** Reddit shut down the RSS feeds I read on 13 November 2026, so " + feeds + " won't post here any more. Nothing is needed from you."
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
			got := contentsTo(d, "c1")
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
	if got := contentsTo(d, "c1"); len(got) != 1 || got[0] != finalText("r/anime (new) and r/anime (top)") {
		t.Errorf("c1 messages = %q", got)
	}
	if got := contentsTo(d, "c2"); len(got) != 1 || got[0] != finalText("r/manga (hot)") {
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
	got := contentsTo(d, "c1")
	if len(got) != 1 {
		t.Fatalf("messages = %d, want 1", len(got))
	}
	m := regexp.MustCompile(`^\*\*Reddit feeds have stopped\.\*\* Reddit shut down the RSS feeds I read on 13 November 2026, so r/subreddit000 \(new\), .* and (\d+) more won't post here any more\. Nothing is needed from you\.$`).FindStringSubmatch(got[0])
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
	if len(contentsTo(d, "c1")) != 1 || d2.SendCalls != 0 {
		t.Errorf("first run %d messages, later runs %d sends", len(contentsTo(d, "c1")), d2.SendCalls)
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
	if got := contentsTo(d, "c1"); len(got) != 0 || len(st.FeedChecks) != 0 {
		t.Fatalf("delivered %q, recorded %d", got, len(st.FeedChecks))
	}
	if n := logs.count("ERROR") + logs.count("WARN"); n != 0 {
		t.Errorf("warnings or errors logged: %s", logs.buf.String())
	}
	runFeeds(context.Background(), d, st, feeds, nil)
	if got := contentsTo(d, "c1"); len(got) != 1 {
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
