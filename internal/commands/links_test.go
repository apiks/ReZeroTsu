package commands

import (
	"errors"
	"testing"

	"ReZeroTsu/internal/discord"

	"github.com/bwmarrin/discordgo"
)

func TestMessageLink(t *testing.T) {
	if got := MessageLink("g", "c", "m"); got != "https://discord.com/channels/g/c/m" {
		t.Errorf("got %q", got)
	}
}

func TestTruncateRunes(t *testing.T) {
	for _, tc := range []struct {
		in    string
		limit int
		want  string
	}{
		{"abc", 3, "abc"},
		{"abcd", 3, "ab…"},
		{"héllo", 3, "hé…"},
		{"abc", 0, ""},
	} {
		if got := TruncateRunes(tc.in, tc.limit); got != tc.want {
			t.Errorf("TruncateRunes(%q, %d) = %q, want %q", tc.in, tc.limit, got, tc.want)
		}
	}
}

func TestReactionErrorHelpers(t *testing.T) {
	rest := func(code int) error { return &discordgo.RESTError{Message: &discordgo.APIErrorMessage{Code: code}} }
	if !discord.IsMaxReactions(rest(30010)) || discord.IsMaxReactions(rest(10014)) || discord.IsMaxReactions(errors.New("x")) {
		t.Error("IsMaxReactions")
	}
	if !discord.IsUnknownEmoji(rest(10014)) || discord.IsUnknownEmoji(rest(30010)) {
		t.Error("IsUnknownEmoji")
	}
}
