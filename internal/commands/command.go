package commands

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"sort"
	"strings"
	"time"

	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/discord"
	"ReZeroTsu/internal/logger"

	"github.com/bwmarrin/discordgo"
)

// ErrUnknownCommand is returned by Handle when the command isn't registered.
var ErrUnknownCommand = errors.New("unknown command")

const errMessage = "Something went wrong."

// PermissionLevel controls who can run a command.
type PermissionLevel int

const (
	PermEveryone PermissionLevel = iota // no check
	PermMod                             // guild admin or moderator role (RequireGuildAdminOrMod)
	PermAdmin                           // guild admin only (RequireGuildAdmin)
	PermOwner                           // bot owner only
)

// BotRuntime provides guild/shard count and uptime for message commands.
type BotRuntime interface {
	GuildCount() int
	ShardCount() int
	Uptime() time.Duration
}

// AllowedContext restricts where a command can run (DMs, guilds, or both).
type AllowedContext int

const (
	ContextBoth      AllowedContext = iota // default: usable in DMs and guilds
	ContextDMOnly                          // only in DMs
	ContextGuildOnly                       // only in guilds
)

// Command defines a slash command. Handlers must not ack; the framework defers first.
type Command struct {
	Name               string
	Desc               string
	Options            []*discordgo.ApplicationCommandOption // optional; can be nil
	Permission         PermissionLevel                       // default PermEveryone
	Context            AllowedContext                        // default ContextBoth
	Module             string                                // e.g. "settings", "general"; metadata only
	Handler            func(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate)
	AutocompleteOption func(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate, optionName, focusedValue string) []*discordgo.ApplicationCommandOptionChoice
}

var registry = make(map[string]*Command)

type MessageCommand struct {
	Name           string
	Aliases        []string
	Permission     PermissionLevel
	Context        AllowedContext // default ContextBoth
	MessageHandler func(ctx context.Context, db *database.Client, s *discordgo.Session, m *discordgo.MessageCreate, args string, runtime BotRuntime)
}

var messageRegistry = make(map[string]*MessageCommand)

func AddMessageCommand(cmd *MessageCommand) {
	if cmd == nil || cmd.Name == "" || cmd.MessageHandler == nil {
		panic("commands: AddMessageCommand requires non-nil MessageCommand with Name and MessageHandler")
	}
	messageRegistry[cmd.Name] = cmd
	for _, a := range cmd.Aliases {
		if a != "" {
			messageRegistry[a] = cmd
		}
	}
}

// HandleMessageCommand parses a prefix command and dispatches.
func HandleMessageCommand(ctx context.Context, db *database.Client, s *discordgo.Session, m *discordgo.MessageCreate, ownerID string, prefixes []string, runtime BotRuntime) bool {
	if m.Author == nil {
		return false
	}
	if m.Author.Bot {
		return false
	}
	content := strings.TrimSpace(m.Content)
	if content == "" {
		return false
	}
	// Match longest prefix first so e.g. ".!" matches before "."
	matchedPrefix := ""
	for _, p := range prefixes {
		if p != "" && strings.HasPrefix(content, p) {
			if len(p) > len(matchedPrefix) {
				matchedPrefix = p
			}
		}
	}
	if matchedPrefix == "" {
		return false
	}
	rest := strings.TrimSpace(content[len(matchedPrefix):])
	if rest == "" {
		return false
	}
	parts := strings.SplitN(rest, " ", 2)
	name := strings.ToLower(strings.TrimSpace(parts[0]))
	args := ""
	if len(parts) > 1 {
		args = strings.TrimSpace(parts[1])
	}
	cmd, ok := messageRegistry[name]
	if !ok {
		return false
	}
	if cmd.Permission == PermOwner && m.Author.ID != ownerID {
		_, _ = s.ChannelMessageSend(m.ChannelID, "Only the bot owner can use this command.")
		return true
	}
	if cmd.Context == ContextGuildOnly && m.GuildID == "" {
		_, _ = s.ChannelMessageSend(m.ChannelID, "This command can only be used in a server.")
		return true
	}
	func() {
		defer func() {
			if r := recover(); r != nil {
				l := logger.For("commands").With("command", name, "guild_id", m.GuildID, "channel_id", m.ChannelID, "panic", r, "stack", string(debug.Stack()))
				l.Error("panic in message command")
				_, _ = s.ChannelMessageSend(m.ChannelID, errMessage)
			}
		}()
		cmd.MessageHandler(ctx, db, s, m, args, runtime)
	}()
	return true
}

var OwnerID string

// SetOwnerID sets the bot owner ID for permission checks. Call once at startup.
func SetOwnerID(id string) {
	OwnerID = id
}

// AllCommands returns all registered commands sorted by name.
func AllCommands() []*Command {
	out := make([]*Command, 0, len(registry))
	for _, c := range registry {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Add registers a command. Safe from init(). Panics if Name empty or Handler nil.
func Add(cmd *Command) {
	if cmd == nil || cmd.Name == "" || cmd.Handler == nil {
		panic("commands: Add requires non-nil Command with Name and Handler")
	}
	registry[cmd.Name] = cmd
}

func contextsForAllowedContext(ctx AllowedContext) *[]discordgo.InteractionContextType {
	switch ctx {
	case ContextGuildOnly:
		return &[]discordgo.InteractionContextType{discordgo.InteractionContextGuild}
	case ContextDMOnly:
		return &[]discordgo.InteractionContextType{discordgo.InteractionContextBotDM}
	case ContextBoth:
		return &[]discordgo.InteractionContextType{discordgo.InteractionContextGuild, discordgo.InteractionContextBotDM}
	default:
		return &[]discordgo.InteractionContextType{discordgo.InteractionContextGuild, discordgo.InteractionContextBotDM}
	}
}

func defaultMemberPermissionsForLevel(perm PermissionLevel) *int64 {
	if perm != PermAdmin {
		return nil
	}
	v := int64(discordgo.PermissionAdministrator)
	return &v
}

// Definitions returns slash command definitions for bulk overwrite.
func Definitions() []*discordgo.ApplicationCommand {
	defs := make([]*discordgo.ApplicationCommand, 0, len(registry))
	for _, cmd := range registry {
		c := &discordgo.ApplicationCommand{
			Name:        cmd.Name,
			Description: cmd.Desc,
			Contexts:    contextsForAllowedContext(cmd.Context),
		}
		if len(cmd.Options) > 0 {
			c.Options = cmd.Options
		}
		if defPerms := defaultMemberPermissionsForLevel(cmd.Permission); defPerms != nil {
			c.DefaultMemberPermissions = defPerms
		}
		defs = append(defs, c)
	}
	sort.Slice(defs, func(i, j int) bool { return defs[i].Name < defs[j].Name })
	return defs
}

// Handle looks up the command, defers the response, then runs the handler.
func Handle(ctx context.Context, db *database.Client, name string, s *discordgo.Session, i *discordgo.InteractionCreate) error {
	cmd, ok := registry[name]
	if !ok {
		return ErrUnknownCommand
	}

	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{},
	})
	if err != nil {
		args := append(discord.RESTAttrs(err), "command", name, "guild_id", i.GuildID, "channel_id", i.ChannelID, "err", err)
		logger.For("commands").Error("deferred ack failed", args...)
		return err
	}

	if cmd.Context == ContextDMOnly && i.GuildID != "" {
		_ = editResponse(s, i, "This command can only be used in DMs.")
		return nil
	}
	if cmd.Context == ContextGuildOnly && i.GuildID == "" {
		_ = editResponse(s, i, "This command can only be used in a server.")
		return nil
	}

	// Permission check (after ack, before handler)
	switch cmd.Permission {
	case PermMod:
		if !RequireGuildAdminOrMod(ctx, db, s, i) {
			return nil
		}
	case PermAdmin:
		if !RequireGuildAdmin(s, i) {
			return nil
		}
	}

	var handlerErr error
	func() {
		defer func() {
			if r := recover(); r != nil {
				l := logger.For("commands").With("command", name, "guild_id", i.GuildID, "channel_id", i.ChannelID, "panic", r, "stack", string(debug.Stack()))
				l.Error("panic in handler")
				handlerErr = editResponse(s, i, errMessage)
			}
		}()
		cmd.Handler(ctx, db, s, i)
	}()

	return handlerErr
}

func editResponse(s *discordgo.Session, i *discordgo.InteractionCreate, content string) error {
	emb := NewEmbed(s, "Notice", content)
	_, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Embeds: &[]*discordgo.MessageEmbed{emb}})
	return err
}

const needAdminMessage = "You need Administrator or Manage Server to use this command."

const needSettingsAdminMessage = "You need Administrator, Manage Server, or a moderator role to use this command."

// IsGuildAdmin reports whether the member is bot owner, guild owner, or has Administrator/Manage Guild.
func IsGuildAdmin(s *discordgo.Session, i *discordgo.InteractionCreate) bool {
	if i.GuildID == "" || i.Member == nil {
		return false
	}
	userID := i.Member.User.ID
	if OwnerID != "" && userID == OwnerID {
		return true
	}
	guild, err := s.State.Guild(i.GuildID)
	if err != nil {
		guild, err = s.Guild(i.GuildID)
		if err == nil && guild != nil && userID == guild.OwnerID {
			return true
		}
	} else if guild != nil && userID == guild.OwnerID {
		return true
	}
	perm := i.Member.Permissions
	return perm&(discordgo.PermissionAdministrator|discordgo.PermissionManageGuild) != 0
}

// IsGuildAdminOrMod reports whether the member is admin/mod (owner, Administrator/Manage Guild, or command role).
func IsGuildAdminOrMod(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate) bool {
	if i.GuildID == "" || i.Member == nil {
		return false
	}
	userID := i.Member.User.ID
	if OwnerID != "" && userID == OwnerID {
		return true
	}
	guild, err := s.State.Guild(i.GuildID)
	if err != nil {
		guild, err = s.Guild(i.GuildID)
		if err == nil && guild != nil && userID == guild.OwnerID {
			return true
		}
	} else if guild != nil && userID == guild.OwnerID {
		return true
	}
	perm := i.Member.Permissions
	if perm&(discordgo.PermissionAdministrator|discordgo.PermissionManageGuild) != 0 {
		return true
	}
	settings, err := db.Guilds().GetGuildSettings(ctx, i.GuildID)
	if err != nil || settings == nil || len(settings.CommandRoles) == 0 {
		return false
	}
	memberRoleIDs := make(map[string]struct{})
	for _, id := range i.Member.Roles {
		memberRoleIDs[id] = struct{}{}
	}
	for _, r := range settings.CommandRoles {
		if _, ok := memberRoleIDs[r.ID]; ok {
			return true
		}
	}
	return false
}

// RequireGuildAdmin checks admin; if not, edits response.
func RequireGuildAdmin(s *discordgo.Session, i *discordgo.InteractionCreate) bool {
	if IsGuildAdmin(s, i) {
		return true
	}
	_ = editResponse(s, i, needAdminMessage)
	return false
}

// RequireGuildAdminOrMod checks admin or mod role; if not, edits response.
func RequireGuildAdminOrMod(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate) bool {
	if IsGuildAdminOrMod(ctx, db, s, i) {
		return true
	}
	_ = editResponse(s, i, needSettingsAdminMessage)
	return false
}

func RespondEphemeral(s *discordgo.Session, i *discordgo.InteractionCreate, content string) error {
	return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: content,
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})
}

const maxAutocompleteChoices = 25

func focusedOption(opts []*discordgo.ApplicationCommandInteractionDataOption) *discordgo.ApplicationCommandInteractionDataOption {
	for _, o := range opts {
		if o != nil && o.Focused {
			return o
		}
	}
	return nil
}

func optionAllowsAutocomplete(cmd *Command, optionName string) bool {
	for _, o := range cmd.Options {
		if o != nil && o.Name == optionName && o.Autocomplete {
			return true
		}
	}
	return false
}

// OnInteraction dispatches slash commands, pagination, and autocomplete; reports errors ephemerally.
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
	if err := Handle(ctx, db, data.Name, s, i); err != nil {
		if errors.Is(err, ErrUnknownCommand) {
			_ = RespondEphemeral(s, i, fmt.Sprintf("Unknown command: %s", data.Name))
		} else {
			args := append(discord.RESTAttrs(err), "command", data.Name, "guild_id", i.GuildID, "channel_id", i.ChannelID, "err", err)
			logger.For("commands").Error("Handle failed", args...)
			_ = RespondEphemeral(s, i, "Something went wrong.")
		}
	}
}

func handleAutocomplete(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ApplicationCommandData()
	cmd, ok := registry[data.Name]
	if !ok || cmd.AutocompleteOption == nil || len(data.Options) == 0 {
		respondAutocompleteChoices(s, i, nil)
		return
	}
	focused := focusedOption(data.Options)
	if focused == nil {
		respondAutocompleteChoices(s, i, nil)
		return
	}
	if !optionAllowsAutocomplete(cmd, focused.Name) {
		respondAutocompleteChoices(s, i, nil)
		return
	}
	focusedValue := ""
	if focused.Value != nil {
		if sv, ok := focused.Value.(string); ok {
			focusedValue = sv
		}
	}
	choices := cmd.AutocompleteOption(ctx, db, s, i, focused.Name, focusedValue)
	if len(choices) > maxAutocompleteChoices {
		choices = choices[:maxAutocompleteChoices]
	}
	respondAutocompleteChoices(s, i, choices)
}

func respondAutocompleteChoices(s *discordgo.Session, i *discordgo.InteractionCreate, choices []*discordgo.ApplicationCommandOptionChoice) {
	if choices == nil {
		choices = []*discordgo.ApplicationCommandOptionChoice{}
	}
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionApplicationCommandAutocompleteResult,
		Data: &discordgo.InteractionResponseData{Choices: choices},
	})
	if err != nil {
		args := append(discord.RESTAttrs(err), "command", i.ApplicationCommandData().Name, "guild_id", i.GuildID, "channel_id", i.ChannelID, "err", err)
		logger.For("commands").Error("autocomplete respond failed", args...)
	}
}
