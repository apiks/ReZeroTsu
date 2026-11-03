package invite

import (
	"fmt"

	"ReZeroTsu/internal/commands"
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
