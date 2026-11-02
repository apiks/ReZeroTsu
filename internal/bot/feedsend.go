package bot

import (
	"context"

	"ReZeroTsu/internal/discord"
	"ReZeroTsu/internal/logger"

	"github.com/bwmarrin/discordgo"
)

// feedSend applies the shared send rules (see sendrules.go) to a feed message, with the two differences feeds need:
//   - an Unknown outcome is verified by the embed author URL (feed embeds carry no top-level URL, so the shared
//     URL+description matcher would match unrelated bot messages);
//   - the feeds policy on a failed verification is the opposite of the default: the item is NOT delivered and is
//     retried on the next run (a rare duplicate post is better than a silently lost item).
//
// It also returns the delivered message ID when known (bot-account sends and verified messages), for pinning.
func feedSend(ctx context.Context, d DiscordPort, guildID, channelID string, webhook *WebhookTarget, msg OutgoingMessage) (SendResult, string) {
	if err := ctx.Err(); err != nil {
		return SendResult{Err: err}, ""
	}
	id, err := d.Send(ctx, channelID, webhook, msg)
	switch discord.ClassifySend(err) {
	case discord.Sent:
		return SendResult{Delivered: true}, id
	case discord.Unknown:
		return verifyFeedSend(ctx, d, channelID, webhook, msg, err)
	}

	// Rejected: nothing was delivered.
	if webhook != nil && discord.IsWebhookGone(err) {
		d.InvalidateChannelWebhook(guildID, channelID)
		if cerr := ctx.Err(); cerr != nil {
			return SendResult{Err: cerr}, ""
		}
		id, err = d.Send(ctx, channelID, nil, msg)
		switch discord.ClassifySend(err) {
		case discord.Sent:
			return SendResult{Delivered: true}, id
		case discord.Unknown:
			return verifyFeedSend(ctx, d, channelID, nil, msg, err)
		}
	}
	if !isKnownRejection(err) {
		logger.For("delivery").Warn("feed send rejected with unrecognised code, not retrying",
			append([]any{"guild_id", guildID, "channel_id", channelID}, discord.RESTAttrs(err)...)...)
	}
	return SendResult{Err: err}, ""
}

// verifyFeedSend decides whether an Unknown feed send landed. Verification failure means not delivered.
func verifyFeedSend(ctx context.Context, d DiscordPort, channelID string, webhook *WebhookTarget, msg OutgoingMessage, sendErr error) (SendResult, string) {
	vctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), verifyTimeout)
	defer cancel()
	recent, err := d.RecentMessages(vctx, channelID, verifyMessageLimit)
	if err != nil {
		logger.For("delivery").Warn("could not verify unknown feed send, treating as not delivered",
			"channel_id", channelID, "send_err", sendErr, "err", err)
		return SendResult{Err: sendErr}, ""
	}
	if id, ok := feedMessageInRecent(recent, d.BotUserID(), webhook, msg.Embeds); ok {
		return SendResult{Delivered: true}, id
	}
	return SendResult{Err: sendErr}, ""
}

// feedMessageInRecent finds a message by the bot (or the webhook used) that contains every wanted embed,
// matched on the embed author URL (author name when the item has no link).
func feedMessageInRecent(recent []ChannelMessage, botUserID string, webhook *WebhookTarget, want []*discordgo.MessageEmbed) (string, bool) {
	if len(want) == 0 {
		return "", false
	}
	for _, m := range recent {
		if m.AuthorID == "" || !(m.AuthorID == botUserID || (webhook != nil && m.AuthorID == webhook.ID)) {
			continue
		}
		if feedEmbedsMatch(m.Embeds, want) {
			return m.ID, true
		}
	}
	return "", false
}

func feedEmbedsMatch(have, want []*discordgo.MessageEmbed) bool {
	for _, w := range want {
		if w == nil || w.Author == nil {
			return false
		}
		found := false
		for _, h := range have {
			if h == nil || h.Author == nil {
				continue
			}
			if w.Author.URL != "" {
				found = h.Author.URL == w.Author.URL
			} else {
				found = h.Author.URL == "" && h.Author.Name == w.Author.Name
			}
			if found {
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
