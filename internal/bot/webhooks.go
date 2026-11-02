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
			_, err := s.WebhookThreadExecute(w.ID, w.Token, false, threadID, params)
			return err
		}
		_, err := s.WebhookExecute(w.ID, w.Token, false, params)
		return err
	}
	_, err := s.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{
		Content: content,
		Embeds:  embeds,
	})
	return err
}

// resolveChannelWebhook returns a usable webhook for the channel (which may be a thread), or nil. It does the
// channel-state lookup, thread-parent resolution, permission checks and lists or creates the bot's webhook; it
// caches nothing (the channel sender's webhook cache does).
func resolveChannelWebhook(s *discordgo.Session, channelID string) *WebhookTarget {
	if s.State == nil || s.State.User == nil {
		return nil
	}
	ch, err := channelStateFirst(s, channelID)
	if err != nil {
		return nil
	}
	webhookChannelID := channelID
	threadID := ""
	isThread := ch.Type == discordgo.ChannelTypeGuildPublicThread || ch.Type == discordgo.ChannelTypeGuildPrivateThread
	if isThread && ch.ParentID != "" {
		webhookChannelID = ch.ParentID
		threadID = channelID
	}
	perms, err := s.State.UserChannelPermissions(s.State.User.ID, webhookChannelID)
	if err != nil {
		return nil
	}
	required := int64(discordgo.PermissionManageWebhooks | discordgo.PermissionViewChannel | discordgo.PermissionSendMessages | discordgo.PermissionEmbedLinks)
	if perms&required != required {
		return nil
	}
	if threadID != "" {
		threadPerms, err := s.State.UserChannelPermissions(s.State.User.ID, channelID)
		sendThreads := int64(discordgo.PermissionSendMessagesInThreads)
		if err != nil || threadPerms&sendThreads != sendThreads {
			return nil
		}
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
