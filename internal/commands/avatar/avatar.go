package avatar

import (
	"context"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/discord"
	"ReZeroTsu/internal/logger"

	"github.com/bwmarrin/discordgo"
)

const embedTitleAvatar = "Avatar"

func init() {
	commands.Add(&commands.Command{
		Name:       "avatar",
		Desc:       "Show a user's avatar.",
		Permission: commands.PermEveryone,
		Module:     "general",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionUser,
				Name:        "user",
				Description: "The user whose avatar you want to see.",
				Required:    false,
			},
		},
		Handler: handleAvatar,
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

func handleAvatar(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate) {
	user := commands.InteractionUser(i)
	data := i.ApplicationCommandData()
	if opt := commands.ParseOption(data.Options, "user"); opt != nil {
		if u := opt.UserValue(s); u != nil {
			user = u
		}
	}
	if user == nil {
		commands.RespondEmbed(s, i, embedTitleAvatar, "Could not determine the user.")
		return
	}

	avatarURL := user.AvatarURL("1024")
	if avatarURL == "" {
		commands.RespondEmbed(s, i, embedTitleAvatar, "This user has no avatar.")
		return
	}

	emb := &discordgo.MessageEmbed{
		Title: user.Username + "'s avatar",
		Color: discord.EmbedColor,
		Image: &discordgo.MessageEmbedImage{URL: avatarURL},
	}
	if _, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Embeds: &[]*discordgo.MessageEmbed{emb}}); err != nil {
		args := append(discord.RESTAttrs(err), "guild_id", i.GuildID, "channel_id", i.ChannelID, "err", err)
		logger.For("commands").Error("avatar edit failed", args...)
	}
}
