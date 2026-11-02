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

func avoidEntryExists(key string) bool {
	channelWebhookMu.RLock()
	defer channelWebhookMu.RUnlock()
	_, ok := channelWebhookAvoidUntil[key]
	return ok
}

// The avoid map must not only grow: invalidating a channel (deleted-channel handling) forgets its avoid entry,
// and setting a new entry prunes the expired ones.
func TestChannelWebhookAvoidEntriesAreForgotten(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		act     func()
		gone    []string
		present []string
	}{
		{
			name: "invalidating a channel clears its avoid entry",
			act: func() {
				avoidChannelWebhook("g1", "c1", t0)
				avoidChannelWebhook("g1", "c2", t0)
				invalidateChannelWebhook("g1", "c1")
			},
			gone:    []string{channelWebhookKey("g1", "c1")},
			present: []string{channelWebhookKey("g1", "c2")},
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
