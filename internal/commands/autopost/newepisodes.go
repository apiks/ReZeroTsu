package autopost

import (
	"context"
	"fmt"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/database/guilds"
	"ReZeroTsu/internal/logger"

	"github.com/bwmarrin/discordgo"
)

const (
	postTypeNewEpisodes    = "newepisodes"
	embedTitleNewEp        = "New Episodes Autopost"
	hintNewEpisodesChange  = "Use /new-episodes to change or disable."
	hintNewEpisodesSet     = "Use /new-episodes to set a channel or disable."
	hintNewEpisodesCurrent = "Use /new-episodes to change channel, role, or disable."
)

func init() {
	commands.Add(&commands.Command{
		Name:       "new-episodes",
		Desc:       "Set or disable the channel for automatic new anime episode notifications.",
		Permission: commands.PermMod,
		Context:    commands.ContextGuildOnly,
		Module:     "autopost",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionChannel,
				Name:        "channel",
				Description: "The channel to post new episode notifications to.",
				Required:    false,
				ChannelTypes: []discordgo.ChannelType{
					discordgo.ChannelTypeGuildText,
					discordgo.ChannelTypeGuildPublicThread,
					discordgo.ChannelTypeGuildPrivateThread,
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionBoolean,
				Name:        "enabled",
				Description: "Whether the new episodes autopost should be enabled or disabled.",
				Required:    false,
			},
			{
				Type:        discordgo.ApplicationCommandOptionRole,
				Name:        "role",
				Description: "Role to ping when a new episode is posted.",
				Required:    false,
			},
		},
		Handler: handleNewEpisodes,
	})
}

func handleNewEpisodes(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.GuildID == "" {
		commands.RespondEmbed(s, i, embedTitleNewEp, "This command can only be used in a server.")
		return
	}

	data := i.ApplicationCommandData()
	optChannel := commands.ParseOption(data.Options, "channel")
	optEnabled := commands.ParseOption(data.Options, "enabled")
	optRole := commands.ParseOption(data.Options, "role")

	// Disable: enabled == false
	if optEnabled != nil && !optEnabled.BoolValue() {
		ap, err := db.Guilds().GetAutopostByType(ctx, i.GuildID, postTypeNewEpisodes)
		if err != nil {
			logger.For("commands").Error("GetAutopostByType on disable failed", "guild_id", i.GuildID, "err", err)
		}
		if ap != nil && ap.ID != "" {
			_ = db.AnimeSubs().DeleteByID(ctx, ap.ID)
		}
		if err := db.Guilds().SetAutopost(ctx, i.GuildID, postTypeNewEpisodes, nil); err != nil {
			commands.RespondEmbed(s, i, embedTitleNewEp, "Could not save. Try again.")
			return
		}
		commands.RespondEmbed(s, i, embedTitleNewEp, "New episodes autopost has been disabled.")
		return
	}

	// Set channel and/or role
	if optChannel != nil || optRole != nil {
		ap, err := db.Guilds().GetAutopostByType(ctx, i.GuildID, postTypeNewEpisodes)
		if err != nil {
			commands.RespondEmbed(s, i, embedTitleNewEp, "Could not load settings.")
			return
		}
		if ap == nil {
			ap = &guilds.Autopost{PostType: postTypeNewEpisodes}
		}
		if optChannel != nil {
			ch := optChannel.ChannelValue(s)
			if ch == nil || ch.ID == "" {
				commands.RespondEmbed(s, i, embedTitleNewEp, "Invalid channel.")
				return
			}
			if ch.Type != discordgo.ChannelTypeGuildText && ch.Type != discordgo.ChannelTypeGuildPublicThread && ch.Type != discordgo.ChannelTypeGuildPrivateThread {
				commands.RespondEmbed(s, i, embedTitleNewEp, "Please choose a text or thread channel.")
				return
			}
			ap.Name = ch.Name
			ap.ID = ch.ID
		}
		if optRole != nil {
			role := optRole.RoleValue(s, i.GuildID)
			if role != nil {
				ap.RoleID = role.ID
			} else {
				ap.RoleID = ""
			}
		}
		if ap.ID == "" {
			commands.RespondEmbed(s, i, embedTitleNewEp, "Set a channel first.")
			return
		}
		if err := db.Guilds().SetAutopost(ctx, i.GuildID, postTypeNewEpisodes, ap); err != nil {
			commands.RespondEmbed(s, i, embedTitleNewEp, "Could not save. Try again.")
			return
		}
		roleInfo := ""
		if ap.RoleID != "" {
			roleInfo = fmt.Sprintf(" Role ping: <@&%s>.", ap.RoleID)
		}
		commands.RespondEmbed(s, i, embedTitleNewEp, fmt.Sprintf("New episodes autopost set to **#%s** (ID: %s).%s", ap.Name, ap.ID, roleInfo), hintNewEpisodesChange)
		return
	}

	ap, err := db.Guilds().GetAutopostByType(ctx, i.GuildID, postTypeNewEpisodes)
	if err != nil {
		commands.RespondEmbed(s, i, embedTitleNewEp, "Could not load settings.")
		return
	}
	if ap == nil || ap.ID == "" {
		commands.RespondEmbed(s, i, embedTitleNewEp, "New episodes autopost is not set.", hintNewEpisodesSet)
		return
	}
	roleInfo := ""
	if ap.RoleID != "" {
		roleInfo = fmt.Sprintf(" Role ping: <@&%s>.", ap.RoleID)
	}
	commands.RespondEmbed(s, i, embedTitleNewEp, fmt.Sprintf("Current new episodes autopost channel: **#%s** (ID: %s).%s", ap.Name, ap.ID, roleInfo), hintNewEpisodesCurrent)
}
