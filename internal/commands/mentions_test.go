package commands

import (
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestChannelRoleUserMention(t *testing.T) {
	tests := []struct {
		name string
		fn   func(string) string
		id   string
		want string
	}{
		{"channel", ChannelMention, "123", "<#123>"},
		{"channel empty", ChannelMention, "", ""},
		{"role", RoleMention, "456", "<@&456>"},
		{"role empty", RoleMention, "", ""},
		{"user", UserMention, "789", "<@789>"},
		{"user empty", UserMention, "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.fn(tc.id); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCommandMention(t *testing.T) {
	t.Cleanup(func() { SetCommandIDs(nil) })
	SetCommandIDs([]*discordgo.ApplicationCommand{
		{ID: "11", Name: "raffle", Type: discordgo.ChatApplicationCommand},
		{ID: "22", Name: "ping"}, // zero type is a slash command
		{ID: "33", Name: "raffle", Type: discordgo.UserApplicationCommand},
		{ID: "", Name: "noid"},
		nil,
	})
	tests := []struct {
		name string
		path []string
		want string
	}{
		{"command", []string{"ping"}, "</ping:22>"},
		{"subcommand", []string{"raffle", "draw"}, "</raffle draw:11>"},
		{"group subcommand", []string{"raffle", "winners", "list"}, "</raffle winners list:11>"},
		{"unknown command", []string{"missing"}, "/missing"},
		{"unknown subcommand", []string{"missing", "sub"}, "/missing sub"},
		{"empty id", []string{"noid"}, "/noid"},
		{"no path", nil, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := CommandMention(tc.path...); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCommandMentionPlainBeforeIDsKnown(t *testing.T) {
	SetCommandIDs(nil)
	if got := CommandMention("ping"); got != "/ping" {
		t.Fatalf("got %q", got)
	}
}
