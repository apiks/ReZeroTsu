package bot

import (
	"slices"
	"testing"

	"ReZeroTsu/internal/commands"
)

func TestOnlyTheOwnerCommandsRemainAsPrefixCommands(t *testing.T) {
	want := []string{"messagelogs", "servers", "shards", "uptime"}
	if got := commands.MessageCommandNames(); !slices.Equal(got, want) {
		t.Errorf("registered prefix commands = %v, want only the owner commands %v", got, want)
	}
}
