package ping

import (
	"context"
	"fmt"
	"time"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/database/guilds"
	"ReZeroTsu/internal/discord"
	"ReZeroTsu/internal/logger"

	"github.com/bwmarrin/discordgo"
)

func init() {
	commands.Add(&commands.Command{
		Name:       "ping",
		Desc:       "Respond with Pong! and show bot latency.",
		Permission: commands.PermEveryone,
		Module:     "general",
		Handler:    handlePing,
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

func handlePing(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate) {
	desc := guilds.DefaultGuildSettings().PingMessage
	if i.GuildID != "" {
		settings, err := db.Guilds().GetGuildSettings(ctx, i.GuildID)
		if err == nil && settings != nil && settings.PingMessage != "" {
			desc = settings.PingMessage
		}
	}
	title := fmt.Sprintf(":ping_pong: %s", s.HeartbeatLatency().Truncate(time.Millisecond).String())
	emb := commands.NewEmbed(s, title, desc)
	if _, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Embeds: &[]*discordgo.MessageEmbed{emb}}); err != nil {
		args := append(discord.RESTAttrs(err), "guild_id", i.GuildID, "channel_id", i.ChannelID, "err", err)
		logger.For("commands").Error("InteractionResponseEdit failed", args...)
	}
}
