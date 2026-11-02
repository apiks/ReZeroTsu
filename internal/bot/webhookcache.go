package bot

import (
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
)

// webhookAvoidTTL is how long a channel whose webhook Discord rejected (while the bot account could post)
// is sent to through the bot account only.
const webhookAvoidTTL = 15 * time.Minute

// clock supplies the current time. Production uses realClock; tests use a settable fake.
type clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

type cachedWebhook struct {
	webhook  *discordgo.Webhook
	threadID string
}

// webhookCache holds the resolved webhooks and the avoid entries of every shard, behind one lock. It is built once at
// startup and shared; it reads time only from its clock.
type webhookCache struct {
	clock clock

	mu         sync.RWMutex
	webhooks   map[string]cachedWebhook
	avoidUntil map[string]time.Time
}

func newWebhookCache(c clock) *webhookCache {
	return &webhookCache{
		clock:      c,
		webhooks:   make(map[string]cachedWebhook),
		avoidUntil: make(map[string]time.Time),
	}
}

func channelWebhookKey(guildID, channelID string) string {
	return guildID + ":" + channelID
}

// Avoided reports whether the channel's webhook is being avoided now. It reads under the read lock and takes the
// write lock only to forget an expired entry.
func (c *webhookCache) Avoided(guildID, channelID string) bool {
	key := channelWebhookKey(guildID, channelID)
	now := c.clock.Now()
	c.mu.RLock()
	until, ok := c.avoidUntil[key]
	c.mu.RUnlock()
	if !ok {
		return false
	}
	if now.Before(until) {
		return true
	}
	c.mu.Lock()
	// Re-check: the entry may have been renewed since the read.
	if until, ok := c.avoidUntil[key]; ok && !now.Before(until) {
		delete(c.avoidUntil, key)
	}
	c.mu.Unlock()
	return false
}

// Lookup returns the channel's cached webhook and thread ID, or nil.
func (c *webhookCache) Lookup(guildID, channelID string) (*discordgo.Webhook, string) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.webhooks[channelWebhookKey(guildID, channelID)]
	if !ok {
		return nil, ""
	}
	return e.webhook, e.threadID
}

// Store caches a resolved webhook for the channel.
func (c *webhookCache) Store(guildID, channelID string, w *discordgo.Webhook, threadID string) {
	c.mu.Lock()
	c.webhooks[channelWebhookKey(guildID, channelID)] = cachedWebhook{webhook: w, threadID: threadID}
	c.mu.Unlock()
}

// Invalidate drops the channel's cached webhook (e.g. the channel was deleted). An avoid entry is kept so a
// bot-account rejection does not un-avoid a rejected webhook; it expires by TTL. Avoid entries that have already
// expired are pruned for every channel, so entries of channels that are never looked up again do not pile up.
func (c *webhookCache) Invalidate(guildID, channelID string) {
	now := c.clock.Now()
	c.mu.Lock()
	delete(c.webhooks, channelWebhookKey(guildID, channelID))
	c.pruneExpiredLocked(now)
	c.mu.Unlock()
}

// Avoid drops the channel's cached webhook and makes Avoided report true for it until webhookAvoidTTL has passed.
// Re-resolving would usually find the same bot-owned webhook, which Discord would reject again; the bot account is
// used meanwhile. Expired avoid entries are pruned on the way.
func (c *webhookCache) Avoid(guildID, channelID string) {
	key := channelWebhookKey(guildID, channelID)
	now := c.clock.Now()
	c.mu.Lock()
	c.pruneExpiredLocked(now)
	delete(c.webhooks, key)
	c.avoidUntil[key] = now.Add(webhookAvoidTTL)
	c.mu.Unlock()
}

// PurgeGuild forgets every cached webhook and avoid entry of the guild.
func (c *webhookCache) PurgeGuild(guildID string) {
	prefix := guildID + ":"
	c.mu.Lock()
	for key := range c.webhooks {
		if strings.HasPrefix(key, prefix) {
			delete(c.webhooks, key)
		}
	}
	for key := range c.avoidUntil {
		if strings.HasPrefix(key, prefix) {
			delete(c.avoidUntil, key)
		}
	}
	c.mu.Unlock()
}

// pruneExpiredLocked forgets the avoid entries that have expired at now. The caller holds c.mu.
func (c *webhookCache) pruneExpiredLocked(now time.Time) {
	for k, until := range c.avoidUntil {
		if !now.Before(until) {
			delete(c.avoidUntil, k)
		}
	}
}
