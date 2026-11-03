package bot

import (
	"bytes"
	"context"
	"encoding/base64"
	"image/png"
	"time"

	"github.com/bwmarrin/discordgo"
)

const webhookCreateThrottle = 150 * time.Millisecond

// SendBatchToChannel sends embeds to a channel (webhook or fallback); content e.g. for role ping on first batch.
func SendBatchToChannel(ctx context.Context, s *discordgo.Session, w *discordgo.Webhook, threadID, channelID, content string, embeds []*discordgo.MessageEmbed, useWebhook bool) error {
	if useWebhook && w != nil {
		if globalWebhookLimiter != nil {
			if err := globalWebhookLimiter.Acquire(ctx); err != nil {
				// The wait ended before any request was made.
				return notAttempted(err)
			}
		}
		params := &discordgo.WebhookParams{Content: content, Embeds: embeds}
		if threadID != "" {
			_, err := s.WebhookThreadExecute(w.ID, w.Token, false, threadID, params, noLibraryRetry)
			return err
		}
		_, err := s.WebhookExecute(w.ID, w.Token, false, params, noLibraryRetry)
		return err
	}
	_, err := s.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{
		Content: content,
		Embeds:  embeds,
	}, noLibraryRetry)
	return err
}

// noLibraryRetry is the per-request option every delivery send carries. The pinned discordgo retries a 500, 503
// or 504 up to MaxRestRetries times inside one call, so a 504 on a message Discord already stored would post a
// duplicate that the send rules never see. With the retry off a send reaches Discord once and the send rules decide
// whether to resend. The option is per request, so command replies and other REST calls keep the library retry.
var noLibraryRetry = discordgo.WithRestRetries(0)

// webhookChannelFromState does the state-only checks behind webhook delivery: the channel is known, a thread
// resolves to its parent channel, and the bot has the permissions webhook delivery needs (View Channel, Send
// Messages, Embed Links and Manage Webhooks on the webhook channel, plus Send Messages in Threads for a thread). It
// returns the channel the webhook lives in and the thread ID (empty when the channel is not a thread). It makes no
// REST call.
func webhookChannelFromState(s *discordgo.Session, channelID string) (webhookChannelID, threadID string, ok bool) {
	if s.State == nil || s.State.User == nil {
		return "", "", false
	}
	ch, err := channelStateFirst(s, channelID)
	if err != nil {
		return "", "", false
	}
	webhookChannelID = channelID
	isThread := ch.Type == discordgo.ChannelTypeGuildPublicThread || ch.Type == discordgo.ChannelTypeGuildPrivateThread
	if isThread && ch.ParentID != "" {
		webhookChannelID = ch.ParentID
		threadID = channelID
	}
	perms, err := s.State.UserChannelPermissions(s.State.User.ID, webhookChannelID)
	if err != nil {
		return "", "", false
	}
	required := int64(discordgo.PermissionManageWebhooks | discordgo.PermissionViewChannel | discordgo.PermissionSendMessages | discordgo.PermissionEmbedLinks)
	if perms&required != required {
		return "", "", false
	}
	if threadID != "" {
		threadPerms, err := s.State.UserChannelPermissions(s.State.User.ID, channelID)
		sendThreads := int64(discordgo.PermissionSendMessagesInThreads)
		if err != nil || threadPerms&sendThreads != sendThreads {
			return "", "", false
		}
	}
	return webhookChannelID, threadID, true
}

// resolveChannelWebhook returns a usable webhook for the channel (which may be a thread), or nil. It does the
// channel-state lookup, thread-parent resolution, permission checks and lists or creates the bot's webhook; it
// caches nothing (the channel sender's webhook cache does).
func resolveChannelWebhook(s *discordgo.Session, channelID string) *WebhookTarget {
	webhookChannelID, threadID, ok := webhookChannelFromState(s, channelID)
	if !ok {
		return nil
	}

	ws, err := s.ChannelWebhooks(webhookChannelID)
	if err != nil {
		return nil
	}
	for _, w := range ws {
		if w.User != nil && w.User.ID == s.State.User.ID && w.ChannelID == webhookChannelID {
			return &WebhookTarget{ID: w.ID, Token: w.Token, ThreadID: threadID}
		}
	}
	time.Sleep(webhookCreateThrottle)
	avatar, err := s.UserAvatarDecode(s.State.User)
	if err != nil {
		avatar = nil
	}
	var avatarURL string
	if avatar != nil {
		var buf bytes.Buffer
		if png.Encode(&buf, avatar) == nil {
			avatarURL = "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
		}
	}
	username := s.State.User.Username
	if username == "" {
		username = "Bot"
	}
	w, err := s.WebhookCreate(webhookChannelID, username, avatarURL)
	if err != nil {
		return nil
	}
	return &WebhookTarget{ID: w.ID, Token: w.Token, ThreadID: threadID}
}
