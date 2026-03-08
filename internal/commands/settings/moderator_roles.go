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
	embedTitleModRoles      = "Moderator Roles"
	hintModRoles            = "Use /add-moderator-role to add, /remove-moderator-role to remove."
	hintModRolesListRemove  = "Use /moderator-roles to list all, /remove-moderator-role to remove."
	hintModRolesList        = "Use /moderator-roles to list all."
	hintModRolesAddOne     = "Use /add-moderator-role to add one."
)

func init() {
	commands.Add(&commands.Command{
		Name:       "add-moderator-role",
		Desc:       "Add a role as a moderator (command) role.",
		Permission: commands.PermMod,
		Context:    commands.ContextGuildOnly,
		Module:     "settings",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionRole,
				Name:        "role",
				Description: "The role to add as a moderator.",
				Required:    true,
			},
		},
		Handler: handleAddModeratorRole,
	})
	commands.Add(&commands.Command{
		Name:       "remove-moderator-role",
		Desc:       "Remove a role from the moderator list.",
		Permission: commands.PermMod,
		Context:    commands.ContextGuildOnly,
		Module:     "settings",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionRole,
				Name:        "role",
				Description: "The role to remove from moderators.",
				Required:    true,
			},
		},
		Handler: handleRemoveModeratorRole,
	})
	commands.Add(&commands.Command{
		Name:       "moderator-roles",
		Desc:       "List all moderator (command) roles.",
		Permission: commands.PermMod,
		Context:    commands.ContextGuildOnly,
		Module:     "settings",
		Handler:    handleModeratorRoles,
	})
}

func handleAddModeratorRole(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate) {
	settings, err := commands.GetSettings(ctx, db, i.GuildID)
	if err != nil {
		commands.RespondEmbed(s, i, embedTitleModRoles, "Could not load guild settings.")
		return
	}

	data := i.ApplicationCommandData()
	opt := commands.ParseOption(data.Options, "role")
	if opt == nil {
		commands.RespondEmbed(s, i, embedTitleModRoles, "Please provide a role.")
		return
	}
	role := opt.RoleValue(s, i.GuildID)
	if role == nil {
		commands.RespondEmbed(s, i, embedTitleModRoles, "That role was not found.")
		return
	}

	for _, r := range settings.CommandRoles {
		if r.ID == role.ID {
			commands.RespondEmbed(s, i, embedTitleModRoles, fmt.Sprintf("Role `%s` is already a moderator role.", role.Name))
			return
		}
	}
	settings.CommandRoles = append(settings.CommandRoles, guilds.Role{
		Name: role.Name, ID: role.ID, Position: role.Position,
	})
	if err := db.Guilds().SetGuildSettings(ctx, i.GuildID, settings); err != nil {
		commands.RespondEmbed(s, i, embedTitleModRoles, "Could not save settings.")
		return
	}
	commands.RespondEmbed(s, i, embedTitleModRoles, fmt.Sprintf("**Added:** %s\n\nThis role can now use settings commands.", role.Name), hintModRolesListRemove)
}

func handleRemoveModeratorRole(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate) {
	settings, err := commands.GetSettings(ctx, db, i.GuildID)
	if err != nil {
		commands.RespondEmbed(s, i, embedTitleModRoles, "Could not load guild settings.")
		return
	}

	data := i.ApplicationCommandData()
	opt := commands.ParseOption(data.Options, "role")
	if opt == nil {
		commands.RespondEmbed(s, i, embedTitleModRoles, "Please provide a role.")
		return
	}
	role := opt.RoleValue(s, i.GuildID)
	if role == nil {
		commands.RespondEmbed(s, i, embedTitleModRoles, "That role was not found.")
		return
	}

	idx := -1
	for j, r := range settings.CommandRoles {
		if r.ID == role.ID {
			idx = j
			break
		}
	}
	if idx < 0 {
		commands.RespondEmbed(s, i, embedTitleModRoles, fmt.Sprintf("Role `%s` is not in the moderator list.", role.Name))
		return
	}
	settings.CommandRoles = append(settings.CommandRoles[:idx], settings.CommandRoles[idx+1:]...)
	if err := db.Guilds().SetGuildSettings(ctx, i.GuildID, settings); err != nil {
		commands.RespondEmbed(s, i, embedTitleModRoles, "Could not save settings.")
		return
	}
	commands.RespondEmbed(s, i, embedTitleModRoles, fmt.Sprintf("**Removed:** %s", role.Name), hintModRolesList)
}

func handleModeratorRoles(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate) {
	settings, err := commands.GetSettings(ctx, db, i.GuildID)
	if err != nil {
		commands.RespondEmbed(s, i, embedTitleModRoles, "Could not load guild settings.")
		return
	}

	if len(settings.CommandRoles) == 0 {
		commands.RespondEmbed(s, i, embedTitleModRoles, "No moderator roles are set.", hintModRolesAddOne)
		return
	}
	fields := make([]*discordgo.MessageEmbedField, 0, len(settings.CommandRoles))
	for _, r := range settings.CommandRoles {
		fields = append(fields, &discordgo.MessageEmbedField{
			Name:   r.Name,
			Value:  fmt.Sprintf("`%s`", r.ID),
			Inline: true,
		})
	}
	commands.RespondEmbedWithFields(s, i, embedTitleModRoles, "Roles that can use settings commands.", fields, hintModRoles)
}
