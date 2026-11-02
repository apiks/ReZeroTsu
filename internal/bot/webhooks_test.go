package bot

import (
	"testing"
	"time"
)

// resetWebhookAvoid empties the package-level avoid map for the test and afterwards.
func resetWebhookAvoid(t *testing.T) {
	t.Helper()
	reset := func() {
		channelWebhookMu.Lock()
		channelWebhookAvoidUntil = make(map[string]time.Time)
		channelWebhookMu.Unlock()
	}
	reset()
	t.Cleanup(reset)
}

// A bot-account rejection invalidates the channel; the avoided webhook must stay avoided.
func TestInvalidateKeepsAvoidedWebhookAvoided(t *testing.T) {
	resetWebhookAvoid(t)
	now := time.Now()
	avoidChannelWebhook("g1", "c1", now)
	invalidateChannelWebhook("g1", "c1")
	if !channelWebhookAvoided(channelWebhookKey("g1", "c1"), now.Add(time.Minute)) {
		t.Error("webhook no longer avoided after invalidate")
	}
}

func avoidEntryExists(key string) bool {
	channelWebhookMu.RLock()
	defer channelWebhookMu.RUnlock()
	_, ok := channelWebhookAvoidUntil[key]
	return ok
}

// The avoid map must not only grow: invalidating a channel (deleted-channel handling) forgets its avoid entry,
// and setting a new entry prunes the expired ones.
func TestChannelWebhookAvoidEntriesAreForgotten(t *testing.T) {
	t0 := time.Now()
	tests := []struct {
		name    string
		act     func()
		gone    []string
		present []string
	}{
		{
			name: "invalidating a channel keeps its avoid entry",
			act: func() {
				avoidChannelWebhook("g1", "c1", t0)
				avoidChannelWebhook("g1", "c2", t0)
				invalidateChannelWebhook("g1", "c1")
			},
			present: []string{channelWebhookKey("g1", "c1"), channelWebhookKey("g1", "c2")},
		},
		{
			name: "invalidating a guild clears its avoid entries",
			act: func() {
				avoidChannelWebhook("g1", "c1", t0)
				avoidChannelWebhook("g2", "c2", t0)
				invalidateGuildWebhooks("g1")
			},
			gone:    []string{channelWebhookKey("g1", "c1")},
			present: []string{channelWebhookKey("g2", "c2")},
		},
		{
			name: "a new avoid entry prunes expired ones",
			act: func() {
				avoidChannelWebhook("g1", "c1", t0)
				avoidChannelWebhook("g2", "c3", t0.Add(30*time.Minute))
				avoidChannelWebhook("g1", "c2", t0.Add(webhookAvoidTTL+time.Minute))
			},
			gone:    []string{channelWebhookKey("g1", "c1")},
			present: []string{channelWebhookKey("g2", "c3"), channelWebhookKey("g1", "c2")},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resetWebhookAvoid(t)
			tc.act()
			for _, k := range tc.gone {
				if avoidEntryExists(k) {
					t.Errorf("avoid entry %q still present", k)
				}
			}
			for _, k := range tc.present {
				if !avoidEntryExists(k) {
					t.Errorf("avoid entry %q missing", k)
				}
			}
		})
	}
}

// Invalidating any channel also forgets avoid entries that have already expired, so entries of channels that are
// never looked up again (deleted, autopost cleared) do not pile up. A live entry is untouched.
func TestInvalidatePrunesExpiredAvoidEntries(t *testing.T) {
	resetWebhookAvoid(t)
	expired, live := channelWebhookKey("g1", "c1"), channelWebhookKey("g1", "c2")
	now := time.Now()
	channelWebhookMu.Lock()
	channelWebhookAvoidUntil[expired] = now.Add(-time.Minute)
	channelWebhookAvoidUntil[live] = now.Add(webhookAvoidTTL)
	channelWebhookMu.Unlock()

	invalidateChannelWebhook("g1", "c9")

	if avoidEntryExists(expired) {
		t.Error("expired avoid entry survived an invalidate")
	}
	if !avoidEntryExists(live) {
		t.Error("live avoid entry was dropped by an invalidate")
	}
}
