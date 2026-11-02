package bot

import (
	"sync"
	"testing"
	"time"
)

// fakeClock is a settable clock for the webhook cache tests; nothing sleeps.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{now: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func newTestWebhookCache() (*webhookCache, *fakeClock) {
	clk := newFakeClock()
	return newWebhookCache(clk), clk
}

// avoidCount reports how many avoid entries (live or expired) the cache still holds.
func (c *webhookCache) avoidCount() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.avoidUntil)
}

func storeHook(c *webhookCache, guildID, channelID string) {
	c.Store(guildID, channelID, &WebhookTarget{ID: "w-" + channelID, Token: "t"})
}

func cached(c *webhookCache, guildID, channelID string) bool {
	return c.Lookup(guildID, channelID) != nil
}

func TestWebhookCacheStoresAndLooksUp(t *testing.T) {
	c, _ := newTestWebhookCache()
	c.Store("g1", "c1", &WebhookTarget{ID: "w1", Token: "t", ThreadID: "thread"})
	w := c.Lookup("g1", "c1")
	if w == nil || w.ID != "w1" || w.ThreadID != "thread" {
		t.Fatalf("Lookup = %+v, want w1 in thread", w)
	}
	if cached(c, "g1", "c2") {
		t.Error("an unrelated channel has a cached webhook")
	}
}

func TestWebhookCacheAvoidExpiresAfterTTL(t *testing.T) {
	c, clk := newTestWebhookCache()
	storeHook(c, "g1", "c1")
	c.Avoid("g1", "c1")
	if !c.Avoided("g1", "c1") {
		t.Fatal("channel not avoided right after Avoid")
	}
	if cached(c, "g1", "c1") {
		t.Error("avoid kept the cached webhook")
	}
	clk.Advance(webhookAvoidTTL - time.Second)
	if !c.Avoided("g1", "c1") {
		t.Error("avoid ended before the TTL")
	}
	clk.Advance(time.Second)
	if c.Avoided("g1", "c1") {
		t.Error("avoid outlived the TTL")
	}
}

// A bot-account rejection invalidates the channel; the avoided webhook must stay avoided.
func TestWebhookCacheInvalidateDropsWebhookButKeepsAvoid(t *testing.T) {
	c, clk := newTestWebhookCache()
	storeHook(c, "g1", "c1")
	storeHook(c, "g1", "c2")
	c.Avoid("g1", "c1")
	c.Invalidate("g1", "c1")
	c.Invalidate("g1", "c2")
	clk.Advance(time.Minute)
	if !c.Avoided("g1", "c1") {
		t.Error("invalidate dropped the avoid entry")
	}
	if cached(c, "g1", "c2") {
		t.Error("invalidate kept the cached webhook")
	}
}

// Entries of channels never looked up again must not pile up: Invalidate and Avoid prune expired entries only.
func TestWebhookCachePrunesExpiredAvoidEntries(t *testing.T) {
	for _, tc := range []struct {
		name string
		act  func(c *webhookCache)
	}{
		{"invalidate", func(c *webhookCache) { c.Invalidate("g9", "c9") }},
		{"avoid", func(c *webhookCache) { c.Avoid("g9", "c9") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, clk := newTestWebhookCache()
			c.Avoid("g1", "old")
			clk.Advance(webhookAvoidTTL / 2)
			c.Avoid("g1", "live")
			clk.Advance(webhookAvoidTTL/2 + time.Second) // "old" expired, "live" not
			tc.act(c)
			if n := c.avoidCount(); n != map[string]int{"invalidate": 1, "avoid": 2}[tc.name] {
				t.Errorf("avoid entries = %d after %s", n, tc.name)
			}
			if !c.Avoided("g1", "live") {
				t.Error("live avoid entry was dropped")
			}
		})
	}
}

func TestWebhookCachePurgeGuildForgetsOnlyThatGuild(t *testing.T) {
	c, _ := newTestWebhookCache()
	storeHook(c, "g1", "c1")
	storeHook(c, "g2", "c2")
	c.Avoid("g1", "c3")
	c.Avoid("g2", "c4")
	c.PurgeGuild("g1")
	if cached(c, "g1", "c1") || c.Avoided("g1", "c3") {
		t.Error("guild g1 still has entries after the purge")
	}
	if !cached(c, "g2", "c2") || !c.Avoided("g2", "c4") {
		t.Error("the purge touched another guild")
	}
}

func TestWebhookCacheGuildIDPrefixDoesNotCollide(t *testing.T) {
	c, _ := newTestWebhookCache()
	storeHook(c, "g1", "c1")
	storeHook(c, "g11", "c1")
	c.PurgeGuild("g1")
	if !cached(c, "g11", "c1") {
		t.Error("purging g1 dropped g11")
	}
}
