package invite

import (
	"context"
	"fmt"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/discord"
	"ReZeroTsu/internal/logger"

	"github.com/bwmarrin/discordgo"
)

func init() {
	commands.Add(&commands.Command{
		Name:       "invite",
		Desc:       "Display my invite link.",
		Permission: commands.PermEveryone,
		Module:     "general",
		Handler:    handleInvite,
	})
	commands.AddMessageCommand(&commands.MessageCommand{
		Name:       "invite",
		Aliases:    []string{"inv", "invit"},
		Permission: commands.PermEveryone,
		Context:    commands.ContextDMOnly,
		MessageHandler: func(ctx context.Context, db *database.Client, s *discordgo.Session, m *discordgo.MessageCreate, args string, runtime commands.BotRuntime) {
			_, _ = s.ChannelMessageSendEmbed(m.ChannelID, inviteEmbed(s))
		},
	})
}

const hintInvite = "Use /add-moderator-role after inviting to set up moderator roles."

func handleInvite(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate) {
	emb := inviteEmbed(s)
	if _, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Embeds: &[]*discordgo.MessageEmbed{emb}}); err != nil {
		args := append(discord.RESTAttrs(err), "guild_id", i.GuildID, "channel_id", i.ChannelID, "err", err)
		logger.For("commands").Error("invite edit failed", args...)
	}
}

func inviteEmbed(s *discordgo.Session) *discordgo.MessageEmbed {
	user := s.State.User
	inviteURL := fmt.Sprintf(discord.InviteURLFormat, user.ID, discord.DefaultInvitePermissions)
	return &discordgo.MessageEmbed{
		URL:         inviteURL,
		Title:       "Invite Link",
		Description: "Click the title above for the invite link.",
		Color:       discord.EmbedColor,
		Thumbnail:   &discordgo.MessageEmbedThumbnail{URL: user.AvatarURL("256")},
		Footer:      &discordgo.MessageEmbedFooter{Text: hintInvite},
	}
}
