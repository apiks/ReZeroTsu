package commands

import (
	"context"
	"runtime/debug"
	"sort"
	"strings"
	"time"

	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/discord"
	"ReZeroTsu/internal/interaction"
	"ReZeroTsu/internal/logger"

	"github.com/bwmarrin/discordgo"
)

const errMessage = "Something went wrong."

// Visibility and response-mode declarations, re-exported so command files declare them without a second import.
const (
	Public   = interaction.Public
	Private  = interaction.Private
	Direct   = interaction.Direct
	Deferred = interaction.Deferred
)

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

// contextRefusal returns the refusal message when a command with the given
// allowed context may not run in the channel identified by guildID (empty for
// DMs), or "" when it may run.
func contextRefusal(ctx AllowedContext, guildID string) string {
	switch {
	case ctx == ContextDMOnly && guildID != "":
		return "This command can only be used in DMs."
	case ctx == ContextGuildOnly && guildID == "":
		return "This command can only be used in a server."
	}
	return ""
}

// Command defines a slash command. Every command declares its Visibility and Response mode, and has exactly one
// handler form:
//   - Run, the new form: it receives the interaction context and reaches Discord only through its port. With
//     Response Deferred the framework acknowledges first with the declared visibility; with Direct, Run's first
//     reply is the acknowledgement.
//   - Handler, the legacy form: it edits the deferred reply on the raw session, so it must be Deferred. Legacy
//     commands are declared Public and Deferred until their area spec migrates them to Run.
type Command struct {
	Name               string
	Desc               string
	Options            []*discordgo.ApplicationCommandOption // optional; can be nil
	Permission         PermissionLevel                       // default PermEveryone
	Context            AllowedContext                        // default ContextBoth
	Module             string                                // e.g. "settings", "general"; metadata only
	Visibility         interaction.Visibility                // required: Public or Private
	Response           interaction.ResponseMode              // required: Direct or Deferred
	Run                func(ic *interaction.Context) error
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
	if msg := contextRefusal(cmd.Context, m.GuildID); msg != "" {
		_, _ = s.ChannelMessageSend(m.ChannelID, msg)
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

// Add registers a command. Safe from init(). Panics if the command has no name, no visibility or response mode,
// not exactly one handler form, or a legacy Handler that is not Deferred.
func Add(cmd *Command) {
	if cmd == nil || cmd.Name == "" {
		panic("commands: Add requires non-nil Command with Name")
	}
	if cmd.Visibility != Public && cmd.Visibility != Private {
		panic("commands: " + cmd.Name + " must declare Visibility")
	}
	if cmd.Response != Direct && cmd.Response != Deferred {
		panic("commands: " + cmd.Name + " must declare Response")
	}
	if (cmd.Handler == nil) == (cmd.Run == nil) {
		panic("commands: " + cmd.Name + " needs exactly one of Handler and Run")
	}
	if cmd.Handler != nil && cmd.Response != Deferred {
		panic("commands: " + cmd.Name + " uses the legacy Handler, which edits the deferred reply, so it must be Deferred")
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
