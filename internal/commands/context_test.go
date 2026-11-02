package commands

import "testing"

func TestContextRefusal(t *testing.T) {
	tests := []struct {
		name    string
		ctx     AllowedContext
		guildID string
		want    string
	}{
		{"dm-only in guild", ContextDMOnly, "123", "This command can only be used in DMs."},
		{"dm-only in dm", ContextDMOnly, "", ""},
		{"guild-only in dm", ContextGuildOnly, "", "This command can only be used in a server."},
		{"guild-only in guild", ContextGuildOnly, "123", ""},
		{"both in guild", ContextBoth, "123", ""},
		{"both in dm", ContextBoth, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := contextRefusal(tt.ctx, tt.guildID); got != tt.want {
				t.Errorf("contextRefusal(%v, %q) = %q, want %q", tt.ctx, tt.guildID, got, tt.want)
			}
		})
	}
}
