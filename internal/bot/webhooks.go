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

const (
	webhookCreateThrottle = 150 * time.Millisecond
	// webhookAvoidTTL is how long a channel whose webhook Discord rejected (while the bot account could post)
	// is sent to through the bot account only.
	webhookAvoidTTL = time.Hour
)

var (
	// channelWebhookAvoidUntil maps a channel's webhook key to the time until which no webhook is offered for it.
	channelWebhookAvoidUntil = make(map[string]time.Time)

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

func channelWebhookKey(guildID, channelID string) string {
	return guildID + ":" + channelID
}

// getOrCreateChannelWebhook returns webhook for guild+channel (channel may be thread). Invalidates cache if channel changed.
func getOrCreateChannelWebhook(s *discordgo.Session, guildID, channelID string) (*discordgo.Webhook, string, bool) {
	if s.State == nil || s.State.User == nil {
		return nil, "", false
	}
	ch, err := channelStateFirst(s, channelID)
	if err != nil {
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
	if channelWebhookAvoided(key, time.Now()) {
		return nil, "", false
	}
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

// avoidChannelWebhook drops the channel's cached webhook and makes getOrCreateChannelWebhook offer none for it
// until webhookAvoidTTL has passed. Re-resolving would usually find the same bot-owned webhook, which Discord
// would reject again; the bot account is used meanwhile.
func avoidChannelWebhook(guildID, channelID string, now time.Time) {
	key := channelWebhookKey(guildID, channelID)
	channelWebhookMu.Lock()
	delete(channelWebhookMap, key)
	channelWebhookAvoidUntil[key] = now.Add(webhookAvoidTTL)
	channelWebhookMu.Unlock()
}

// channelWebhookAvoided reports whether the channel's webhook is being avoided at now, forgetting an expired entry.
func channelWebhookAvoided(key string, now time.Time) bool {
	channelWebhookMu.Lock()
	defer channelWebhookMu.Unlock()
	until, ok := channelWebhookAvoidUntil[key]
	if !ok {
		return false
	}
	if now.Before(until) {
		return true
	}
	delete(channelWebhookAvoidUntil, key)
	return false
}

func invalidateGuildWebhooks(guildID string) {
	prefix := guildID + ":"
	channelWebhookMu.Lock()
	for key := range channelWebhookMap {
		if strings.HasPrefix(key, prefix) {
			delete(channelWebhookMap, key)
		}
	}
	for key := range channelWebhookAvoidUntil {
		if strings.HasPrefix(key, prefix) {
			delete(channelWebhookAvoidUntil, key)
		}
	}
	channelWebhookMu.Unlock()
}
