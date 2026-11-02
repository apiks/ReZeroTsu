package bot

import (
	"context"
	"errors"
	"time"

	"ReZeroTsu/internal/discord"
	"ReZeroTsu/internal/logger"

	"github.com/bwmarrin/discordgo"
)

// Shared send rules for every delivery run (guild new-episodes, feeds, user DMs, ...):
//   - nothing is sent once the context is cancelled, and a context error never triggers a fallback send; a
//     context error returned by Send once the context is done means the request was never made (not verified);
//   - the webhook -> bot-account fallback runs at most once per send, only when the webhook send is Rejected as
//     an unknown/invalid webhook (which is invalidated first) or with a channel-terminal code (deleted,
//     inaccessible, non-text, missing permissions); callers apply terminal handling to the bot-account error;
//   - an Unknown outcome is verified by reading recent channel messages; if verification itself fails the
//     message is treated as delivered (a duplicate role ping is worse than a miss);
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
	// WebhookGone is true when the webhook turned out to be unusable, whatever the final outcome: gone (and
	// invalidated), or rejected with a channel-terminal code so the bot account was tried instead. Callers
	// sending further chunks to the same channel must stop passing that webhook.
	WebhookGone bool
}

// sendWithRules sends msg to channelID (through webhook when non-nil) applying the shared send rules.
// On a non-delivered result the caller maps Err to its own terminal handling (deleted channel, no permission, ...).
func sendWithRules(ctx context.Context, d DiscordPort, guildID, channelID string, webhook *WebhookTarget, msg OutgoingMessage) SendResult {
	res, _ := deliverWithRules(ctx, d, guildID, channelID, webhook, msg, matchRecentMessage, true, "send")
	return res
}

// recentMatcher finds the message that an Unknown send produced among the channel's recent messages and
// returns its ID when known.
type recentMatcher func(recent []ChannelMessage, botUserID string, webhook *WebhookTarget, msg OutgoingMessage) (id string, ok bool)

// matchRecentMessage is the default matcher: embed URL and description (content for embed-less messages).
func matchRecentMessage(recent []ChannelMessage, botUserID string, webhook *WebhookTarget, msg OutgoingMessage) (string, bool) {
	return "", messageInRecent(recent, botUserID, webhook, msg)
}

// deliverWithRules is the shared send structure behind sendWithRules and feedSend: cancel check, webhook-gone
// fallback, Unknown classification and unrecognised-rejection logging. The callers differ only in how an Unknown
// send is matched (match), whether a failed verification counts as delivered (presumeOnVerifyFailure) and in the
// log label. It also returns the delivered message ID when known (bot-account sends and matched messages).
func deliverWithRules(ctx context.Context, d DiscordPort, guildID, channelID string, webhook *WebhookTarget, msg OutgoingMessage,
	match recentMatcher, presumeOnVerifyFailure bool, label string) (SendResult, string) {
	if err := ctx.Err(); err != nil {
		return SendResult{Err: err}, ""
	}
	id, err := d.Send(ctx, channelID, webhook, msg)
	if sendNotAttempted(ctx, err) {
		return SendResult{Err: err}, ""
	}
	switch discord.ClassifySend(err) {
	case discord.Sent:
		return SendResult{Delivered: true}, id
	case discord.Unknown:
		return verifyUnknownSend(ctx, d, channelID, webhook, msg, err, match, presumeOnVerifyFailure, label)
	}

	// Rejected: nothing was delivered.
	gone := false
	if webhook != nil && (discord.IsWebhookGone(err) || discord.ClassifyChannelTerminal(err) != discord.ChannelNotTerminal) {
		// A gone webhook is invalidated. A channel-terminal rejection of the webhook does not prove the bot
		// account cannot post (e.g. missing access to a private thread for the webhook), so callers only get
		// to apply terminal handling when this single bot-account attempt is rejected too.
		gone = true
		if discord.IsWebhookGone(err) {
			d.InvalidateChannelWebhook(guildID, channelID)
		}
		if cerr := ctx.Err(); cerr != nil {
			return SendResult{Err: cerr, WebhookGone: true}, ""
		}
		id, err = d.Send(ctx, channelID, nil, msg)
		if sendNotAttempted(ctx, err) {
			return SendResult{Err: err, WebhookGone: true}, ""
		}
		switch discord.ClassifySend(err) {
		case discord.Sent:
			return SendResult{Delivered: true, WebhookGone: true}, id
		case discord.Unknown:
			res, vid := verifyUnknownSend(ctx, d, channelID, nil, msg, err, match, presumeOnVerifyFailure, label)
			res.WebhookGone = true
			return res, vid
		}
	}
	if !isKnownRejection(err) {
		logger.For("delivery").Warn(label+" rejected with unrecognised code, not retrying",
			append([]any{"guild_id", guildID, "channel_id", channelID}, discord.RESTAttrs(err)...)...)
	}
	return SendResult{Err: err, WebhookGone: gone}, ""
}

// sendNotAttempted reports whether a Send error is the run context's own cancellation or deadline. Production
// Send only observes ctx while waiting for a send limiter (the REST call itself is not context-aware), so such
// an error means the request was never made: it is not verified and never falls back. A context error while ctx
// is still live comes from a client-side request timeout, which may hide a delivered message, so it stays Unknown.
func sendNotAttempted(ctx context.Context, err error) bool {
	if ctx.Err() == nil {
		return false
	}
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// isKnownRejection reports whether err is a Rejected code that callers have terminal handling for.
func isKnownRejection(err error) bool {
	return discord.IsUnknownChannel(err) || discord.IsChannelUnavailable(err) || discord.IsChannelNotText(err) ||
		discord.IsMissingPermissions(err) || discord.IsUnknownRole(err) || discord.IsUnknownMessage(err) ||
		discord.IsCannotDMUser(err) || discord.IsWebhookGone(err)
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
