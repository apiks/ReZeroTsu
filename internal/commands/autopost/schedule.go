package autopost

import (
	"context"
	"fmt"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/database/guilds"

	"github.com/bwmarrin/discordgo"
)

const (
	postTypeDailySchedule = "dailyschedule"
	embedTitleDaily       = "Daily Schedule Autopost"
	hintDailySchedule     = "Use /daily-schedule to change or disable."
)

func init() {
	commands.Add(&commands.Command{
		Name:       "daily-schedule",
		Desc:       "Set or disable the channel for automatic daily anime schedule posts.",
		Permission: commands.PermMod,
		Context:    commands.ContextGuildOnly,
		Module:     "autopost",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionChannel,
				Name:        "channel",
				Description: "The channel to post the daily anime schedule to.",
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
				Description: "Whether the daily schedule autopost should be enabled or disabled.",
				Required:    false,
			},
		},
		Handler: handleDailySchedule,
	})
}

func handleDailySchedule(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.GuildID == "" {
		commands.RespondEmbed(s, i, embedTitleDaily, "This command can only be used in a server.")
		return
	}

	data := i.ApplicationCommandData()
	optChannel := commands.ParseOption(data.Options, "channel")
	optEnabled := commands.ParseOption(data.Options, "enabled")

	// Disable: enabled == false
	if optEnabled != nil && !optEnabled.BoolValue() {
		if err := db.Guilds().SetAutopost(ctx, i.GuildID, postTypeDailySchedule, nil); err != nil {
			commands.RespondEmbed(s, i, embedTitleDaily, "Could not save. Try again.")
			return
		}
		commands.RespondEmbed(s, i, embedTitleDaily, "Daily schedule autopost has been disabled.")
		return
	}

	// Set channel: channel provided (and enabled not false, or omitted)
	if optChannel != nil {
		ch := optChannel.ChannelValue(s)
		if ch == nil || ch.ID == "" {
			commands.RespondEmbed(s, i, embedTitleDaily, "Invalid channel.")
			return
		}
		if ch.Type != discordgo.ChannelTypeGuildText && ch.Type != discordgo.ChannelTypeGuildPublicThread && ch.Type != discordgo.ChannelTypeGuildPrivateThread {
			commands.RespondEmbed(s, i, embedTitleDaily, "Please choose a text or thread channel.")
			return
		}
		ap := &guilds.Autopost{
			PostType: postTypeDailySchedule,
			Name:     ch.Name,
			ID:       ch.ID,
		}
		if err := db.Guilds().SetAutopost(ctx, i.GuildID, postTypeDailySchedule, ap); err != nil {
			commands.RespondEmbed(s, i, embedTitleDaily, "Could not save. Try again.")
			return
		}
		commands.RespondEmbed(s, i, embedTitleDaily, fmt.Sprintf("Daily schedule autopost set to **#%s** (ID: %s).", ch.Name, ch.ID), hintDailySchedule)
		return
	}

	ap, err := db.Guilds().GetAutopostByType(ctx, i.GuildID, postTypeDailySchedule)
	if err != nil {
		commands.RespondEmbed(s, i, embedTitleDaily, "Could not load settings.")
		return
	}
	if ap == nil || ap.ID == "" {
		commands.RespondEmbed(s, i, embedTitleDaily, "Daily anime schedule autopost is not set.", hintDailySchedule)
		return
	}
	commands.RespondEmbed(s, i, embedTitleDaily, fmt.Sprintf("Current daily schedule autopost channel: **#%s** (ID: %s).", ap.Name, ap.ID), hintDailySchedule)
}
