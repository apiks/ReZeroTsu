package ping

import (
	"context"
	"fmt"
	"time"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/database/guilds"
	"ReZeroTsu/internal/discord"
	"ReZeroTsu/internal/interaction"

	"github.com/bwmarrin/discordgo"
)

func init() {
	commands.Add(&commands.Command{
		Visibility: commands.Public,
		Response:   commands.Direct,
		Name:       "ping",
		Desc:       "Respond with Pong! and show bot latency.",
		Permission: commands.PermEveryone,
		Module:     "general",
		Installs:   commands.BothInstalls(),
		Run:        func(ic *interaction.Context) error { return runPing(ic, ic.DB.Guilds()) },
	})
}

// settingsReader is the part of the guild store /ping reads.
type settingsReader interface {
	GetGuildSettings(ctx context.Context, guildID string) (*guilds.GuildSettings, error)
}

// settingsTimeout keeps the settings read inside Discord's three seconds for a first response, since /ping
// answers directly instead of deferring. A read that does not finish shows the default ping message. It is a
// variable so tests can shorten it.
var settingsTimeout = 1500 * time.Millisecond

func runPing(ic *interaction.Context, settings settingsReader) error {
	desc := guilds.DefaultGuildSettings().PingMessage
	if ic.BotInGuild() {
		ctx, cancel := context.WithTimeout(ic.Context(), settingsTimeout)
		defer cancel()
		gs, err := settings.GetGuildSettings(ctx, ic.GuildID)
		if err == nil && gs != nil && gs.PingMessage != "" {
			desc = gs.PingMessage
		}
	}
	emb := &discordgo.MessageEmbed{
		Title:       fmt.Sprintf(":ping_pong: %s", ic.Latency().Truncate(time.Millisecond).String()),
		Description: desc,
		Color:       discord.EmbedColor,
	}
	return ic.Reply(interaction.Response{Embeds: []*discordgo.MessageEmbed{emb}})
}
