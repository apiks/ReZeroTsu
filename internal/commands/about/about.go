package about

import (
	"context"
	"fmt"

	"ReZeroTsu/internal/animeschedule"
	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/discord"
	"ReZeroTsu/internal/logger"

	"github.com/bwmarrin/discordgo"
)

const (
	patreonURL = "https://patreon.com/animeschedule"
	topGGBase  = "https://top.gg/bot/"
)

func init() {
	commands.Add(&commands.Command{
		Name:       "about",
		Desc:       "Display more information about me.",
		Permission: commands.PermEveryone,
		Module:     "general",
		Handler:    handleAbout,
	})
	commands.AddMessageCommand(&commands.MessageCommand{
		Name:       "about",
		Permission: commands.PermEveryone,
		Context:    commands.ContextDMOnly,
		MessageHandler: func(ctx context.Context, db *database.Client, s *discordgo.Session, m *discordgo.MessageCreate, args string, runtime commands.BotRuntime) {
			_, _ = s.ChannelMessageSendEmbed(m.ChannelID, aboutEmbed(s))
		},
	})
}

func handleAbout(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate) {
	emb := aboutEmbed(s)
	if _, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Embeds: &[]*discordgo.MessageEmbed{emb}}); err != nil {
		args := append(discord.RESTAttrs(err), "guild_id", i.GuildID, "channel_id", i.ChannelID, "err", err)
		logger.For("commands").Error("about edit failed", args...)
	}
}

func aboutEmbed(s *discordgo.Session) *discordgo.MessageEmbed {
	user := s.State.User
	inviteURL := fmt.Sprintf(discord.InviteURLFormat, user.ID, discord.DefaultInvitePermissions)

	emb := &discordgo.MessageEmbed{
		Title:       user.Username,
		Description: "Written in **Go** by Apiks. [GitHub](" + discord.RepoURL + "). For questions or help please **join** the [support server](" + discord.SupportServerURL + ").",
		Color:       discord.EmbedColor,
		Thumbnail:   &discordgo.MessageEmbedThumbnail{URL: user.AvatarURL("256")},
		Fields: []*discordgo.MessageEmbedField{{
			Name:  "**Features:**",
			Value: "**-** Autopost Anime Episodes, Anime Schedule (_subbed_)\n**-** Autopost Reddit Feed\n**-** React-based Auto Role\n**-** Raffles\n[Invite Link](" + inviteURL + ")",
		}},
	}
	emb.URL = topGGBase + user.ID
	emb.Fields = append(emb.Fields,
		&discordgo.MessageEmbedField{
			Name:  "**Anime Times:**",
			Value: "The Anime features derive their data from [AnimeSchedule.net](" + animeschedule.BaseURL + "), a site dedicated to showing you when and what anime are airing this week.",
		},
		&discordgo.MessageEmbedField{
			Name:  "**Support me:**",
			Value: "Consider becoming a [Patron](" + patreonURL + ") if you want to help out!",
		},
	)
	return emb
}
