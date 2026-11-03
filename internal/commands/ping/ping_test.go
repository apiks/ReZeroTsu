package ping

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/database/guilds"
	"ReZeroTsu/internal/interaction"
	"ReZeroTsu/internal/interaction/interactiontest"

	"github.com/bwmarrin/discordgo"
)

type settings struct {
	ping string
	err  error
}

func (s settings) GetGuildSettings(context.Context, string) (*guilds.GuildSettings, error) {
	return &guilds.GuildSettings{PingMessage: s.ping}, s.err
}

func pingWith(s settings) []interactiontest.Call {
	fake := &interactiontest.Fake{HeartbeatLatency: 42*time.Millisecond + 500*time.Microsecond}
	ic := interactiontest.NewCommandContext(fake, interaction.Public, interaction.Direct, discordgo.ApplicationCommandInteractionData{Name: "ping"})
	if err := runPing(ic, s); err != nil {
		panic(err)
	}
	return fake.Calls()
}

func TestPingRepliesOncePubliclyWithLatencyAndTheServersPingMessage(t *testing.T) {
	calls := pingWith(settings{ping: "Hi there"})
	if len(calls) != 1 || calls[0].Op != "Reply" || calls[0].Visibility != interaction.Public {
		t.Fatalf("calls = %+v, want exactly one public Reply", calls)
	}
	emb := calls[0].Response.Embeds[0]
	if !strings.Contains(emb.Title, "42ms") || emb.Description != "Hi there" {
		t.Errorf("embed = %+v", emb)
	}
}

func TestPingFallsBackToTheDefaultMessage(t *testing.T) {
	for name, s := range map[string]settings{"empty": {}, "store error": {ping: "x", err: errors.New("db")}} {
		t.Run(name, func(t *testing.T) {
			emb := pingWith(s)[0].Response.Embeds[0]
			if emb.Description != guilds.DefaultGuildSettings().PingMessage {
				t.Errorf("description = %q", emb.Description)
			}
		})
	}
}

func TestPingIsRegisteredAsAPublicDirectCommand(t *testing.T) {
	for _, c := range commands.AllCommands() {
		if c.Name == "ping" {
			if c.Run == nil || c.Handler != nil || c.Visibility != commands.Public || c.Response != commands.Direct {
				t.Errorf("ping registration = %+v", c)
			}
			return
		}
	}
	t.Fatal("ping is not registered")
}
