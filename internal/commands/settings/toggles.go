package settings

import (
	"context"
	"fmt"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/database"

	"github.com/bwmarrin/discordgo"
)

func init() {
	commands.Add(&commands.Command{
		Name:       "react-module",
		Desc:       "View or set whether the reacts module is enabled.",
		Permission: commands.PermMod,
		Context:    commands.ContextGuildOnly,
		Module:     "settings",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionBoolean,
				Name:        "enabled",
				Description: "Whether the reacts module should be enabled or disabled.",
				Required:    false,
			},
		},
		Handler: handleReactModule,
	})
	commands.Add(&commands.Command{
		Name:       "ping-message",
		Desc:       "View or set the message shown when someone pings the bot.",
		Permission: commands.PermMod,
		Context:    commands.ContextGuildOnly,
		Module:     "settings",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "message",
				Description: "The new ping message.",
				Required:    false,
			},
		},
		Handler: handlePingMessage,
	})
	commands.Add(&commands.Command{
		Name:       "mod-only",
		Desc:       "View or set whether only moderators can use bot commands.",
		Permission: commands.PermMod,
		Context:    commands.ContextGuildOnly,
		Module:     "settings",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionBoolean,
				Name:        "enabled",
				Description: "Whether mod-only mode should be enabled or disabled.",
				Required:    false,
			},
		},
		Handler: handleModOnly,
	})
	commands.Add(&commands.Command{
		Name:       "donghua",
		Desc:       "View or set whether donghua (Chinese anime) appears in the anime schedule.",
		Permission: commands.PermMod,
		Context:    commands.ContextGuildOnly,
		Module:     "settings",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionBoolean,
				Name:        "enabled",
				Description: "Whether donghua should be shown in the schedule.",
				Required:    false,
			},
		},
		Handler: handleDonghua,
	})
}

const (
	embedTitleReactModule = "Reacts Module"
	embedTitlePingMessage = "Ping Message"
	embedTitleModOnly     = "Mod-Only"
	embedTitleDonghua     = "Donghua"

	hintReactModule = "Use /react-module enabled:true or enabled:false to change."
	hintPingMessage = "Use /ping-message message:your text to change."
	hintModOnly     = "Use /mod-only enabled:true or enabled:false to change."
	hintDonghua     = "Use /donghua enabled:true or enabled:false to change."
)

func handleReactModule(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate) {
	settings, err := commands.GetSettings(ctx, db, i.GuildID)
	if err != nil {
		commands.RespondEmbed(s, i, embedTitleReactModule, "Could not load guild settings.")
		return
	}

	data := i.ApplicationCommandData()
	opt := commands.ParseOption(data.Options, "enabled")
	if opt == nil {
		status := "Disabled"
		if settings.ReactsModule {
			status = "Enabled"
		}
		commands.RespondEmbed(s, i, embedTitleReactModule, fmt.Sprintf("**Status:** %s", status), hintReactModule)
		return
	}
	settings.ReactsModule = opt.BoolValue()
	if err := db.Guilds().SetGuildSettings(ctx, i.GuildID, settings); err != nil {
		commands.RespondEmbed(s, i, embedTitleReactModule, "Could not save settings.")
		return
	}
	status := "Disabled"
	if settings.ReactsModule {
		status = "Enabled"
	}
	commands.RespondEmbed(s, i, embedTitleReactModule, fmt.Sprintf("**Status:** %s", status), hintReactModule)
}

func handlePingMessage(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate) {
	settings, err := commands.GetSettings(ctx, db, i.GuildID)
	if err != nil {
		commands.RespondEmbed(s, i, embedTitlePingMessage, "Could not load guild settings.")
		return
	}

	data := i.ApplicationCommandData()
	opt := commands.ParseOption(data.Options, "message")
	if opt == nil {
		commands.RespondEmbed(s, i, embedTitlePingMessage, fmt.Sprintf("**Current message:**\n%s", settings.PingMessage), hintPingMessage)
		return
	}
	settings.PingMessage = opt.StringValue()
	if err := db.Guilds().SetGuildSettings(ctx, i.GuildID, settings); err != nil {
		commands.RespondEmbed(s, i, embedTitlePingMessage, "Could not save settings.")
		return
	}
	commands.RespondEmbed(s, i, embedTitlePingMessage, fmt.Sprintf("**Current message:**\n%s", settings.PingMessage), hintPingMessage)
}

func handleModOnly(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate) {
	settings, err := commands.GetSettings(ctx, db, i.GuildID)
	if err != nil {
		commands.RespondEmbed(s, i, embedTitleModOnly, "Could not load guild settings.")
		return
	}

	data := i.ApplicationCommandData()
	opt := commands.ParseOption(data.Options, "enabled")
	if opt == nil {
		status := "Disabled"
		if settings.ModOnly {
			status = "Enabled"
		}
		commands.RespondEmbed(s, i, embedTitleModOnly, fmt.Sprintf("**Status:** %s\n\nOnly users with a moderator role can use bot commands when enabled.", status), hintModOnly)
		return
	}
	settings.ModOnly = opt.BoolValue()
	if err := db.Guilds().SetGuildSettings(ctx, i.GuildID, settings); err != nil {
		commands.RespondEmbed(s, i, embedTitleModOnly, "Could not save settings.")
		return
	}
	status := "Disabled"
	if settings.ModOnly {
		status = "Enabled"
	}
	commands.RespondEmbed(s, i, embedTitleModOnly, fmt.Sprintf("**Status:** %s", status), hintModOnly)
}

func handleDonghua(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate) {
	settings, err := commands.GetSettings(ctx, db, i.GuildID)
	if err != nil {
		commands.RespondEmbed(s, i, embedTitleDonghua, "Could not load guild settings.")
		return
	}

	data := i.ApplicationCommandData()
	opt := commands.ParseOption(data.Options, "enabled")
	if opt == nil {
		status := "Disabled"
		if settings.Donghua {
			status = "Enabled"
		}
		commands.RespondEmbed(s, i, embedTitleDonghua, fmt.Sprintf("Donghua (Chinese anime) in schedule: **%s**.", status), hintDonghua)
		return
	}
	settings.Donghua = opt.BoolValue()
	if err := db.Guilds().SetGuildSettings(ctx, i.GuildID, settings); err != nil {
		commands.RespondEmbed(s, i, embedTitleDonghua, "Could not save settings.")
		return
	}
	status := "Disabled"
	if settings.Donghua {
		status = "Enabled"
	}
	commands.RespondEmbed(s, i, embedTitleDonghua, fmt.Sprintf("Donghua in schedule: **%s**.", status), hintDonghua)
}
