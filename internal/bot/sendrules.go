package bot

import (
	"context"
	"errors"
	"fmt"
	"time"

	"ReZeroTsu/internal/discord"
	"ReZeroTsu/internal/logger"

	"github.com/bwmarrin/discordgo"
)

// Shared send rules for every delivery run (guild new-episodes, feeds, user DMs, ...):
//   - nothing is sent once the context is cancelled, and a context error never triggers a fallback send; only a
//     Send that never made the request (errSendNotAttempted: a send-limiter wait ended by the context) skips
//     verification, while any other context error (e.g. an in-flight client timeout) stays Unknown;
//   - the webhook -> bot-account fallback runs at most once per send, only when the webhook send is Rejected as
//     an unknown/invalid webhook (which is invalidated first) or with a channel-terminal code (deleted,
//     inaccessible, non-text, missing permissions); when the bot account then delivers after a channel-terminal
//     rejection, the channel's webhook is avoided for a while; callers apply terminal handling to the
//     bot-account error, and channelSender stops using a rejected webhook for the rest of a run;
//   - an Unknown outcome is verified by reading recent channel messages; what a failed verification means is the
//     caller's policy (presumeOnVerifyFailure): new-episodes posts and DMs (sendWithRules) treat it as delivered,
//     since a duplicate role ping or DM is worse than a miss, while feeds and the daily schedule treat it as not
//     delivered and post again;
//   - "sent" state is written on a context detached from shutdown with a short timeout.

const (
	// verifyMessageLimit is how many recent channel messages are read when verifying an Unknown send.
	verifyMessageLimit = 25
	// verifyTimeout bounds the verification read; it runs detached from shutdown cancellation.
	verifyTimeout = 10 * time.Second
	// sentWriteTimeout bounds "sent" state writes made through detachedWrite.
	sentWriteTimeout = 5 * time.Second
)

// SendResult is the outcome of sendWithRules.
type SendResult struct {
	// Delivered is true when the message is confirmed, or presumed (verification failed), delivered.
	Delivered bool
	// Err is the error that ended an undelivered send (a Rejected error, the Unknown error verified missing,
	// or the context error). Nil when Delivered unless Presumed.
	Err error
	// Presumed is true when Delivered was assumed because verification failed.
	Presumed bool
	// webhookRejected is true when Discord rejected the webhook send (gone, or a channel-terminal code) and the
	// bot account was tried instead, whatever the final outcome. channelSender stops using the webhook for later
	// sends of the run; across runs the port's invalidate/avoid calls keep it from being offered again.
	webhookRejected bool
}

// sendWithRules sends msg to channelID (through webhook when non-nil) applying the shared send rules.
// On a non-delivered result the caller maps Err to its own terminal handling (deleted channel, no permission, ...).
func sendWithRules(ctx context.Context, d DiscordPort, guildID, channelID string, webhook *WebhookTarget, msg OutgoingMessage) SendResult {
	res, _ := deliverWithRules(ctx, d, guildID, channelID, webhook, msg, matchRecentMessage, true, "send")
	return res
}

// channelSender sends a run's messages (chunks, retries) to one channel under the shared send rules. It looks
// the channel's webhook up once and stops using it for the rest of the run as soon as Discord rejected it, so
// no caller has to track that itself.
type channelSender struct {
	d                  DiscordPort
	guildID, channelID string
	webhook            *WebhookTarget
	match              recentMatcher
	presume            bool
	label              string
}

// newChannelSender returns a sender for channelID through its webhook when one is usable, with the default
// matcher and presume-on-verify-failure policy. Use the with* options for other policies.
func newChannelSender(d DiscordPort, guildID, channelID string) *channelSender {
	return &channelSender{
		d: d, guildID: guildID, channelID: channelID, webhook: d.ChannelWebhook(guildID, channelID),
		match: matchRecentMessage, presume: true, label: "send",
	}
}

// withPolicy sets how an Unknown send is matched, whether a failed verification counts as delivered and the
// log label.
func (c *channelSender) withPolicy(match recentMatcher, presumeOnVerifyFailure bool, label string) *channelSender {
	c.match, c.presume, c.label = match, presumeOnVerifyFailure, label
	return c
}

// send delivers msg and returns the result and the delivered message ID when known.
func (c *channelSender) send(ctx context.Context, msg OutgoingMessage) (SendResult, string) {
	res, id := deliverWithRules(ctx, c.d, c.guildID, c.channelID, c.webhook, msg, c.match, c.presume, c.label)
	if res.webhookRejected {
		c.webhook = nil
	}
	return res, id
}

// recentMatcher finds the message that an Unknown send produced among the channel's recent messages and
// returns its ID when known.
type recentMatcher func(recent []ChannelMessage, botUserID string, webhook *WebhookTarget, msg OutgoingMessage) (id string, ok bool)

// matchRecentMessage is the default matcher: embed URL and description (content for embed-less messages).
func matchRecentMessage(recent []ChannelMessage, botUserID string, webhook *WebhookTarget, msg OutgoingMessage) (string, bool) {
	return "", messageInRecent(recent, botUserID, webhook, msg)
}

// deliverWithRules is the shared send structure behind sendWithRules, feedSend and channelSender: cancel check,
// rejected-webhook fallback, Unknown classification and unrecognised-rejection logging. The callers differ only in how an Unknown
// send is matched (match), whether a failed verification counts as delivered (presumeOnVerifyFailure) and in the
// log label. It also returns the delivered message ID when known (bot-account sends and matched messages).
func deliverWithRules(ctx context.Context, d DiscordPort, guildID, channelID string, webhook *WebhookTarget, msg OutgoingMessage,
	match recentMatcher, presumeOnVerifyFailure bool, label string) (SendResult, string) {
	if err := ctx.Err(); err != nil {
		return SendResult{Err: err}, ""
	}
	id, err := d.Send(ctx, channelID, webhook, msg)
	if sendNotAttempted(err) {
		return SendResult{Err: err}, ""
	}
	switch discord.ClassifySend(err) {
	case discord.Sent:
		return SendResult{Delivered: true}, id
	case discord.Unknown:
		return verifyUnknownSend(ctx, d, channelID, webhook, msg, err, match, presumeOnVerifyFailure, label)
	}

	// Rejected: nothing was delivered.
	if webhook != nil && (discord.IsWebhookGone(err) || discord.ClassifyChannelTerminal(err) != discord.ChannelNotTerminal) {
		// A gone webhook is invalidated. A channel-terminal rejection of the webhook does not prove the bot
		// account cannot post (e.g. missing access to a private thread for the webhook), so callers only get
		// to apply terminal handling when this single bot-account attempt is rejected too.
		gone := discord.IsWebhookGone(err)
		if gone {
			d.InvalidateChannelWebhook(guildID, channelID)
		}
		if cerr := ctx.Err(); cerr != nil {
			return SendResult{Err: cerr, webhookRejected: true}, ""
		}
		id, err = d.Send(ctx, channelID, nil, msg)
		if sendNotAttempted(err) {
			return SendResult{Err: err, webhookRejected: true}, ""
		}
		if outcome := discord.ClassifySend(err); outcome != discord.Rejected {
			res := SendResult{Delivered: true}
			if outcome == discord.Unknown {
				res, id = verifyUnknownSend(ctx, d, channelID, nil, msg, err, match, presumeOnVerifyFailure, label)
			}
			res.webhookRejected = true
			if res.Delivered && !res.Presumed && !gone {
				// The bot account can post where the webhook cannot: stop offering the webhook, or every later
				// run spends another rejected (401/403) request on it before falling back. A presumed delivery
				// proves nothing about the bot account, so it does not avoid the webhook.
				d.AvoidChannelWebhook(guildID, channelID)
			}
			return res, id
		}
		logUnrecognisedRejection(label, guildID, channelID, err)
		return SendResult{Err: err, webhookRejected: true}, ""
	}
	logUnrecognisedRejection(label, guildID, channelID, err)
	return SendResult{Err: err}, ""
}

// logUnrecognisedRejection warns about a Rejected error that callers have no terminal handling for.
func logUnrecognisedRejection(label, guildID, channelID string, err error) {
	if !isKnownRejection(err) {
		logger.For("delivery").Warn(label+" rejected with unrecognised code, not retrying",
			append([]any{"guild_id", guildID, "channel_id", channelID}, discord.RESTAttrs(err)...)...)
	}
}

// errSendNotAttempted marks a Send error returned before any request was made (a send-limiter wait ended by
// the context). Only such errors skip verification; see sendNotAttempted.
var errSendNotAttempted = errors.New("send not attempted")

// notAttempted wraps err (a context error from a wait that precedes the request) as errSendNotAttempted while
// keeping it matchable as the original error.
func notAttempted(err error) error {
	return fmt.Errorf("%w: %w", errSendNotAttempted, err)
}

// sendNotAttempted reports whether a Send error says the request was never made: it is not verified and never
// falls back. Every other error, including a context error from an in-flight request's client timeout (which
// matches context.DeadlineExceeded even during shutdown), may hide a delivered message and stays Unknown.
func sendNotAttempted(err error) bool {
	return errors.Is(err, errSendNotAttempted)
}

// isKnownRejection reports whether err is a Rejected code that callers have terminal handling for.
// Channel-terminal codes come from ClassifyChannelTerminal, so a new terminal code only needs adding there.
func isKnownRejection(err error) bool {
	return discord.ClassifyChannelTerminal(err) != discord.ChannelNotTerminal ||
		discord.IsUnknownRole(err) || discord.IsUnknownMessage(err) || discord.IsCannotDMUser(err) || discord.IsWebhookGone(err)
}

// verifyUnknownSend reads recent channel messages to decide whether an Unknown send actually landed.
// It runs detached from ctx so that it still works when the send failed because of shutdown.
// When the read itself fails the message is treated as delivered or not per presumeOnVerifyFailure.
func verifyUnknownSend(ctx context.Context, d DiscordPort, channelID string, webhook *WebhookTarget, msg OutgoingMessage, sendErr error,
	match recentMatcher, presumeOnVerifyFailure bool, label string) (SendResult, string) {
	vctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), verifyTimeout)
	defer cancel()
	recent, err := d.RecentMessages(vctx, channelID, verifyMessageLimit)
	if err != nil {
		if presumeOnVerifyFailure {
			logger.For("delivery").Warn("could not verify unknown "+label+", treating as delivered",
				"channel_id", channelID, "send_err", sendErr, "err", err)
			return SendResult{Delivered: true, Presumed: true}, ""
		}
		logger.For("delivery").Warn("could not verify unknown "+label+", treating as not delivered",
			"channel_id", channelID, "send_err", sendErr, "err", err)
		return SendResult{Err: sendErr}, ""
	}
	if id, ok := match(recent, d.BotUserID(), webhook, msg); ok {
		return SendResult{Delivered: true}, id
	}
	return SendResult{Err: sendErr}, ""
}

// messageInRecent reports whether recent holds a message authored by the bot user (or the webhook used)
// whose embeds match every embed of msg by URL and description. Embed-less messages match on content.
func messageInRecent(recent []ChannelMessage, botUserID string, webhook *WebhookTarget, msg OutgoingMessage) bool {
	for _, m := range recent {
		if m.AuthorID == "" || !(m.AuthorID == botUserID || (webhook != nil && m.AuthorID == webhook.ID)) {
			continue
		}
		if len(msg.Embeds) == 0 {
			if m.Content == msg.Content {
				return true
			}
			continue
		}
		if embedsMatch(m.Embeds, msg.Embeds) {
			return true
		}
	}
	return false
}

func embedsMatch(have, want []*discordgo.MessageEmbed) bool {
	for _, w := range want {
		if w == nil {
			continue
		}
		found := false
		for _, h := range have {
			if h != nil && h.URL == w.URL && h.Description == w.Description {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// detachedWrite runs fn on a context detached from ctx's cancellation with a short timeout, so that "sent"
// state is still recorded during shutdown.
func detachedWrite(ctx context.Context, fn func(ctx context.Context) error) error {
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sentWriteTimeout)
	defer cancel()
	return fn(wctx)
}
