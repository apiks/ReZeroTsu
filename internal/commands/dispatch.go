package commands

import (
	"context"
	"runtime/debug"

	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/database/guilds"
	"ReZeroTsu/internal/discord"
	"ReZeroTsu/internal/interaction"
	"ReZeroTsu/internal/logger"

	"github.com/bwmarrin/discordgo"
)

const needAdminMessage = "You need Administrator or Manage Server to use this command."

const needSettingsAdminMessage = "You need Administrator, Manage Server, or a moderator role to use this command."

// settingsReader is the part of the guild store the bot-side moderator check reads.
type settingsReader interface {
	GetGuildSettings(ctx context.Context, guildID string) (*guilds.GuildSettings, error)
}

// isOwner reports whether the member is the bot owner or the guild owner.
func isOwner(port interaction.Port, i *discordgo.Interaction) bool {
	userID := i.Member.User.ID
	if OwnerID != "" && userID == OwnerID {
		return true
	}
	guild, err := port.Guild(i.GuildID)
	return err == nil && guild != nil && userID == guild.OwnerID
}

func isGuildAdmin(port interaction.Port, i *discordgo.Interaction) bool {
	if i.GuildID == "" || i.Member == nil {
		return false
	}
	if isOwner(port, i) {
		return true
	}
	return i.Member.Permissions&(discordgo.PermissionAdministrator|discordgo.PermissionManageGuild) != 0
}

func isGuildAdminOrMod(ctx context.Context, settings settingsReader, port interaction.Port, i *discordgo.Interaction) bool {
	if isGuildAdmin(port, i) {
		return true
	}
	if i.GuildID == "" || i.Member == nil {
		return false
	}
	gs, err := settings.GetGuildSettings(ctx, i.GuildID)
	if err != nil || gs == nil || len(gs.CommandRoles) == 0 {
		return false
	}
	memberRoleIDs := make(map[string]struct{}, len(i.Member.Roles))
	for _, id := range i.Member.Roles {
		memberRoleIDs[id] = struct{}{}
	}
	for _, r := range gs.CommandRoles {
		if _, ok := memberRoleIDs[r.ID]; ok {
			return true
		}
	}
	return false
}

// IsGuildAdmin reports whether the member is bot owner, guild owner, or has Administrator/Manage Guild.
func IsGuildAdmin(s *discordgo.Session, i *discordgo.InteractionCreate) bool {
	return isGuildAdmin(interaction.NewDiscordgoPort(s, i.Interaction), i.Interaction)
}

// IsGuildAdminOrMod reports whether the member is admin/mod (owner, Administrator/Manage Guild, or command role).
func IsGuildAdminOrMod(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate) bool {
	return isGuildAdminOrMod(ctx, db.Guilds(), interaction.NewDiscordgoPort(s, i.Interaction), i.Interaction)
}

// dispatch runs one slash command interaction. The framework's own refusals (unknown command, wrong context,
// failed bot-side moderator check) are private replies sent before acknowledging; after that the command is
// acknowledged by its declared response mode and visibility, and its handler runs. A failure after the
// acknowledgement is shown privately (see interaction.Context.Refuse). s is only handed to legacy handlers.
// It returns an error only when the acknowledgement itself failed.
func dispatch(ctx context.Context, db *database.Client, settings settingsReader, name string, port interaction.Port, s *discordgo.Session, i *discordgo.InteractionCreate) error {
	cmd, ok := registry[name]
	if !ok {
		refuse(interaction.NewContext(port, i.Interaction, db, interaction.Public, interaction.Direct), "Unknown command: "+name)
		return nil
	}
	ic := interaction.NewContext(port, i.Interaction, db, cmd.Visibility, cmd.Response)

	if msg := contextRefusal(cmd.Context, i.GuildID); msg != "" {
		refuse(ic, msg)
		return nil
	}
	switch cmd.Permission {
	case PermMod:
		if !isGuildAdminOrMod(ctx, settings, port, i.Interaction) {
			refuse(ic, needSettingsAdminMessage)
			return nil
		}
	case PermAdmin:
		if !isGuildAdmin(port, i.Interaction) {
			refuse(ic, needAdminMessage)
			return nil
		}
	}

	if cmd.Response == Deferred {
		if err := ic.Defer(cmd.Visibility); err != nil {
			args := append(discord.RESTAttrs(err), "command", name, "guild_id", i.GuildID, "channel_id", i.ChannelID, "err", err)
			logger.For("commands").Error("deferred ack failed", args...)
			return err
		}
	}

	func() {
		defer func() {
			if r := recover(); r != nil {
				l := logger.For("commands").With("command", name, "guild_id", i.GuildID, "channel_id", i.ChannelID, "panic", r, "stack", string(debug.Stack()))
				l.Error("panic in handler")
				refuse(ic, errMessage)
			}
		}()
		if cmd.Run != nil {
			if err := cmd.Run(ic); err != nil {
				args := append(discord.RESTAttrs(err), "command", name, "guild_id", i.GuildID, "channel_id", i.ChannelID, "err", err)
				logger.For("commands").Error("handler failed", args...)
				refuse(ic, errMessage)
			}
			return
		}
		cmd.Handler(ctx, db, s, i)
	}()
	return nil
}

// refuse shows text only to the invoker and logs when even that fails.
func refuse(ic *interaction.Context, text string) {
	if err := ic.Refuse(text); err != nil {
		args := append(discord.RESTAttrs(err), "guild_id", ic.GuildID, "channel_id", ic.ChannelID, "err", err)
		logger.For("commands").Error("private refusal failed", args...)
	}
}

// OnInteraction dispatches slash commands, pagination, and autocomplete; reports errors privately.
func OnInteraction(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type == discordgo.InteractionMessageComponent && HasPaginationPrefix(i.MessageComponentData().CustomID) {
		HandlePagination(ctx, db, s, i)
		return
	}
	if i.Type == discordgo.InteractionApplicationCommandAutocomplete {
		handleAutocomplete(ctx, db, s, i)
		return
	}
	if i.Type != discordgo.InteractionApplicationCommand {
		return
	}
	data := i.ApplicationCommandData()
	port := interaction.NewDiscordgoPort(s, i.Interaction)
	if err := dispatch(ctx, db, db.Guilds(), data.Name, port, s, i); err != nil {
		// The acknowledgement failed; it is already logged. A private reply is the last attempt to tell the user.
		_ = port.Reply(interaction.Response{Content: errMessage, Visibility: interaction.Private})
	}
}
