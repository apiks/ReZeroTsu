package owner

import (
	"context"
	"fmt"
	"time"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/database"

	"github.com/bwmarrin/discordgo"
)

func init() {
	commands.AddMessageCommand(&commands.MessageCommand{
		Name:       "servers",
		Permission: commands.PermOwner,
		Context:    commands.ContextDMOnly,
		MessageHandler: func(ctx context.Context, db *database.Client, s *discordgo.Session, m *discordgo.MessageCreate, args string, runtime commands.BotRuntime) {
			n := 0
			if runtime != nil {
				n = runtime.GuildCount()
			}
			_, _ = s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("I am in %d servers.", n))
		},
	})
	commands.AddMessageCommand(&commands.MessageCommand{
		Name:       "shards",
		Permission: commands.PermOwner,
		Context:    commands.ContextDMOnly,
		MessageHandler: func(ctx context.Context, db *database.Client, s *discordgo.Session, m *discordgo.MessageCreate, args string, runtime commands.BotRuntime) {
			n := 0
			if runtime != nil {
				n = runtime.ShardCount()
			}
			_, _ = s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("I have %d shards.", n))
		},
	})
	commands.AddMessageCommand(&commands.MessageCommand{
		Name:       "uptime",
		Permission: commands.PermOwner,
		Context:    commands.ContextDMOnly,
		MessageHandler: func(ctx context.Context, db *database.Client, s *discordgo.Session, m *discordgo.MessageCreate, args string, runtime commands.BotRuntime) {
			var d time.Duration
			if runtime != nil {
				d = runtime.Uptime().Truncate(time.Second)
			}
			_, _ = s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("I've been online for %s.", d))
		},
	})
	commands.AddMessageCommand(&commands.MessageCommand{
		Name:       "messagelogs",
		Permission: commands.PermOwner,
		Context:    commands.ContextDMOnly,
		MessageHandler: func(ctx context.Context, db *database.Client, s *discordgo.Session, m *discordgo.MessageCreate, args string, runtime commands.BotRuntime) {
			if args == "" {
				_, _ = s.ChannelMessageSend(m.ChannelID, "Usage: messagelogs <message>")
				return
			}
			channelIDs, err := db.Guilds().ListBotLogChannelIDs(ctx)
			if err != nil {
				emb := commands.NewEmbed(s, "Error", fmt.Sprintf("Failed to list bot log channels: %v", err))
				_, _ = s.ChannelMessageSendEmbed(m.ChannelID, emb)
				return
			}
			if len(channelIDs) == 0 {
				_, _ = s.ChannelMessageSend(m.ChannelID, "No bot log channels configured.")
				return
			}
			total := len(channelIDs)
			progressMsg, _ := s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Sending to %d channels... (0/%d)", total, total))
			for i, chID := range channelIDs {
				_, _ = s.ChannelMessageSend(chID, args)
				time.Sleep(1 * time.Second)
				sent := i + 1
				if progressMsg != nil && (sent%30 == 0 || sent == total) {
					_, _ = s.ChannelMessageEdit(m.ChannelID, progressMsg.ID, fmt.Sprintf("Sending to %d channels... (%d/%d)", total, sent, total))
				}
			}
			if progressMsg != nil {
				_, _ = s.ChannelMessageEdit(m.ChannelID, progressMsg.ID, fmt.Sprintf("Sent to %d channels.", total))
			}
		},
	})
}
