package bot

import (
	"context"
	"errors"
	"testing"

	"ReZeroTsu/internal/discord"

	"github.com/bwmarrin/discordgo"
)

// cancelDuringSend runs a Send through the wrapped fake, then ends the run's context as the call returns: the
// shutdown arrives while the request is in flight, whatever the request answered.
type cancelDuringSend struct {
	*fakeDiscord
	cancel context.CancelFunc
}

func (c cancelDuringSend) Send(ctx context.Context, channelID string, webhook *WebhookTarget, msg OutgoingMessage) (string, error) {
	id, err := c.fakeDiscord.Send(ctx, channelID, webhook, msg)
	c.cancel()
	return id, err
}

func TestChannelSenderPostOutcome(t *testing.T) {
	tests := []struct {
		name         string
		policy       sendPolicy
		script       []step
		recentErr    error
		wantOutcome  PostOutcome
		wantTerminal discord.ChannelTerminalReason
		wantID       string
		wantErr      bool
	}{
		{name: "confirmed send is delivered", policy: newEpisodesPolicy(), wantOutcome: PostDelivered, wantID: "msg-1"},
		{name: "unknown outcome verified as landed is delivered", policy: newEpisodesPolicy(), script: []step{timeoutAfterDelivery()}, wantOutcome: PostDelivered, wantErr: false},
		{name: "unknown outcome verified as not landed is missing", policy: newEpisodesPolicy(), script: []step{timeoutWithoutDelivery()}, wantOutcome: PostMissing, wantErr: true},
		{name: "failed verification under a presuming policy is presumed", policy: newEpisodesPolicy(), script: []step{timeoutAfterDelivery()}, recentErr: errors.New("boom"), wantOutcome: PostPresumed},
		{name: "failed verification under a non-presuming policy is missing", policy: feedPolicy(), script: []step{timeoutAfterDelivery()}, recentErr: errors.New("boom"), wantOutcome: PostMissing, wantErr: true},
		{name: "deleted channel is rejected with its reason", policy: newEpisodesPolicy(), script: []step{rejectWith(discordgo.ErrCodeUnknownChannel)}, wantOutcome: PostRejected, wantTerminal: discord.ChannelDeleted, wantErr: true},
		{name: "missing permissions is rejected with its reason", policy: newEpisodesPolicy(), script: []step{rejectWith(discordgo.ErrCodeMissingPermissions)}, wantOutcome: PostRejected, wantTerminal: discord.ChannelMissingPermissions, wantErr: true},
		{name: "unrecognised rejection has no terminal reason", policy: newEpisodesPolicy(), script: []step{rejectWith(discordgo.ErrCodeInvalidFormBody)}, wantOutcome: PostRejected, wantTerminal: discord.ChannelNotTerminal, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := newFakeDiscord(tc.script...)
			d.RecentErr = tc.recentErr
			res := newTestSenders(d).open("g1", "c1", tc.policy).post(context.Background(), OutgoingMessage{Content: "hello"})
			if res.Outcome != tc.wantOutcome {
				t.Errorf("outcome = %v, want %v", res.Outcome, tc.wantOutcome)
			}
			if res.Terminal != tc.wantTerminal {
				t.Errorf("terminal = %v, want %v", res.Terminal, tc.wantTerminal)
			}
			if tc.wantID != "" && res.MessageID != tc.wantID {
				t.Errorf("message ID = %q, want %q", res.MessageID, tc.wantID)
			}
			if (res.Err != nil) != tc.wantErr {
				t.Errorf("err = %v, want error: %v", res.Err, tc.wantErr)
			}
		})
	}
}

// A rejected webhook whose bot-account fallback is also rejected reports the final attempt's reason.
func TestChannelSenderPostRejectedFallbackReportsFinalReason(t *testing.T) {
	d := newFakeDiscord(rejectWith(discordgo.ErrCodeMissingAccess), rejectWith(discordgo.ErrCodeUnknownChannel))
	d.Webhook = testWebhook
	res := newTestSenders(d).open("g1", "c1", newEpisodesPolicy()).post(context.Background(), OutgoingMessage{Content: "hello"})
	if res.Outcome != PostRejected || res.Terminal != discord.ChannelDeleted {
		t.Fatalf("outcome = %v, terminal = %v, want rejected, channel deleted", res.Outcome, res.Terminal)
	}
}

// An undelivered post that ends with the context done reports cancelled, whatever the underlying error.
func TestChannelSenderPostUndeliveredDuringShutdownIsCancelled(t *testing.T) {
	tests := []struct {
		name   string
		policy sendPolicy
		step   step
	}{
		{"request never made", newEpisodesPolicy(), blockUntilCancelled()},
		{"rejected as channel-terminal", newEpisodesPolicy(), rejectWith(discordgo.ErrCodeUnknownChannel)},
		{"rejected with an unrecognised code", feedPolicy(), rejectWith(discordgo.ErrCodeInvalidFormBody)},
		{"unknown outcome verified as not landed", feedPolicy(), timeoutWithoutDelivery()},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			d := newFakeDiscord(tc.step)
			d.OnBlock = cancel
			raw := cancelDuringSend{fakeDiscord: d, cancel: cancel}
			res := newTestSenders(raw).open("g1", "c1", tc.policy).post(ctx, OutgoingMessage{Content: "hello"})
			if res.Outcome != PostCancelled {
				t.Errorf("outcome = %v, want cancelled", res.Outcome)
			}
			if res.Terminal != discord.ChannelNotTerminal {
				t.Errorf("terminal = %v, want none (only a rejected post carries one)", res.Terminal)
			}
		})
	}
}

// A post that was delivered stays delivered even when shutdown starts while it is in flight.
func TestChannelSenderPostDeliveredDuringShutdownStaysDelivered(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	raw := cancelDuringSend{fakeDiscord: newFakeDiscord(), cancel: cancel}
	res := newTestSenders(raw).open("g1", "c1", newEpisodesPolicy()).post(ctx, OutgoingMessage{Content: "hello"})
	if res.Outcome != PostDelivered {
		t.Fatalf("outcome = %v, want delivered", res.Outcome)
	}
}

// A post that is undelivered during shutdown applies no terminal handling in the new-episodes run, even when
// Discord's answer was a channel-terminal rejection.
func TestGuildNewEpisodesCancelledPostAppliesNoTerminalHandling(t *testing.T) {
	h := newGuildHarness(1, rejectWith(discordgo.ErrCodeUnknownChannel))
	ctx, cancel := context.WithCancel(context.Background())
	raw := cancelDuringSend{fakeDiscord: h.d, cancel: cancel}
	processGuildNewEpisodes(ctx, raw, newTestSenders(raw), h.st, "g1", h.fn, h.now)
	if h.d.SendCalls != 1 {
		t.Fatalf("send calls = %d, want 1", h.d.SendCalls)
	}
	if h.st.Autoposts[apKey("g1", postTypeNewEpisodes)] == nil {
		t.Error("autopost cleared during shutdown")
	}
	if h.st.AnimeSubs["c1"] == nil {
		t.Error("channel's sent state dropped during shutdown")
	}
}
