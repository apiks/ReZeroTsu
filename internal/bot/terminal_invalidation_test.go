package bot

import (
	"context"
	"testing"

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
