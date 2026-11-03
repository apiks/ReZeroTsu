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
	ContextBoth       AllowedContext = iota // default: usable in DMs and guilds
	ContextDMOnly                           // only in DMs
	ContextGuildOnly                        // only in guilds
	ContextEverywhere                       // guilds, DMs with the bot and private channels, for user-installed commands
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
//
// A command is a slash command (Kind KindSlash, the default), a user right-click command (KindUser) or a message
// right-click command (KindMessage). A slash command is either flat (Options and Run) or a group: Subcommands
// and Groups, each subcommand with its own Run, options and autocomplete, and no Run or Options on the command.
// A right-click command has no description, options or subcommands, and its Run reads the target with
// ic.TargetUser or ic.TargetMessage.
type Command struct {
	Name string
	Desc string
	Kind CommandKind
	// Options is optional and can be nil; a command with subcommands has none.
	Options     []*discordgo.ApplicationCommandOption
	Subcommands []*Subcommand
	Groups      []*SubcommandGroup
	Permission  PermissionLevel // default PermEveryone
	Context     AllowedContext  // default ContextBoth
	// DefaultMemberPermissions, when set, is registered as the command's default member permissions and wins
	// over the one implied by Permission. Discord applies it to the whole top-level command.
	DefaultMemberPermissions *int64
	// Installs lists where the command can be installed. Nil leaves it to the application's defaults.
	Installs []discordgo.ApplicationIntegrationType
	Module   string // e.g. "settings", "general"; metadata only
	// Visibility and Response are required; subcommands inherit them unless they declare their own.
	Visibility         interaction.Visibility
	Response           interaction.ResponseMode
	Run                func(ic *interaction.Context) error
	Handler            func(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate)
	AutocompleteOption func(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate, optionName, focusedValue string) []*discordgo.ApplicationCommandOptionChoice
}

// CommandKind says how a command is invoked.
type CommandKind int

const (
	KindSlash   CommandKind = iota // typed as /name; the default
	KindUser                       // right-click a user
	KindMessage                    // right-click a message
)

// Subcommand is one subcommand of a group command. A zero Visibility or Response inherits the command's.
type Subcommand struct {
	Name               string
	Desc               string
	Options            []*discordgo.ApplicationCommandOption
	Visibility         interaction.Visibility
	Response           interaction.ResponseMode
	Run                func(ic *interaction.Context) error
	AutocompleteOption func(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate, optionName, focusedValue string) []*discordgo.ApplicationCommandOptionChoice
}

// SubcommandGroup nests subcommands one level deeper: /command group subcommand.
type SubcommandGroup struct {
	Name        string
	Desc        string
	Subcommands []*Subcommand
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
	if cmd.Installs != nil && len(cmd.Installs) == 0 {
		panic("commands: " + cmd.Name + " declares Installs but lists no install type")
	}
	if cmd.isGroup() {
		validateGroup(cmd)
		registry[cmd.Name] = cmd
		return
	}
	if cmd.Kind != KindSlash && (cmd.Handler != nil || cmd.Desc != "" || len(cmd.Options) > 0) {
		panic("commands: " + cmd.Name + " is a right-click command: it needs Run and no description or options")
	}
	if (cmd.Handler == nil) == (cmd.Run == nil) {
		panic("commands: " + cmd.Name + " needs exactly one of Handler and Run")
	}
	if cmd.Handler != nil && cmd.Response != Deferred {
		panic("commands: " + cmd.Name + " uses the legacy Handler, which edits the deferred reply, so it must be Deferred")
	}
	registry[cmd.Name] = cmd
}

func (c *Command) isGroup() bool { return len(c.Subcommands) > 0 || len(c.Groups) > 0 }

func validateGroup(cmd *Command) {
	if cmd.Kind != KindSlash {
		panic("commands: " + cmd.Name + " is a right-click command and cannot have subcommands")
	}
	if cmd.Run != nil || cmd.Handler != nil || len(cmd.Options) > 0 {
		panic("commands: " + cmd.Name + " has subcommands, so the command itself has no Run, Handler or Options")
	}
	check := func(s *Subcommand) {
		if s == nil || s.Name == "" || s.Run == nil {
			panic("commands: " + cmd.Name + " has a subcommand without a name or a Run")
		}
	}
	for _, s := range cmd.Subcommands {
		check(s)
	}
	for _, g := range cmd.Groups {
		if g == nil || g.Name == "" || len(g.Subcommands) == 0 {
			panic("commands: " + cmd.Name + " has a subcommand group without a name or subcommands")
		}
		for _, s := range g.Subcommands {
			check(s)
		}
	}
}

// leaf is what a slash interaction resolves to: the handler to run and the options it was given.
type leaf struct {
	group, subcommand string
	visibility        interaction.Visibility
	response          interaction.ResponseMode
	run               func(ic *interaction.Context) error
	options           []*discordgo.ApplicationCommandInteractionDataOption
	sub               *Subcommand // nil for a flat command
}

// resolve finds the handler for the given interaction options. ok is false when the interaction names a
// subcommand or group the command does not declare.
func (c *Command) resolve(opts []*discordgo.ApplicationCommandInteractionDataOption) (l leaf, ok bool) {
	if !c.isGroup() {
		return leaf{visibility: c.Visibility, response: c.Response, run: c.Run, options: opts}, true
	}
	if len(opts) == 0 || opts[0] == nil {
		return leaf{}, false
	}
	first := opts[0]
	var group string
	if first.Type == discordgo.ApplicationCommandOptionSubCommandGroup {
		if len(first.Options) == 0 || first.Options[0] == nil {
			return leaf{}, false
		}
		group, first = first.Name, first.Options[0]
	}
	var found *Subcommand
	if group == "" {
		for _, s := range c.Subcommands {
			if s.Name == first.Name {
				found = s
			}
		}
	} else {
		for _, g := range c.Groups {
			if g.Name != group {
				continue
			}
			for _, s := range g.Subcommands {
				if s.Name == first.Name {
					found = s
				}
			}
		}
	}
	if found == nil {
		return leaf{}, false
	}
	l = leaf{group: group, subcommand: found.Name, visibility: found.Visibility, response: found.Response, run: found.Run, options: first.Options, sub: found}
	if l.visibility == 0 {
		l.visibility = c.Visibility
	}
	if l.response == 0 {
		l.response = c.Response
	}
	return l, true
}

func subcommandOption(s *Subcommand) *discordgo.ApplicationCommandOption {
	return &discordgo.ApplicationCommandOption{Name: s.Name, Description: s.Desc, Type: discordgo.ApplicationCommandOptionSubCommand, Options: s.Options}
}

func groupOptions(cmd *Command) []*discordgo.ApplicationCommandOption {
	var out []*discordgo.ApplicationCommandOption
	for _, s := range cmd.Subcommands {
		out = append(out, subcommandOption(s))
	}
	for _, g := range cmd.Groups {
		opt := &discordgo.ApplicationCommandOption{Name: g.Name, Description: g.Desc, Type: discordgo.ApplicationCommandOptionSubCommandGroup}
		for _, s := range g.Subcommands {
			opt.Options = append(opt.Options, subcommandOption(s))
		}
		out = append(out, opt)
	}
	return out
}

func contextsForAllowedContext(ctx AllowedContext) *[]discordgo.InteractionContextType {
	switch ctx {
	case ContextGuildOnly:
		return &[]discordgo.InteractionContextType{discordgo.InteractionContextGuild}
	case ContextDMOnly:
		return &[]discordgo.InteractionContextType{discordgo.InteractionContextBotDM}
	case ContextEverywhere:
		return &[]discordgo.InteractionContextType{discordgo.InteractionContextGuild, discordgo.InteractionContextBotDM, discordgo.InteractionContextPrivateChannel}
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
		switch cmd.Kind {
		case KindUser:
			c.Type = discordgo.UserApplicationCommand
		case KindMessage:
			c.Type = discordgo.MessageApplicationCommand
		}
		switch {
		case cmd.isGroup():
			c.Options = groupOptions(cmd)
		case len(cmd.Options) > 0:
			c.Options = cmd.Options
		}
		if defPerms := defaultMemberPermissionsForLevel(cmd.Permission); defPerms != nil {
			c.DefaultMemberPermissions = defPerms
		}
		if cmd.DefaultMemberPermissions != nil {
			c.DefaultMemberPermissions = cmd.DefaultMemberPermissions
		}
		if cmd.Installs != nil {
			installs := append([]discordgo.ApplicationIntegrationType(nil), cmd.Installs...)
			c.IntegrationTypes = &installs
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

func optionAllowsAutocomplete(options []*discordgo.ApplicationCommandOption, optionName string) bool {
	for _, o := range options {
		if o != nil && o.Name == optionName && o.Autocomplete {
			return true
		}
	}
	return false
}

func handleAutocomplete(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate) {
	respondAutocompleteChoices(s, i, autocompleteChoices(ctx, db, s, i))
}

// autocompleteChoices asks the focused option's autocomplete for its choices: the subcommand's when the
// command is a group, the command's otherwise. It returns nil when nothing offers any.
func autocompleteChoices(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate) []*discordgo.ApplicationCommandOptionChoice {
	data := i.ApplicationCommandData()
	cmd, ok := registry[data.Name]
	if !ok || len(data.Options) == 0 {
		return nil
	}
	l, ok := cmd.resolve(data.Options)
	if !ok {
		return nil
	}
	options, ask := cmd.Options, cmd.AutocompleteOption
	if l.sub != nil {
		options, ask = l.sub.Options, l.sub.AutocompleteOption
	}
	focused := focusedOption(l.options)
	if ask == nil || focused == nil || !optionAllowsAutocomplete(options, focused.Name) {
		return nil
	}
	focusedValue := ""
	if sv, ok := focused.Value.(string); ok {
		focusedValue = sv
	}
	choices := ask(ctx, db, s, i, focused.Name, focusedValue)
	if len(choices) > maxAutocompleteChoices {
		choices = choices[:maxAutocompleteChoices]
	}
	return choices
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
