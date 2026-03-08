package bot

import (
	"bytes"
	"context"
	"encoding/base64"
	"image/png"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
)

const webhookCreateThrottle = 150 * time.Millisecond

var (
	channelWebhookMu  sync.RWMutex
	channelWebhookMap = make(map[string]*struct {
		Webhook   *discordgo.Webhook
		ThreadID  string
		ChannelID string
	})
)

// SendBatchToChannel sends embeds to a channel (webhook or fallback); content e.g. for role ping on first batch.
func SendBatchToChannel(ctx context.Context, s *discordgo.Session, w *discordgo.Webhook, threadID, channelID, content string, embeds []*discordgo.MessageEmbed, useWebhook bool) error {
	if useWebhook && w != nil {
		if globalWebhookLimiter != nil {
			if err := globalWebhookLimiter.Acquire(ctx); err != nil {
				return err
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

func channelWebhookKey(guildID, channelID string) string {
	return guildID + ":" + channelID
}

// getOrCreateChannelWebhook returns webhook for guild+channel (channel may be thread). Invalidates cache if channel changed.
func getOrCreateChannelWebhook(s *discordgo.Session, guildID, channelID string) (*discordgo.Webhook, string, bool) {
	ch, err := s.Channel(channelID)
	if err != nil {
		ch, err = s.State.Channel(channelID)
	}
	if err != nil || ch == nil {
		return nil, "", false
	}
	if s.State.User == nil {
		return nil, "", false
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
		return nil, "", false
	}
	required := int64(discordgo.PermissionManageWebhooks | discordgo.PermissionViewChannel | discordgo.PermissionSendMessages | discordgo.PermissionEmbedLinks)
	if perms&required != required {
		return nil, "", false
	}
	if threadID != "" {
		threadPerms, err := s.State.UserChannelPermissions(s.State.User.ID, channelID)
		sendThreads := int64(discordgo.PermissionSendMessagesInThreads)
		if err != nil || threadPerms&sendThreads != sendThreads {
			return nil, "", false
		}
	}

	key := channelWebhookKey(guildID, channelID)
	channelWebhookMu.Lock()
	entry := channelWebhookMap[key]
	if entry != nil && entry.ChannelID != channelID {
		delete(channelWebhookMap, key)
		entry = nil
	}
	if entry != nil {
		w, tid := entry.Webhook, entry.ThreadID
		channelWebhookMu.Unlock()
		return w, tid, true
	}
	channelWebhookMu.Unlock()

	ws, err := s.ChannelWebhooks(webhookChannelID)
	if err != nil {
		return nil, "", false
	}
	for _, w := range ws {
		if w.User != nil && w.User.ID == s.State.User.ID && w.ChannelID == webhookChannelID {
			channelWebhookMu.Lock()
			channelWebhookMap[key] = &struct {
				Webhook   *discordgo.Webhook
				ThreadID  string
				ChannelID string
			}{Webhook: w, ThreadID: threadID, ChannelID: channelID}
			channelWebhookMu.Unlock()
			return w, threadID, true
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
		return nil, "", false
	}
	channelWebhookMu.Lock()
	channelWebhookMap[key] = &struct {
		Webhook   *discordgo.Webhook
		ThreadID  string
		ChannelID string
	}{Webhook: w, ThreadID: threadID, ChannelID: channelID}
	channelWebhookMu.Unlock()
	return w, threadID, true
}

func invalidateChannelWebhook(guildID, channelID string) {
	channelWebhookMu.Lock()
	delete(channelWebhookMap, channelWebhookKey(guildID, channelID))
	channelWebhookMu.Unlock()
}

func invalidateGuildWebhooks(guildID string) {
	prefix := guildID + ":"
	channelWebhookMu.Lock()
	for key := range channelWebhookMap {
		if strings.HasPrefix(key, prefix) {
			delete(channelWebhookMap, key)
		}
	}
	channelWebhookMu.Unlock()
}
