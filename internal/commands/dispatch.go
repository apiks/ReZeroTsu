package commands

import (
	"context"
	"runtime/debug"
	"time"

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

// permissionCheckTimeout bounds the guild fetch and the settings read of a permission check. The check runs before
// the acknowledgement, which Discord wants within 3 seconds, and the private refusal needs the interaction still
// open, so a stalled lookup must give up well inside that window. It is a variable so tests can shorten it.
var permissionCheckTimeout = 1500 * time.Millisecond

const msgPermissionCheckTimeout = "I couldn't check your permissions in time. Please try again."

// isGuildAdmin reports whether the member is the bot owner, the guild owner, or has Administrator/Manage Server.
// The cheap checks run first so the guild fetch is only spent on a member they do not settle. The error is only
// ever ctx's, set when the guild fetch could not finish: the answer is unknown and the caller fails closed.
func isGuildAdmin(ctx context.Context, port interaction.Port, i *discordgo.Interaction) (bool, error) {
	if i.GuildID == "" || i.Member == nil {
		return false, nil
	}
	userID := i.Member.User.ID
	if OwnerID != "" && userID == OwnerID {
		return true, nil
	}
	if i.Member.Permissions&(discordgo.PermissionAdministrator|discordgo.PermissionManageGuild) != 0 {
		return true, nil
	}
	guild, err := port.Guild(ctx, i.GuildID)
	if err != nil {
		return false, ctx.Err()
	}
	return guild != nil && userID == guild.OwnerID, nil
}

// isGuildAdminOrMod reports whether the member is an administrator (see isGuildAdmin) or a Moderator through a
// role the server granted bot-side. The error is only ever ctx's, as for isGuildAdmin.
func isGuildAdminOrMod(ctx context.Context, settings settingsReader, port interaction.Port, i *discordgo.Interaction) (bool, error) {
	if admin, err := isGuildAdmin(ctx, port, i); admin || err != nil {
		return admin, err
	}
	if i.GuildID == "" || i.Member == nil {
		return false, nil
	}
	gs, err := settings.GetGuildSettings(ctx, i.GuildID)
	if err != nil {
		return false, ctx.Err()
	}
	if gs == nil || len(gs.CommandRoles) == 0 {
		return false, nil
	}
	memberRoleIDs := make(map[string]struct{}, len(i.Member.Roles))
	for _, id := range i.Member.Roles {
		memberRoleIDs[id] = struct{}{}
	}
	for _, r := range gs.CommandRoles {
		if _, ok := memberRoleIDs[r.ID]; ok {
			return true, nil
		}
	}
	return false, nil
}

// IsGuildAdmin reports whether the member is bot owner, guild owner, or has Administrator/Manage Guild. An
// unknown answer (the lookup did not finish) is false.
func IsGuildAdmin(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) bool {
	ok, _ := isGuildAdmin(ctx, interaction.NewDiscordgoPort(s, i.Interaction), i.Interaction)
	return ok
}

// IsGuildAdminOrMod reports whether the member is an administrator or a Moderator (owner, Administrator/Manage
// Guild, or a role granted bot-side). An unknown answer is false.
func IsGuildAdminOrMod(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate) bool {
	ok, _ := isGuildAdminOrMod(ctx, db.Guilds(), interaction.NewDiscordgoPort(s, i.Interaction), i.Interaction)
	return ok
}

// checkPermission runs the command's permission check under permissionCheckTimeout. refusal is the private text
// to show when the member may not run the command, empty when they may.
func checkPermission(ctx context.Context, level PermissionLevel, settings settingsReader, port interaction.Port, i *discordgo.Interaction) (refusal string) {
	if level != PermMod && level != PermAdmin {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, permissionCheckTimeout)
	defer cancel()
	var ok bool
	var err error
	denied := needAdminMessage
	if level == PermMod {
		ok, err = isGuildAdminOrMod(ctx, settings, port, i)
		denied = needSettingsAdminMessage
	} else {
		ok, err = isGuildAdmin(ctx, port, i)
	}
	switch {
	case err != nil:
		logger.For("commands").Error("permission check did not finish", "guild_id", i.GuildID, "channel_id", i.ChannelID, "err", err)
		return msgPermissionCheckTimeout
	case !ok:
		return denied
	}
	return ""
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
	ic.Ctx = ctx

	if msg := contextRefusal(cmd.Context, i.GuildID); msg != "" {
		refuse(ic, msg)
		return nil
	}
	if msg := checkPermission(ctx, cmd.Permission, settings, port, i.Interaction); msg != "" {
		refuse(ic, msg)
		return nil
	}

	l, found := cmd.resolve(ic.Command.Options)
	if !found {
		refuse(ic, "Unknown command: "+name)
		return nil
	}
	if l.sub != nil {
		ic.Visibility, ic.Mode = l.visibility, l.response
		ic.Group, ic.Subcommand, ic.Command.Options = l.group, l.subcommand, l.options
	}

	if l.response == Deferred {
		if err := ic.Defer(l.visibility); err != nil {
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
		if l.run != nil {
			if err := l.run(ic); err != nil {
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

// OnInteraction dispatches slash commands, buttons, pickers, forms and autocomplete; reports errors privately.
func OnInteraction(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type == discordgo.InteractionMessageComponent || i.Type == discordgo.InteractionModalSubmit {
		dispatchComponent(ctx, db, interaction.NewDiscordgoPort(s, i.Interaction), s, i)
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
