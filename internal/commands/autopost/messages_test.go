package autopost

import "testing"

func TestAutopostMessagesUseMentions(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want string
	}{
		{"new episodes set", newEpisodesMessage("New episodes autopost set to", "123", ""), "New episodes autopost set to <#123>."},
		{"new episodes with role", newEpisodesMessage("Current new episodes autopost channel:", "123", "456"), "Current new episodes autopost channel: <#123>. Role ping: <@&456>."},
		{"daily set", dailyMessage("Daily schedule autopost set to", "123"), "Daily schedule autopost set to <#123>."},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Fatalf("got %q, want %q", tc.got, tc.want)
			}
		})
	}
}
