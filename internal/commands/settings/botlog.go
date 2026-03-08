package settings

import (
	"context"
	"fmt"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/database/guilds"

	"github.com/bwmarrin/discordgo"
)

func init() {
	commands.Add(&commands.Command{
		Name:       "bot-log",
		Desc:       "View or set the bot log channel. Use 'disable' or enabled=false to turn off.",
		Permission: commands.PermMod,
		Context:    commands.ContextGuildOnly,
		Module:     "settings",
		Options:    botLogOptions(),
		Handler:    handleBotLog,
	})
}

func botLogOptions() []*discordgo.ApplicationCommandOption {
	return []*discordgo.ApplicationCommandOption{
		{
			Type:        discordgo.ApplicationCommandOptionChannel,
			Name:        "channel",
			Description: "The channel to use for bot log.",
			Required:    false,
			ChannelTypes: []discordgo.ChannelType{
				discordgo.ChannelTypeGuildText,
			},
		},
		{
			Type:        discordgo.ApplicationCommandOptionBoolean,
			Name:        "enabled",
			Description: "Whether the bot log should be enabled or disabled.",
			Required:    false,
		},
	}
}

const (
	embedTitleBotLog   = "Bot Log"
	hintBotLogSet      = "Use /bot-log channel:#channel to set it, or enabled:false to disable."
	hintBotLogChange   = "Use /bot-log channel:#channel to change, or enabled:false to disable."
	hintBotLogSetAgain = "Use /bot-log channel:#channel to set a channel again."
)

func handleBotLog(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate) {
	settings, err := commands.GetSettings(ctx, db, i.GuildID)
	if err != nil {
		commands.RespondEmbed(s, i, embedTitleBotLog, "Could not load guild settings.")
		return
	}

	data := i.ApplicationCommandData()
	optChannel := commands.ParseOption(data.Options, "channel")
	optEnabled := commands.ParseOption(data.Options, "enabled")

	if optChannel == nil && optEnabled == nil {
		if settings.BotLogID == nil || settings.BotLogID.ID == "" {
			commands.RespondEmbed(s, i, embedTitleBotLog, "**Status:** Not set", hintBotLogSet)
			return
		}
		commands.RespondEmbed(s, i, embedTitleBotLog, fmt.Sprintf("**Status:** Enabled\n\n**Channel:** %s\n**ID:** `%s`", settings.BotLogID.Name, settings.BotLogID.ID), hintBotLogChange)
		return
	}

	enabled := true
	if optEnabled != nil {
		enabled = optEnabled.BoolValue()
	}

	if !enabled {
		settings.BotLogID = nil
		if err := db.Guilds().SetGuildSettings(ctx, i.GuildID, settings); err != nil {
			commands.RespondEmbed(s, i, embedTitleBotLog, "Could not save settings.")
			return
		}
		commands.RespondEmbed(s, i, embedTitleBotLog, "**Status:** Disabled\n\nBot log has been turned off.", hintBotLogSetAgain)
		return
	}

	if optChannel == nil {
		if settings.BotLogID != nil && settings.BotLogID.ID != "" {
			commands.RespondEmbed(s, i, embedTitleBotLog, fmt.Sprintf("**Status:** Enabled\n\n**Channel:** %s\n**ID:** `%s`", settings.BotLogID.Name, settings.BotLogID.ID), hintBotLogChange)
		} else {
			commands.RespondEmbed(s, i, embedTitleBotLog, "**Status:** Not set", hintBotLogSet)
		}
		return
	}

	ch := optChannel.ChannelValue(s)
	if ch == nil {
		commands.RespondEmbed(s, i, embedTitleBotLog, "That channel was not found.")
		return
	}
	settings.BotLogID = &guilds.ChannelRef{Name: ch.Name, ID: ch.ID}
	if err := db.Guilds().SetGuildSettings(ctx, i.GuildID, settings); err != nil {
		commands.RespondEmbed(s, i, embedTitleBotLog, "Could not save settings.")
		return
	}
	commands.RespondEmbed(s, i, embedTitleBotLog, fmt.Sprintf("**Status:** Enabled\n\n**Channel:** %s\n**ID:** `%s`", ch.Name, ch.ID), hintBotLogChange)
}
