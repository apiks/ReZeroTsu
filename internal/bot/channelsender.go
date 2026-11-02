package bot

import (
	"context"
	"time"

	"ReZeroTsu/internal/discord"
	"ReZeroTsu/internal/logger"
)

// The channel sender is the one way a delivery run posts its messages to a single channel or DM under the send
// rules (see GLOSSARY.md):
//   - nothing is sent once the context is cancelled, and a context error never triggers a fallback send; only a
//     Send that never made the request (errSendNotAttempted: a send-limiter wait ended by the context) skips
//     verification, while any other context error (e.g. an in-flight client timeout) stays Unknown;
//   - the webhook -> bot-account fallback runs at most once per post, only when the webhook send is Rejected as
//     an unknown/invalid webhook (which is invalidated first) or with a channel-terminal code (deleted,
//     inaccessible, non-text, missing permissions); when the bot account then delivers after a channel-terminal
//     rejection, the channel's webhook is avoided for a while; callers apply terminal handling to the
//     bot-account error, and the sender stops using a rejected webhook for the rest of a run;
//   - an Unknown outcome is verified by reading recent channel messages; what a failed verification means is the
//     policy's presumeOnVerifyFailure: new-episodes posts and DMs treat it as delivered, since a duplicate role
//     ping or DM is worse than a miss, while feeds and the daily schedule treat it as not delivered and post again.

// sendPolicy is how a channel sender matches and treats its messages.
type sendPolicy struct {
	// match finds the message an Unknown post produced among the channel's recent messages.
	match recentMatcher
	// presumeOnVerifyFailure counts a post as delivered (presumed) when verification itself fails.
	presumeOnVerifyFailure bool
	// label names the post in logs.
	label string
	// botAccountOnly posts through the bot account and never looks up a webhook.
	botAccountOnly bool
}

// newEpisodesPolicy is the policy for new-episodes posts: webhook or bot account, presume delivered.
func newEpisodesPolicy() sendPolicy {
	return sendPolicy{match: matchRecentMessage, presumeOnVerifyFailure: true, label: "send"}
}

// dmPolicy is the policy for DMs (episode DMs, reminders): bot account only, presume delivered.
func dmPolicy() sendPolicy {
	return sendPolicy{match: matchRecentMessage, presumeOnVerifyFailure: true, label: "send", botAccountOnly: true}
}

// feedPolicy is the policy for feed posts: webhook or bot account, a failed verification means not delivered.
func feedPolicy() sendPolicy {
	return sendPolicy{match: feedMessageInRecent, label: "feed send"}
}

// feedPinPolicy is the policy for a pinned feed item: bot account only (a message ID is needed to pin), a failed
// verification means not delivered.
func feedPinPolicy() sendPolicy {
	return sendPolicy{match: feedMessageInRecent, label: "feed send", botAccountOnly: true}
}

// schedulePolicy is the policy for the daily schedule posted at now: webhook or bot account, matching the
// schedule title posted on the current UTC day, a failed verification means not delivered.
func schedulePolicy(title string, now time.Time) sendPolicy {
	return sendPolicy{match: scheduleMatcher(title, now), label: "schedule send"}
}

// PostResult is the outcome of one channelSender.post.
type PostResult struct {
	// Delivered is true when the message is confirmed, or presumed (verification failed), delivered.
	Delivered bool
	// Presumed is true when Delivered was assumed because verification failed.
	Presumed bool
	// Err is the error that ended an undelivered post (a Rejected error, the Unknown error verified missing,
	// or the context error).
	Err error
	// MessageID is the delivered message's ID when known (bot-account sends and matched messages).
	MessageID string
}

// sendOutcome is a PostResult plus what only the channel sender needs.
type sendOutcome struct {
	PostResult
	// webhookRejected is true when Discord rejected the webhook send (gone, or a channel-terminal code) and the
	// bot account was tried instead, whatever the final outcome.
	webhookRejected bool
}

// channelSender posts a run's messages (chunks, retries) to one channel or DM under the send rules. It looks the
// channel's webhook up once (unless the policy is bot-account only) and stops using it for the rest of the run
// as soon as Discord rejected it, so no caller has to track that itself.
type channelSender struct {
	d                  DiscordPort
	guildID, channelID string
	webhook            *WebhookTarget
	policy             sendPolicy
}

// newChannelSender opens a channel sender for channelID (guildID is empty for a DM) under policy.
func newChannelSender(d DiscordPort, guildID, channelID string, policy sendPolicy) *channelSender {
	c := &channelSender{d: d, guildID: guildID, channelID: channelID, policy: policy}
	if !policy.botAccountOnly {
		c.webhook = d.ChannelWebhook(guildID, channelID)
	}
	return c
}

// post delivers msg.
func (c *channelSender) post(ctx context.Context, msg OutgoingMessage) PostResult {
	out := c.deliver(ctx, msg)
	if out.webhookRejected {
		c.webhook = nil
	}
	return out.PostResult
}

// deliver is the send structure: cancel check, never-attempted check, outcome classification, rejected-webhook
// fallback, Unknown verification and unrecognised-rejection logging.
func (c *channelSender) deliver(ctx context.Context, msg OutgoingMessage) sendOutcome {
	if err := ctx.Err(); err != nil {
		return sendOutcome{PostResult: PostResult{Err: err}}
	}
	webhook := c.webhook
	id, err := c.d.Send(ctx, c.channelID, webhook, msg)
	if sendNotAttempted(err) {
		return sendOutcome{PostResult: PostResult{Err: err}}
	}
	switch discord.ClassifySend(err) {
	case discord.Sent:
		return sendOutcome{PostResult: PostResult{Delivered: true, MessageID: id}}
	case discord.Unknown:
		return c.verify(ctx, webhook, msg, err)
	}

	// Rejected: nothing was delivered.
	if webhook != nil && (discord.IsWebhookGone(err) || discord.ClassifyChannelTerminal(err) != discord.ChannelNotTerminal) {
		// A gone webhook is invalidated. A channel-terminal rejection of the webhook does not prove the bot
		// account cannot post (e.g. missing access to a private thread for the webhook), so callers only get
		// to apply terminal handling when this single bot-account attempt is rejected too.
		gone := discord.IsWebhookGone(err)
		if gone {
			c.d.InvalidateChannelWebhook(c.guildID, c.channelID)
		}
		if cerr := ctx.Err(); cerr != nil {
			return sendOutcome{PostResult: PostResult{Err: cerr}, webhookRejected: true}
		}
		id, err = c.d.Send(ctx, c.channelID, nil, msg)
		if sendNotAttempted(err) {
			return sendOutcome{PostResult: PostResult{Err: err}, webhookRejected: true}
		}
		if outcome := discord.ClassifySend(err); outcome != discord.Rejected {
			res := sendOutcome{PostResult: PostResult{Delivered: true, MessageID: id}}
			if outcome == discord.Unknown {
				res = c.verify(ctx, nil, msg, err)
			}
			res.webhookRejected = true
			if res.Delivered && !res.Presumed && !gone {
				// The bot account can post where the webhook cannot: stop offering the webhook, or every later
				// run spends another rejected (401/403) request on it before falling back. A presumed delivery
				// proves nothing about the bot account, so it does not avoid the webhook.
				c.d.AvoidChannelWebhook(c.guildID, c.channelID)
			}
			return res
		}
		c.logUnrecognisedRejection(err)
		return sendOutcome{PostResult: PostResult{Err: err}, webhookRejected: true}
	}
	c.logUnrecognisedRejection(err)
	return sendOutcome{PostResult: PostResult{Err: err}}
}

// verify reads recent channel messages to decide whether an Unknown send actually landed. It runs detached from
// ctx so that it still works when the send failed because of shutdown. When the read itself fails the message is
// treated as delivered or not per the policy.
func (c *channelSender) verify(ctx context.Context, webhook *WebhookTarget, msg OutgoingMessage, sendErr error) sendOutcome {
	vctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), verifyTimeout)
	defer cancel()
	recent, err := c.d.RecentMessages(vctx, c.channelID, verifyMessageLimit)
	if err != nil {
		if c.policy.presumeOnVerifyFailure {
			logger.For("delivery").Warn("could not verify unknown "+c.policy.label+", treating as delivered",
				"channel_id", c.channelID, "send_err", sendErr, "err", err)
			return sendOutcome{PostResult: PostResult{Delivered: true, Presumed: true}}
		}
		logger.For("delivery").Warn("could not verify unknown "+c.policy.label+", treating as not delivered",
			"channel_id", c.channelID, "send_err", sendErr, "err", err)
		return sendOutcome{PostResult: PostResult{Err: sendErr}}
	}
	if id, ok := c.policy.match(recent, c.d.BotUserID(), webhook, msg); ok {
		return sendOutcome{PostResult: PostResult{Delivered: true, MessageID: id}}
	}
	return sendOutcome{PostResult: PostResult{Err: sendErr}}
}

// logUnrecognisedRejection warns about a Rejected error that callers have no terminal handling for.
func (c *channelSender) logUnrecognisedRejection(err error) {
	if !isKnownRejection(err) {
		logger.For("delivery").Warn(c.policy.label+" rejected with unrecognised code, not retrying",
			append([]any{"guild_id", c.guildID, "channel_id", c.channelID}, discord.RESTAttrs(err)...)...)
	}
}

// recentMatcher finds the message that an Unknown post produced among the channel's recent messages and
// returns its ID when known.
type recentMatcher func(recent []ChannelMessage, botUserID string, webhook *WebhookTarget, msg OutgoingMessage) (id string, ok bool)

// matchRecentMessage is the default matcher: embed URL and description (content for embed-less messages).
func matchRecentMessage(recent []ChannelMessage, botUserID string, webhook *WebhookTarget, msg OutgoingMessage) (string, bool) {
	return "", messageInRecent(recent, botUserID, webhook, msg)
}
