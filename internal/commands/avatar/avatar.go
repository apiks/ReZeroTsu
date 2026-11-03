package avatar

import (
	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/discord"
	"ReZeroTsu/internal/interaction"

	"github.com/bwmarrin/discordgo"
)

func init() {
	commands.Add(&commands.Command{
		Visibility: commands.Public,
		Response:   commands.Direct,
		Name:       "avatar",
		Desc:       "Show a user's avatar.",
		Permission: commands.PermEveryone,
		Module:     "general",
		Installs:   commands.BothInstalls(),
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionUser,
				Name:        "user",
				Description: "The user whose avatar you want to see.",
				Required:    false,
			},
		},
		Run: run,
	})
	commands.Add(&commands.Command{
		Name:       "Show avatar",
		Kind:       commands.KindUser,
		Visibility: commands.Public,
		Response:   commands.Direct,
		Permission: commands.PermEveryone,
		Module:     "general",
		Installs:   commands.BothInstalls(),
		Run:        runShowAvatar,
	})
}

func run(ic *interaction.Context) error {
	user := ic.Invoker
	if opt := commands.ParseOption(ic.Command.Options, "user"); opt != nil {
		user = nil
		if id, ok := opt.Value.(string); ok && ic.Command.Resolved != nil {
			user = ic.Command.Resolved.Users[id]
		}
	}
	return replyWithAvatar(ic, user)
}

// runShowAvatar answers the "Show avatar" right-click command with the target's avatar.
func runShowAvatar(ic *interaction.Context) error {
	return replyWithAvatar(ic, ic.TargetUser())
}

func replyWithAvatar(ic *interaction.Context, user *discordgo.User) error {
	if user == nil {
		return ic.Refuse("Could not determine the user. Please try again.")
	}
	avatarURL := user.AvatarURL("1024")
	if avatarURL == "" {
		return ic.Refuse("This user has no avatar.")
	}
	emb := &discordgo.MessageEmbed{
		Title: user.Username + "'s avatar",
		Color: discord.EmbedColor,
		Image: &discordgo.MessageEmbedImage{URL: avatarURL},
	}
	return ic.Reply(interaction.Response{Embeds: []*discordgo.MessageEmbed{emb}})
}
