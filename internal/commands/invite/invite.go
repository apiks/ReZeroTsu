package invite

import (
	"context"
	"fmt"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/discord"
	"ReZeroTsu/internal/interaction"

	"github.com/bwmarrin/discordgo"
)

func init() {
	commands.Add(&commands.Command{
		Visibility: commands.Public,
		Response:   commands.Direct,
		Name:       "invite",
		Desc:       "Display my invite link.",
		Permission: commands.PermEveryone,
		Module:     "general",
		Installs:   commands.BothInstalls(),
		Run:        run,
	})
	commands.AddMessageCommand(&commands.MessageCommand{
		Name:       "invite",
		Aliases:    []string{"inv", "invit"},
		Permission: commands.PermEveryone,
		Context:    commands.ContextDMOnly,
		MessageHandler: func(ctx context.Context, db *database.Client, s *discordgo.Session, m *discordgo.MessageCreate, args string, runtime commands.BotRuntime) {
			_, _ = s.ChannelMessageSendEmbed(m.ChannelID, inviteEmbed(s.State.User))
		},
	})
}

const hintInvite = "Use /add-moderator-role after inviting to set up moderator roles."

func run(ic *interaction.Context) error {
	return ic.Reply(interaction.Response{Embeds: []*discordgo.MessageEmbed{inviteEmbed(ic.BotUser())}})
}

func inviteEmbed(user *discordgo.User) *discordgo.MessageEmbed {
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
