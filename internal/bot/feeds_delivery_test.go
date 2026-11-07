package bot

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"ReZeroTsu/internal/database/feed_checks"
	"ReZeroTsu/internal/database/guilds"
	"ReZeroTsu/internal/discord"

	"github.com/bwmarrin/discordgo"
	"github.com/mmcdole/gofeed"
)

func init() {
	feedWebhookThrottle = 0
	feedPinThrottle = 0
}

func testFeedItem(i int) *gofeed.Item {
	return &gofeed.Item{
		GUID:        fmt.Sprintf("t3_%d", i),
		Link:        fmt.Sprintf("https://www.reddit.com/r/sub/comments/%d/", i),
		Title:       fmt.Sprintf("Post %d", i),
		Description: fmt.Sprintf("desc %d", i),
	}
}

func testFeed(n int) *gofeed.Feed {
	f := &gofeed.Feed{}
	for i := 0; i < n; i++ {
		f.Items = append(f.Items, testFeedItem(i))
	}
	return f
}

func parsedFor(n int) map[string]*gofeed.Feed {
	return map[string]*gofeed.Feed{"sub/new": testFeed(n)}
}

func entry(channel string, pin bool) guilds.FeedEntry {
	return guilds.FeedEntry{Subreddit: "sub", PostType: "new", ChannelID: channel, Pin: pin}
}

func runFeeds(ctx context.Context, d fakeDiscordPort, st SentState, feeds []guilds.FeedEntry, parsed map[string]*gofeed.Feed) {
	processGuildFeeds(ctx, d, newTestSenders(d), st, "g1", feeds, parsed)
}

func TestFeedsNoRepostWithMoreThanFortyFeeds(t *testing.T) {
	var feeds []guilds.FeedEntry
	st := newFakeSentState()
	parsed := parsedFor(30)
	for c := 0; c < 45; c++ {
		ch := fmt.Sprintf("c%d", c)
		feeds = append(feeds, entry(ch, false))
		for i := 0; i < 30; i++ {
			st.FeedChecks = append(st.FeedChecks, feed_checks.FeedCheck{GuildID: "g1", GUID: feedItemCompositeGUID(testFeedItem(i), ch)})
		}
	}
	d := newFakeDiscord()
	runFeeds(context.Background(), d, st, feeds, parsed)
	if d.SendCalls != 0 {
		t.Fatalf("expected no sends for items already recorded, got %d", d.SendCalls)
	}
}

func TestFeedsItemSentOncePerChannel(t *testing.T) {
	tests := []struct {
		name      string
		feeds     []guilds.FeedEntry
		wantItems int
	}{
		{"two feeds same channel", []guilds.FeedEntry{entry("c1", false), entry("c1", false)}, 3},
		{"two feeds different channels", []guilds.FeedEntry{entry("c1", false), entry("c2", false)}, 6},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d, st := newFakeDiscord(), newFakeSentState()
			runFeeds(context.Background(), d, st, tc.feeds, parsedFor(3))
			got := len(embedAuthorURLs(d, "c1")) + len(embedAuthorURLs(d, "c2"))
			if got != tc.wantItems {
				t.Fatalf("delivered %d items, want %d", got, tc.wantItems)
			}
		})
	}
}

func TestFeedsUnknownOutcome(t *testing.T) {
	tests := []struct {
		name         string
		step         step
		recentErr    error
		wantSavedAll bool
		wantResend   int // sends on the second run
	}{
		{"timeout after delivery is verified and recorded", timeoutAfterDelivery(), nil, true, 0},
		{"timeout without delivery is retried", timeoutWithoutDelivery(), nil, false, 1},
		{"verification failure counts as not delivered", timeoutAfterDelivery(), errors.New("read failed"), false, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d, st := newFakeDiscord(tc.step), newFakeSentState()
			d.RecentErr = tc.recentErr
			feeds := []guilds.FeedEntry{entry("c1", false)}
			runFeeds(context.Background(), d, st, feeds, parsedFor(2))
			if saved := postChecks(st) == 2; saved != tc.wantSavedAll {
				t.Fatalf("feed checks saved = %d, wantSavedAll=%v", len(st.FeedChecks), tc.wantSavedAll)
			}
			before := d.SendCalls
			d.RecentErr = nil
			runFeeds(context.Background(), d, st, feeds, parsedFor(2))
			if got := d.SendCalls - before; got != tc.wantResend {
				t.Fatalf("second run sends = %d, want %d", got, tc.wantResend)
			}
		})
	}
}

func TestFeedsVerificationIgnoresOtherBotMessages(t *testing.T) {
	// An unrelated recent bot embed (same empty top-level URL, same description) must not count as the item.
	d, st := newFakeDiscord(timeoutWithoutDelivery()), newFakeSentState()
	other := testFeedItem(99)
	other.Description = "desc 0"
	d.Delivered = append(d.Delivered, delivered{ChannelID: "c1", Msg: OutgoingMessage{Embeds: []*discordgo.MessageEmbed{buildFeedEmbed(entry("c1", false), other)}}})
	runFeeds(context.Background(), d, st, []guilds.FeedEntry{entry("c1", false)}, parsedFor(1))
	if len(st.FeedChecks) != 0 {
		t.Fatalf("item recorded as delivered on a non-matching message")
	}
}

func TestFeedsShutdownMidFlush(t *testing.T) {
	const items = 25 // three chunks of at most 10 embeds
	d, st := newFakeDiscord(), strictWriteState{newFakeSentState()}
	ctx, cancel := context.WithCancel(context.Background())
	p := &cancelAfterSendPort{fakeDiscord: d, cancel: cancel, after: 1}
	feeds := []guilds.FeedEntry{entry("c1", false)}
	runFeeds(ctx, p, st, feeds, parsedFor(items))
	if got := len(embedAuthorURLs(d, "c1")); got != 10 {
		t.Fatalf("delivered %d before shutdown, want 10", got)
	}
	if got := postChecks(st.fakeSentState); got != 10 {
		t.Fatalf("recorded %d delivered items, want 10", got)
	}
	runFeeds(context.Background(), d, st, feeds, parsedFor(items))
	urls := embedAuthorURLs(d, "c1")
	seen := map[string]bool{}
	for _, u := range urls {
		if seen[u] {
			t.Fatalf("item %s delivered twice", u)
		}
		seen[u] = true
	}
	if len(urls) != items {
		t.Fatalf("delivered %d in total, want %d", len(urls), items)
	}
}

func TestFeedsNoSendAfterCancel(t *testing.T) {
	d, st := newFakeDiscord(), newFakeSentState()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runFeeds(ctx, d, st, []guilds.FeedEntry{entry("c1", false), entry("c2", true)}, parsedFor(2))
	if d.SendCalls != 0 {
		t.Fatalf("sent %d messages after cancel", d.SendCalls)
	}
}

func TestFeedsWebhookFallback(t *testing.T) {
	tests := []struct {
		name          string
		steps         []step
		wantSends     int
		wantDelivered int
		wantPurged    int
	}{
		{"unknown webhook falls back to bot", []step{rejectWith(discord.UnknownWebhookCode)}, 2, 1, 0},
		{"missing permissions on the webhook falls back to bot", []step{rejectWith(discord.MissingPermissionsCode)}, 2, 1, 0},
		{"missing access on the webhook falls back to bot", []step{rejectWith(discord.MissingAccessCode)}, 2, 1, 0},
		{"missing access on both removes the feeds once", []step{rejectWith(discord.MissingAccessCode), rejectWith(discord.MissingAccessCode)}, 2, 0, 1},
		{"other rejection does not fall back", []step{rejectWith(99999)}, 1, 0, 0},
		{"timeout does not fall back", []step{timeoutWithoutDelivery()}, 1, 0, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d, st := newFakeDiscord(tc.steps...), newFakeSentState()
			d.Webhook = &WebhookTarget{ID: "wh", Token: "tok"}
			runFeeds(context.Background(), d, st, []guilds.FeedEntry{entry("c1", false)}, parsedFor(1))
			if d.SendCalls != tc.wantSends {
				t.Fatalf("sends = %d, want %d", d.SendCalls, tc.wantSends)
			}
			if got := len(d.deliveredTo("c1")); got != tc.wantDelivered {
				t.Fatalf("delivered = %d, want %d", got, tc.wantDelivered)
			}
			if got := len(st.FeedsPurged); got != tc.wantPurged {
				t.Fatalf("feeds purged = %v, want %d", st.FeedsPurged, tc.wantPurged)
			}
		})
	}
}

func TestFeedsDeadWebhookIsNotReusedForLaterChunks(t *testing.T) {
	d, st := newFakeDiscord(rejectWith(discord.UnknownWebhookCode)), newFakeSentState()
	d.Webhook = &WebhookTarget{ID: "wh", Token: "tok"}
	// 11 items handed to one flush make two chunks (10 + 1).
	var items []sendItem
	for i := 0; i < 11; i++ {
		items = append(items, sendItem{
			embed:         &discordgo.MessageEmbed{Author: &discordgo.MessageEmbedAuthor{Name: fmt.Sprintf("a%d", i), URL: fmt.Sprintf("https://x/%d", i)}},
			feed:          entry("c1", false),
			compositeGUID: fmt.Sprintf("g%d", i),
		})
	}
	flushChannel(context.Background(), d, newTestSenders(d), st, "g1", "c1", items)
	if d.WebhookSends != 1 {
		t.Errorf("webhook attempts = %d, want 1 (later chunks must not reuse the dead webhook)", d.WebhookSends)
	}
	if got := len(d.deliveredTo("c1")); got != 2 {
		t.Errorf("delivered chunks = %d, want 2", got)
	}
}

func TestFeedsChunkRecordedWithOneWrite(t *testing.T) {
	tests := []struct {
		name       string
		items      int
		script     []step
		wantWrites []int // items per feed-check write, in order
	}{
		{name: "one chunk", items: 3, wantWrites: []int{3}},
		{name: "two chunks", items: 11, wantWrites: []int{10, 1}},
		{name: "undelivered chunk is not recorded", items: 11, script: []step{succeed(), rejectWith(99999)}, wantWrites: []int{10}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d, st := newFakeDiscord(tc.script...), newFakeSentState()
			var items []sendItem
			for i := 0; i < tc.items; i++ {
				items = append(items, sendItem{
					embed:         &discordgo.MessageEmbed{Author: &discordgo.MessageEmbedAuthor{Name: fmt.Sprintf("a%d", i), URL: fmt.Sprintf("https://x/%d", i)}},
					feed:          entry("c1", false),
					compositeGUID: fmt.Sprintf("g%d", i),
				})
			}
			flushChannel(context.Background(), d, newTestSenders(d), st, "g1", "c1", items)
			// The first message of the day also writes the channel's warning record.
			writes := slices.Clone(st.FeedCheckWrites)
			if len(writes) > 0 {
				writes[0]--
			}
			if fmt.Sprint(writes) != fmt.Sprint(tc.wantWrites) {
				t.Errorf("feed-check writes = %v, want %v", writes, tc.wantWrites)
			}
			want := 0
			for _, n := range tc.wantWrites {
				want += n
			}
			if postChecks(st) != want {
				t.Errorf("feed checks stored = %d, want %d", postChecks(st), want)
			}
		})
	}
}

func TestFeedsDeletedChannelRemovesFeeds(t *testing.T) {
	d, st := newFakeDiscord(rejectWith(discord.UnknownChannelCode)), newFakeSentState()
	runFeeds(context.Background(), d, st, []guilds.FeedEntry{entry("c1", false)}, parsedFor(1))
	if len(st.FeedsPurged) != 1 || st.FeedsPurged[0] != "g1:c1" {
		t.Fatalf("FeedsPurged = %v", st.FeedsPurged)
	}
}

func TestFeedsPinned(t *testing.T) {
	tests := []struct {
		name  string
		feeds []guilds.FeedEntry
		step  []step
		runs  int
		pins  int
		sends int
	}{
		{"one message one pin", []guilds.FeedEntry{entry("c1", true)}, nil, 2, 1, 1},
		{"two pinned feeds same item", []guilds.FeedEntry{entry("c1", true), entry("c1", true)}, nil, 1, 1, 1},
		{"timeout after delivery pins the verified message once", []guilds.FeedEntry{entry("c1", true)}, []step{timeoutAfterDelivery()}, 2, 1, 1},
		{"timeout without delivery pins nothing", []guilds.FeedEntry{entry("c1", true)}, []step{timeoutWithoutDelivery()}, 1, 0, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d, st := newFakeDiscord(tc.step...), newFakeSentState()
			for i := 0; i < tc.runs; i++ {
				runFeeds(context.Background(), d, st, tc.feeds, parsedFor(1))
			}
			if len(d.Pinned) != tc.pins {
				t.Fatalf("pins = %v, want %d", d.Pinned, tc.pins)
			}
			if d.SendCalls != tc.sends {
				t.Fatalf("sends = %d, want %d", d.SendCalls, tc.sends)
			}
		})
	}
}
