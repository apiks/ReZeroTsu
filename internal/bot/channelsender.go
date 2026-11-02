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

// PostOutcome is what one channelSender.post concluded about its message. It differs from the discord package's
// SendOutcome, which classifies the answer to a single Discord call (sent, rejected or unknown): a post may make
// several calls (webhook, bot-account fallback, verification read) and reports the one conclusion that a delivery
// run switches on, including a shutdown that the single calls do not describe.
type PostOutcome int

const (
	// PostDelivered means the message is confirmed delivered.
	PostDelivered PostOutcome = iota + 1
	// PostPresumed means the message is treated as delivered because verifying an unknown outcome failed under a
	// policy that presumes delivery.
	PostPresumed
	// PostRejected means Discord refused the final attempt; nothing was delivered.
	PostRejected
	// PostMissing means an unknown outcome was verified as not delivered, or its verification failed under a
	// policy that does not presume delivery.
	PostMissing
	// PostCancelled means the post ended undelivered while shutdown was under way.
	PostCancelled
)

func (o PostOutcome) String() string {
	switch o {
	case PostDelivered:
		return "delivered"
	case PostPresumed:
		return "presumed"
	case PostRejected:
		return "rejected"
	case PostMissing:
		return "missing"
	case PostCancelled:
		return "cancelled"
	default:
		return "invalid"
	}
}

// PostResult is the result of one channelSender.post.
type PostResult struct {
	// Outcome is what the post concluded.
	Outcome PostOutcome
	// Terminal is why Discord's final rejection means the channel cannot be posted to; it is
	// discord.ChannelNotTerminal unless Outcome is PostRejected.
	Terminal discord.ChannelTerminalReason
	// Err is the error that ended an undelivered post (the rejection, the unknown error verified missing, or the
	// context error), kept for logging.
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
	d                  RawDiscord
	cache              *webhookCache
	guildID, channelID string
	webhook            *WebhookTarget
	policy             sendPolicy
}

// channelSenderFactory opens channel senders over one shard's raw Discord adapter and the webhook cache shared by
// all shards. It is built once per shard. The cache owns time (it holds the injected clock), so the factory has none.
type channelSenderFactory struct {
	raw   RawDiscord
	cache *webhookCache
}

func newChannelSenderFactory(raw RawDiscord, cache *webhookCache) *channelSenderFactory {
	return &channelSenderFactory{raw: raw, cache: cache}
}

// open opens a channel sender for channelID (guildID is empty for a DM) under policy.
func (f *channelSenderFactory) open(guildID, channelID string, policy sendPolicy) *channelSender {
	return newChannelSender(f.raw, f.cache, guildID, channelID, policy)
}

// newChannelSender opens a channel sender for channelID (guildID is empty for a DM) under policy. Unless the policy
// is bot-account only it picks the channel's webhook once: none for an avoided channel, else the cached webhook,
// else one resolved through the raw port (and cached).
func newChannelSender(d RawDiscord, cache *webhookCache, guildID, channelID string, policy sendPolicy) *channelSender {
	c := &channelSender{d: d, cache: cache, guildID: guildID, channelID: channelID, policy: policy}
	if !policy.botAccountOnly {
		c.webhook = c.selectWebhook()
	}
	return c
}

// selectWebhook returns the webhook to try for the channel, or nil.
func (c *channelSender) selectWebhook() *WebhookTarget {
	if c.cache.Avoided(c.guildID, c.channelID) {
		return nil
	}
	if w := c.cache.Lookup(c.guildID, c.channelID); w != nil {
		// A webhook send keeps working after the bot lost its permissions, so check the state before using the
		// cached webhook. The entry stays cached; the bot account posts meanwhile.
		if !c.d.WebhookUsable(c.channelID) {
			return nil
		}
		return w
	}
	w := c.d.ResolveWebhook(c.guildID, c.channelID)
	if w != nil {
		c.cache.Store(c.guildID, c.channelID, w)
	}
	return w
}

// post delivers msg.
func (c *channelSender) post(ctx context.Context, msg OutgoingMessage) PostResult {
	out := c.deliver(ctx, msg)
	if out.webhookRejected {
		c.webhook = nil
	}
	if out.Outcome == PostRejected && out.Terminal != discord.ChannelNotTerminal {
		// The final attempt was rejected as channel-terminal: drop the channel's cached webhook so the next run
		// resolves it again (the permission check then fails before any REST call).
		c.cache.Invalidate(c.guildID, c.channelID)
	}
	if (out.Outcome == PostRejected || out.Outcome == PostMissing) && ctx.Err() != nil {
		// Undelivered while shutdown is under way, whatever the underlying error: no run handles it as a failure.
		out.PostResult = PostResult{Outcome: PostCancelled, Err: out.Err}
	}
	return out.PostResult
}

// rejectedResult is the result of a post whose final attempt Discord refused with err.
func rejectedResult(err error) PostResult {
	return PostResult{Outcome: PostRejected, Terminal: discord.ClassifyChannelTerminal(err), Err: err}
}

// deliver makes one post: cancel check, never-attempted check, outcome classification, rejected-webhook
// fallback, Unknown verification and unrecognised-rejection logging.
func (c *channelSender) deliver(ctx context.Context, msg OutgoingMessage) sendOutcome {
	if err := ctx.Err(); err != nil {
		return sendOutcome{PostResult: PostResult{Outcome: PostCancelled, Err: err}}
	}
	webhook := c.webhook
	id, err := c.d.Send(ctx, c.channelID, webhook, msg)
	if sendNotAttempted(err) {
		return sendOutcome{PostResult: PostResult{Outcome: PostMissing, Err: err}}
	}
	switch discord.ClassifySend(err) {
	case discord.Sent:
		return sendOutcome{PostResult: PostResult{Outcome: PostDelivered, MessageID: id}}
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
			c.cache.Invalidate(c.guildID, c.channelID)
		}
		if cerr := ctx.Err(); cerr != nil {
			return sendOutcome{PostResult: PostResult{Outcome: PostCancelled, Err: cerr}, webhookRejected: true}
		}
		id, err = c.d.Send(ctx, c.channelID, nil, msg)
		if sendNotAttempted(err) {
			return sendOutcome{PostResult: PostResult{Outcome: PostMissing, Err: err}, webhookRejected: true}
		}
		if outcome := discord.ClassifySend(err); outcome != discord.Rejected {
			res := sendOutcome{PostResult: PostResult{Outcome: PostDelivered, MessageID: id}}
			if outcome == discord.Unknown {
				res = c.verify(ctx, nil, msg, err)
			}
			res.webhookRejected = true
			if res.Outcome == PostDelivered && !gone {
				// The bot account can post where the webhook cannot: stop offering the webhook, or every later
				// run spends another rejected (401/403) request on it before falling back. A presumed delivery
				// proves nothing about the bot account, so it does not avoid the webhook.
				c.cache.Avoid(c.guildID, c.channelID)
			}
			return res
		}
		c.logUnrecognisedRejection(err)
		return sendOutcome{PostResult: rejectedResult(err), webhookRejected: true}
	}
	c.logUnrecognisedRejection(err)
	return sendOutcome{PostResult: rejectedResult(err)}
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
			return sendOutcome{PostResult: PostResult{Outcome: PostPresumed}}
		}
		logger.For("delivery").Warn("could not verify unknown "+c.policy.label+", treating as not delivered",
			"channel_id", c.channelID, "send_err", sendErr, "err", err)
		return sendOutcome{PostResult: PostResult{Outcome: PostMissing, Err: sendErr}}
	}
	if id, ok := c.policy.match(recent, c.d.BotUserID(), webhook, msg); ok {
		return sendOutcome{PostResult: PostResult{Outcome: PostDelivered, MessageID: id}}
	}
	return sendOutcome{PostResult: PostResult{Outcome: PostMissing, Err: sendErr}}
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
