package ping

import (
	"context"
	"fmt"
	"time"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/database"
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
		Run:        func(ic *interaction.Context) error { return runPing(ic, ic.DB.Guilds()) },
	})
	commands.AddMessageCommand(&commands.MessageCommand{
		Name:       "ping",
		Aliases:    []string{"pingme"},
		Permission: commands.PermEveryone,
		Context:    commands.ContextDMOnly,
		MessageHandler: func(ctx context.Context, db *database.Client, s *discordgo.Session, m *discordgo.MessageCreate, args string, runtime commands.BotRuntime) {
			desc := guilds.DefaultGuildSettings().PingMessage
			title := fmt.Sprintf(":ping_pong: %s", s.HeartbeatLatency().Truncate(time.Millisecond).String())
			emb := commands.NewEmbed(s, title, desc)
			_, _ = s.ChannelMessageSendEmbed(m.ChannelID, emb)
		},
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
	if ic.GuildID != "" {
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
