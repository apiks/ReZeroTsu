package bot

import (
	"context"
	"time"

	"ReZeroTsu/internal/discord"
	"ReZeroTsu/internal/logger"

	"github.com/bwmarrin/discordgo"
)

// Shared send rules for every delivery run (guild new-episodes, feeds, user DMs, ...):
//   - nothing is sent once the context is cancelled, and a context error never triggers a fallback send;
//   - the webhook -> bot-account fallback runs only on a Rejected unknown/invalid webhook, after invalidating it;
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
}

// sendWithRules sends msg to channelID (through webhook when non-nil) applying the shared send rules.
// On a non-delivered result the caller maps Err to its own terminal handling (deleted channel, no permission, ...).
func sendWithRules(ctx context.Context, d DiscordPort, guildID, channelID string, webhook *WebhookTarget, msg OutgoingMessage) SendResult {
	if err := ctx.Err(); err != nil {
		return SendResult{Err: err}
	}
	_, err := d.Send(ctx, channelID, webhook, msg)
	switch discord.ClassifySend(err) {
	case discord.Sent:
		return SendResult{Delivered: true}
	case discord.Unknown:
		return verifyUnknownSend(ctx, d, channelID, webhook, msg, err)
	}

	// Rejected: nothing was delivered.
	if webhook != nil && discord.IsWebhookGone(err) {
		d.InvalidateChannelWebhook(guildID, channelID)
		if cerr := ctx.Err(); cerr != nil {
			return SendResult{Err: cerr}
		}
		_, err = d.Send(ctx, channelID, nil, msg)
		switch discord.ClassifySend(err) {
		case discord.Sent:
			return SendResult{Delivered: true}
		case discord.Unknown:
			return verifyUnknownSend(ctx, d, channelID, nil, msg, err)
		}
	}
	if !isKnownRejection(err) {
		logger.For("delivery").Warn("send rejected with unrecognised code, not retrying",
			append([]any{"guild_id", guildID, "channel_id", channelID}, discord.RESTAttrs(err)...)...)
	}
	return SendResult{Err: err}
}

// isKnownRejection reports whether err is a Rejected code that callers have terminal handling for.
func isKnownRejection(err error) bool {
	return discord.IsUnknownChannel(err) || discord.IsChannelUnavailable(err) || discord.IsChannelNotText(err) ||
		discord.IsMissingPermissions(err) || discord.IsUnknownRole(err) || discord.IsUnknownMessage(err) ||
		discord.IsCannotDMUser(err) || discord.IsWebhookGone(err)
}

// verifyUnknownSend reads recent channel messages to decide whether an Unknown send actually landed.
// It runs detached from ctx so that it still works when the send failed because of shutdown.
func verifyUnknownSend(ctx context.Context, d DiscordPort, channelID string, webhook *WebhookTarget, msg OutgoingMessage, sendErr error) SendResult {
	vctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), verifyTimeout)
	defer cancel()
	recent, err := d.RecentMessages(vctx, channelID, verifyMessageLimit)
	if err != nil {
		logger.For("delivery").Warn("could not verify unknown send, treating as delivered",
			"channel_id", channelID, "send_err", sendErr, "err", err)
		return SendResult{Delivered: true, Presumed: true}
	}
	if messageInRecent(recent, d.BotUserID(), webhook, msg) {
		return SendResult{Delivered: true}
	}
	return SendResult{Err: sendErr}
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
