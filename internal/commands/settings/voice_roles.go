package settings

import (
	"context"
	"fmt"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/database/guilds"

	"github.com/bwmarrin/discordgo"
)

const (
	embedTitleVoiceRoles   = "Voice Roles"
	hintVoiceRoles         = "Use /add-voice to add, /remove-voice to remove."
	hintVoiceRolesRemove  = "Use /remove-voice to remove."
	hintVoiceRolesListAdd = "Use /voice-roles to list all, /add-voice to add."
	hintVoiceRolesEmpty   = "Use /add-voice to assign a role to a channel, /remove-voice to remove one."
)

func init() {
	commands.Add(&commands.Command{
		Name:       "add-voice",
		Desc:       "Set a voice channel to give a role when users join and remove it when they leave.",
		Permission: commands.PermMod,
		Context:    commands.ContextGuildOnly,
		Module:     "settings",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionChannel,
				Name:        "channel",
				Description: "The voice channel.",
				Required:    true,
				ChannelTypes: []discordgo.ChannelType{
					discordgo.ChannelTypeGuildVoice,
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionRole,
				Name:        "role",
				Description: "The role to give when joining the channel.",
				Required:    true,
			},
		},
		Handler: handleAddVoice,
	})
	commands.Add(&commands.Command{
		Name:       "remove-voice",
		Desc:       "Remove a voice channel role, or remove a specific role from a channel.",
		Permission: commands.PermMod,
		Context:    commands.ContextGuildOnly,
		Module:     "settings",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionChannel,
				Name:        "channel",
				Description: "The voice channel to remove.",
				Required:    true,
				ChannelTypes: []discordgo.ChannelType{
					discordgo.ChannelTypeGuildVoice,
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionRole,
				Name:        "role",
				Description: "If provided, remove only this role from the channel.",
				Required:    false,
			},
		},
		Handler: handleRemoveVoice,
	})
	commands.Add(&commands.Command{
		Name:       "voice-roles",
		Desc:       "List all voice channels and their associated roles.",
		Permission: commands.PermMod,
		Context:    commands.ContextGuildOnly,
		Module:     "settings",
		Handler:    handleVoiceRoles,
	})
}

func handleAddVoice(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate) {
	settings, err := commands.GetSettings(ctx, db, i.GuildID)
	if err != nil {
		commands.RespondEmbed(s, i, embedTitleVoiceRoles, "Could not load guild settings.")
		return
	}

	data := i.ApplicationCommandData()
	optCh := commands.ParseOption(data.Options, "channel")
	optRole := commands.ParseOption(data.Options, "role")
	if optCh == nil || optRole == nil {
		commands.RespondEmbed(s, i, embedTitleVoiceRoles, "Please provide both channel and role.")
		return
	}
	ch := optCh.ChannelValue(s)
	role := optRole.RoleValue(s, i.GuildID)
	if ch == nil || role == nil {
		commands.RespondEmbed(s, i, embedTitleVoiceRoles, "Channel or role not found.")
		return
	}
	if ch.Type != discordgo.ChannelTypeGuildVoice {
		commands.RespondEmbed(s, i, embedTitleVoiceRoles, "That channel is not a voice channel.")
		return
	}

	newRole := guilds.Role{Name: role.Name, ID: role.ID, Position: role.Position}
	merged := false
	for j, vc := range settings.VoiceChas {
		if vc.ID == ch.ID {
			for _, r := range vc.Roles {
				if r.ID == role.ID {
					commands.RespondEmbed(s, i, embedTitleVoiceRoles, fmt.Sprintf("Role `%s` is already set for channel `%s`.", role.Name, ch.Name))
					return
				}
			}
			settings.VoiceChas[j].Roles = append(settings.VoiceChas[j].Roles, newRole)
			merged = true
			break
		}
	}
	if !merged {
		settings.VoiceChas = append(settings.VoiceChas, guilds.VoiceCha{
			Name: ch.Name, ID: ch.ID, Roles: []guilds.Role{newRole},
		})
	}
	if err := db.Guilds().SetGuildSettings(ctx, i.GuildID, settings); err != nil {
		commands.RespondEmbed(s, i, embedTitleVoiceRoles, "Could not save settings.")
		return
	}
	commands.RespondEmbed(s, i, embedTitleVoiceRoles, fmt.Sprintf("**Channel:** %s\n**Role:** %s\n\nUsers will get this role when they join the channel.", ch.Name, role.Name), hintVoiceRolesRemove)
}

func handleRemoveVoice(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate) {
	settings, err := commands.GetSettings(ctx, db, i.GuildID)
	if err != nil {
		commands.RespondEmbed(s, i, embedTitleVoiceRoles, "Could not load guild settings.")
		return
	}

	data := i.ApplicationCommandData()
	optCh := commands.ParseOption(data.Options, "channel")
	if optCh == nil {
		commands.RespondEmbed(s, i, embedTitleVoiceRoles, "Please provide a channel.")
		return
	}
	ch := optCh.ChannelValue(s)
	if ch == nil {
		commands.RespondEmbed(s, i, embedTitleVoiceRoles, "Channel not found.")
		return
	}

	optRole := commands.ParseOption(data.Options, "role")
	var roleID string
	if optRole != nil {
		role := optRole.RoleValue(s, i.GuildID)
		if role != nil {
			roleID = role.ID
		}
	}

	channelIdx := -1
	for j, vc := range settings.VoiceChas {
		if vc.ID == ch.ID {
			channelIdx = j
			break
		}
	}
	if channelIdx < 0 {
		commands.RespondEmbed(s, i, embedTitleVoiceRoles, fmt.Sprintf("Channel `%s` is not in the voice roles list.", ch.Name))
		return
	}

	if roleID != "" {
		roles := settings.VoiceChas[channelIdx].Roles
		roleIdx := -1
		for k, r := range roles {
			if r.ID == roleID {
				roleIdx = k
				break
			}
		}
		if roleIdx < 0 {
			commands.RespondEmbed(s, i, embedTitleVoiceRoles, "That role is not set for this channel.")
			return
		}
		settings.VoiceChas[channelIdx].Roles = append(roles[:roleIdx], roles[roleIdx+1:]...)
		if len(settings.VoiceChas[channelIdx].Roles) == 0 {
			settings.VoiceChas = append(settings.VoiceChas[:channelIdx], settings.VoiceChas[channelIdx+1:]...)
		}
	} else {
		settings.VoiceChas = append(settings.VoiceChas[:channelIdx], settings.VoiceChas[channelIdx+1:]...)
	}
	if err := db.Guilds().SetGuildSettings(ctx, i.GuildID, settings); err != nil {
		commands.RespondEmbed(s, i, embedTitleVoiceRoles, "Could not save settings.")
		return
	}
	if roleID != "" {
		commands.RespondEmbed(s, i, embedTitleVoiceRoles, fmt.Sprintf("Role removed from channel `%s`.", ch.Name), hintVoiceRolesListAdd)
	} else {
		commands.RespondEmbed(s, i, embedTitleVoiceRoles, fmt.Sprintf("All roles removed from channel `%s`.", ch.Name), hintVoiceRolesListAdd)
	}
}

func handleVoiceRoles(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate) {
	settings, err := commands.GetSettings(ctx, db, i.GuildID)
	if err != nil {
		commands.RespondEmbed(s, i, embedTitleVoiceRoles, "Could not load guild settings.")
		return
	}

	if len(settings.VoiceChas) == 0 {
		commands.RespondEmbed(s, i, embedTitleVoiceRoles, "No voice channel roles are set.", hintVoiceRolesEmpty)
		return
	}
	fields := make([]*discordgo.MessageEmbedField, 0, len(settings.VoiceChas))
	for _, vc := range settings.VoiceChas {
		var rolesList string
		if len(vc.Roles) == 0 {
			rolesList = "*No roles*"
		} else {
			for _, r := range vc.Roles {
				rolesList += fmt.Sprintf("• %s\n", r.Name)
			}
		}
		fields = append(fields, &discordgo.MessageEmbedField{
			Name:   vc.Name,
			Value:  rolesList,
			Inline: false,
		})
	}
	commands.RespondEmbedWithFields(s, i, embedTitleVoiceRoles, "Channels and the roles given when users join.", fields, hintVoiceRoles)
}
