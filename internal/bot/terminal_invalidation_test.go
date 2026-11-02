package bot

import (
	"context"
	"testing"

	"ReZeroTsu/internal/database/guilds"
	"ReZeroTsu/internal/discord"
)

// A final attempt rejected as channel-terminal (any of the four reasons) drops the channel's cached webhook, so the
// next run resolves the webhook again; a non-terminal rejection keeps the cache.

var terminalReasonCodes = []struct {
	name string
	code int
}{
	{"deleted", discord.UnknownChannelCode},
	{"inaccessible", discord.MissingAccessCode},
	{"non-text", discord.ChannelNotTextCode},
	{"missing permissions", discord.MissingPermissionsCode},
}

func TestTerminalRejectionResolvesWebhookAgainNewEpisodes(t *testing.T) {
	for _, tc := range terminalReasonCodes {
		t.Run(tc.name, func(t *testing.T) {
			h := newGuildHarness(1, rejectWith(tc.code), rejectWith(tc.code))
			h.d.Webhook = testWebhook
			doc, ap := h.st.AnimeSubs["c1"], h.st.Autoposts[apKey("g1", postTypeNewEpisodes)]
			h.run(context.Background())
			if h.d.ResolveCalls != 1 {
				t.Fatalf("resolve calls after run 1 = %d, want 1", h.d.ResolveCalls)
			}
			// The terminal handling drops the channel's sent state and autopost; put them back for the next run.
			h.st.AnimeSubs["c1"] = doc
			h.st.Autoposts[apKey("g1", postTypeNewEpisodes)] = ap
			h.markAllDue()
			h.run(context.Background())
			if h.d.ResolveCalls != 2 {
				t.Errorf("resolve calls after run 2 = %d, want 2 (re-resolved)", h.d.ResolveCalls)
			}
		})
	}
}

func TestNonTerminalRejectionKeepsWebhookNewEpisodes(t *testing.T) {
	h := newGuildHarness(1, rejectWith(99999))
	h.d.Webhook = testWebhook
	h.run(context.Background())
	h.markAllDue()
	h.run(context.Background())
	if h.d.ResolveCalls != 1 {
		t.Errorf("resolve calls = %d, want 1 (cached webhook kept)", h.d.ResolveCalls)
	}
}

func TestTerminalRejectionResolvesWebhookAgainFeeds(t *testing.T) {
	for _, tc := range terminalReasonCodes {
		t.Run(tc.name, func(t *testing.T) {
			d, st := newFakeDiscord(rejectWith(tc.code), rejectWith(tc.code)), newFakeSentState()
			d.Webhook = testWebhook
			senders := newTestSenders(d)
			feeds := []guilds.FeedEntry{entry("c1", false)}
			processGuildFeeds(context.Background(), d, senders, st, "g1", feeds, parsedFor(1))
			if d.ResolveCalls != 1 {
				t.Fatalf("resolve calls after run 1 = %d, want 1", d.ResolveCalls)
			}
			processGuildFeeds(context.Background(), d, senders, st, "g1", feeds, parsedFor(1))
			if d.ResolveCalls != 2 {
				t.Errorf("resolve calls after run 2 = %d, want 2 (re-resolved)", d.ResolveCalls)
			}
		})
	}
}

func TestNonTerminalRejectionKeepsWebhookFeeds(t *testing.T) {
	d, st := newFakeDiscord(rejectWith(99999)), newFakeSentState()
	d.Webhook = testWebhook
	senders := newTestSenders(d)
	feeds := []guilds.FeedEntry{entry("c1", false)}
	processGuildFeeds(context.Background(), d, senders, st, "g1", feeds, parsedFor(1))
	processGuildFeeds(context.Background(), d, senders, st, "g1", feeds, parsedFor(1))
	if d.ResolveCalls != 1 {
		t.Errorf("resolve calls = %d, want 1 (cached webhook kept)", d.ResolveCalls)
	}
}
