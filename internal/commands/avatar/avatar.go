package avatar

import (
	"context"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/discord"
	"ReZeroTsu/internal/interaction"

	"github.com/bwmarrin/discordgo"
)

const embedTitleAvatar = "Avatar"

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
	commands.AddMessageCommand(&commands.MessageCommand{
		Name:       "avatar",
		Permission: commands.PermEveryone,
		Context:    commands.ContextDMOnly,
		MessageHandler: func(ctx context.Context, db *database.Client, s *discordgo.Session, m *discordgo.MessageCreate, args string, runtime commands.BotRuntime) {
			user := m.Author
			if len(m.Mentions) > 0 {
				user = m.Mentions[0]
			}
			if user == nil {
				emb := commands.NewEmbed(s, embedTitleAvatar, "Could not determine the user.")
				_, _ = s.ChannelMessageSendEmbed(m.ChannelID, emb)
				return
			}
			avatarURL := user.AvatarURL("1024")
			if avatarURL == "" {
				emb := commands.NewEmbed(s, embedTitleAvatar, "This user has no avatar.")
				_, _ = s.ChannelMessageSendEmbed(m.ChannelID, emb)
				return
			}
			emb := &discordgo.MessageEmbed{
				Title: user.Username + "'s avatar",
				Color: discord.EmbedColor,
				Image: &discordgo.MessageEmbedImage{URL: avatarURL},
			}
			_, _ = s.ChannelMessageSendEmbed(m.ChannelID, emb)
		},
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
