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

// stalling never answers: it blocks until the context given to it ends.
type stalling struct{}

func (stalling) GetGuildSettings(ctx context.Context, _ string) (*guilds.GuildSettings, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

// /ping answers directly, inside Discord's three seconds, so a stalled settings read is cut short and the default
// ping message is shown.
func TestPingBoundsTheSettingsReadAndFallsBackToTheDefault(t *testing.T) {
	old := settingsTimeout
	settingsTimeout = 20 * time.Millisecond
	t.Cleanup(func() { settingsTimeout = old })

	fake := &interactiontest.Fake{}
	ic := interactiontest.NewCommandContext(fake, interaction.Public, interaction.Direct, discordgo.ApplicationCommandInteractionData{Name: "ping"})
	start := time.Now()
	if err := runPing(ic, stalling{}); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > time.Second {
		t.Error("the settings read was not bounded")
	}
	calls := fake.Calls()
	if len(calls) != 1 || calls[0].Response.Embeds[0].Description != guilds.DefaultGuildSettings().PingMessage {
		t.Errorf("calls = %+v, want one reply with the default ping message", calls)
	}
}

// The read runs under the interaction's context, so shutdown ends it.
func TestPingReadStopsWithTheInteractionContext(t *testing.T) {
	fake := &interactiontest.Fake{}
	ic := interactiontest.NewCommandContext(fake, interaction.Public, interaction.Direct, discordgo.ApplicationCommandInteractionData{Name: "ping"})
	ctx, cancel := context.WithCancel(context.Background())
	ic.Ctx = ctx
	cancel()
	done := make(chan error, 1)
	go func() { done <- runPing(ic, stalling{}) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("runPing ignored the cancelled interaction context")
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
