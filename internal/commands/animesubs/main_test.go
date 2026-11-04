package animesubs

import (
	"context"
	"os"
	"testing"

	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/database/guilds"
)

// TestMain keeps every test off the database: a server that is not under test shows donghua, as an unset server does.
func TestMain(m *testing.M) {
	guildSettings = func(context.Context, *database.Client, string) (*guilds.GuildSettings, error) {
		return &guilds.GuildSettings{Donghua: true}, nil
	}
	os.Exit(m.Run())
}
