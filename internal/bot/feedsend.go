package bot

import (
	"context"

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
	return deliverWithRules(ctx, d, guildID, channelID, webhook, msg, feedMessageInRecent, false, "feed send")
}

// feedMessageInRecent finds a message by the bot (or the webhook used) that contains every wanted embed,
// matched on the embed author URL (author name when the item has no link).
func feedMessageInRecent(recent []ChannelMessage, botUserID string, webhook *WebhookTarget, msg OutgoingMessage) (string, bool) {
	want := msg.Embeds
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
