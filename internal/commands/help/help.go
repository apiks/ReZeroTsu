package help

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/discord"
	"ReZeroTsu/internal/interaction"

	"github.com/bwmarrin/discordgo"
)

// Category order and display names for help. A category is a command's Module.
var (
	moduleOrder = []string{"general", "misc", "schedule", "channel", "reddit", "reacts", "raffles", "autopost", "settings"}
	moduleNames = map[string]string{
		"general":  "General",
		"misc":     "Misc",
		"schedule": "Schedule",
		"channel":  "Channel",
		"reddit":   "Reddit",
		"reacts":   "Reacts",
		"raffles":  "Raffles",
		"autopost": "Autopost",
		"settings": "Settings",
	}
)

const (
	pickerPrefix = "help:"
	overviewKey  = "overview"

	// Where the help was opened, kept in the picker's custom ID so a later choice lists the same commands.
	scopeServer = "g" // a server the bot is in: every slash command
	scopeUser   = "u" // a server the bot is not in (user install): user-installable commands only
	scopeDM     = "d" // a DM or group DM: user-installable commands only

	moderatorsOnly = "Moderators only"
	msgStalePicker = "That help page is out of date. Run /help again."
)

// listCommands is the registered commands; tests replace it.
var listCommands = commands.AllCommands

func init() {
	commands.Add(&commands.Command{
		Visibility: commands.Public,
		Response:   commands.Direct,
		Name:       "help",
		Desc:       "List the bot's commands by category.",
		Permission: commands.PermEveryone,
		Module:     "general",
		Installs:   commands.BothInstalls(),
		Run:        runHelp,
	})
	commands.AddComponent(pickerPrefix, handlePick)
}

// category is one help page: the visible commands of a module.
type category struct {
	key, name string
	cmds      []*commands.Command
	modOnly   bool // every command in it needs moderator rights
}

func scopeOf(i *discordgo.Interaction) string {
	switch {
	case i.GuildID == "":
		return scopeDM
	case !interaction.BotInGuild(i):
		return scopeUser
	default:
		return scopeServer
	}
}

// listed reports whether help shows c in the scope. Right-click commands are not typed, so help skips them.
// Owner commands run only as DM prefix commands, so no slash command is an owner command. Where the bot is not a
// server member only user-installable commands run, and a DM cannot run a server-only command.
func listed(c *commands.Command, scope string) bool {
	if c.Kind != commands.KindSlash || c.Permission == commands.PermOwner {
		return false
	}
	switch scope {
	case scopeDM:
		if c.Context == commands.ContextGuildOnly {
			return false
		}
	default:
		if c.Context == commands.ContextDMOnly {
			return false
		}
	}
	if scope != scopeServer {
		return slices.Contains(c.Installs, discordgo.ApplicationIntegrationUserInstall)
	}
	return true
}

func buildCategories(all []*commands.Command, scope string) []category {
	by := make(map[string][]*commands.Command)
	for _, c := range all {
		if !listed(c, scope) {
			continue
		}
		key := c.Module
		if key == "" {
			key = "general"
		}
		by[key] = append(by[key], c)
	}
	keys := slices.DeleteFunc(slices.Clone(moduleOrder), func(k string) bool { return len(by[k]) == 0 })
	var extra []string
	for k := range by {
		if !slices.Contains(moduleOrder, k) {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	keys = append(keys, extra...)

	out := make([]category, 0, len(keys))
	for _, k := range keys {
		cmds := by[k]
		sort.Slice(cmds, func(i, j int) bool { return cmds[i].Name < cmds[j].Name })
		name := moduleNames[k]
		if name == "" {
			name = k
		}
		modOnly := true
		for _, c := range cmds {
			modOnly = modOnly && c.Permission != commands.PermEveryone
		}
		out = append(out, category{key: k, name: name, cmds: cmds, modOnly: modOnly})
	}
	return out
}

func pickerID(authorID, scope string) string { return pickerPrefix + authorID + ":" + scope }

func parsePickerID(id string) (authorID, scope string, ok bool) {
	parts := strings.Split(strings.TrimPrefix(id, pickerPrefix), ":")
	if len(parts) != 2 || parts[0] == "" || (parts[1] != scopeServer && parts[1] != scopeUser && parts[1] != scopeDM) {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func picker(cats []category, authorID, scope, selected string) discordgo.MessageComponent {
	opts := []discordgo.SelectMenuOption{{Label: "Overview", Value: overviewKey, Default: selected == overviewKey}}
	for _, c := range cats {
		o := discordgo.SelectMenuOption{Label: c.name, Value: c.key, Default: selected == c.key}
		if c.modOnly {
			o.Description = moderatorsOnly
		}
		opts = append(opts, o)
	}
	return discordgo.ActionsRow{Components: []discordgo.MessageComponent{discordgo.SelectMenu{
		MenuType:    discordgo.StringSelectMenu,
		CustomID:    pickerID(authorID, scope),
		Placeholder: "Choose a category",
		Options:     opts,
	}}}
}

func supportFooter() string {
	return "Need help? Join our support server: " + discord.SupportServerURL
}

func overview(cats []category, authorID, scope string) interaction.Response {
	var b strings.Builder
	b.WriteString("Pick a category below to see its commands.\n\n")
	for _, c := range cats {
		fmt.Fprintf(&b, "**%s**", c.name)
		if c.modOnly {
			fmt.Fprintf(&b, " (%s)", moderatorsOnly)
		}
		fmt.Fprintf(&b, " - %s\n", countLabel(commandLines(c.cmds, false)))
	}
	if len(cats) == 0 {
		b.Reset()
		b.WriteString("No commands are available here.")
	}
	emb := commands.NewEmbed(nil, "Help", b.String(), supportFooter())
	return interaction.Response{Embeds: []*discordgo.MessageEmbed{emb}, Components: []discordgo.MessageComponent{picker(cats, authorID, scope, overviewKey)}}
}

func countLabel(lines []string) string {
	if len(lines) == 1 {
		return "1 command"
	}
	return fmt.Sprintf("%d commands", len(lines))
}

func categoryPage(cats []category, c category, authorID, scope string) interaction.Response {
	var b strings.Builder
	if c.modOnly {
		b.WriteString("**" + moderatorsOnly + "**\n\n")
	}
	for _, line := range commandLines(c.cmds, !c.modOnly) {
		if b.Len()+len(line)+1 > discord.MaxEmbedDescriptionLength {
			break
		}
		b.WriteString(line + "\n")
	}
	emb := commands.NewEmbed(nil, "Commands: "+c.name, b.String(), supportFooter())
	return interaction.Response{Embeds: []*discordgo.MessageEmbed{emb}, Components: []discordgo.MessageComponent{picker(cats, authorID, scope, c.key)}}
}

// commandLines renders one line per runnable command: a clickable mention (plain text where the command's ID is
// not known) and its description. A group command lists each subcommand. A command that needs moderator rights
// in a category that mixes both is tagged when tagMods is set.
func commandLines(cmds []*commands.Command, tagMods bool) []string {
	var lines []string
	for _, c := range cmds {
		tag := ""
		if tagMods && c.Permission != commands.PermEveryone {
			tag = " (" + moderatorsOnly + ")"
		}
		if len(c.Subcommands) == 0 && len(c.Groups) == 0 {
			lines = append(lines, commands.CommandMention(c.Name)+" - "+c.Desc+tag)
			continue
		}
		for _, s := range c.Subcommands {
			lines = append(lines, commands.CommandMention(c.Name, s.Name)+" - "+s.Desc+tag)
		}
		for _, g := range c.Groups {
			for _, s := range g.Subcommands {
				lines = append(lines, commands.CommandMention(c.Name, g.Name, s.Name)+" - "+s.Desc+tag)
			}
		}
	}
	return lines
}

func runHelp(ic *interaction.Context) error {
	scope := scopeOf(ic.Interaction)
	return ic.Reply(overview(buildCategories(listCommands(), scope), ic.Invoker.ID, scope))
}

// handlePick shows the chosen category. The author's choice replaces the message. Anyone else's choice, and any
// choice on a private copy, goes the way pagination does: help is public, so another member gets a private copy
// whose picker belongs to them, and the original message stays as its author left it. Nothing here is
// per-member, so a copy leaks nothing.
func handlePick(ic *interaction.Context, _ *discordgo.Session) error {
	authorID, scope, ok := parsePickerID(ic.CustomID)
	if !ok || len(ic.Values) != 1 {
		return ic.Refuse(msgStalePicker)
	}
	cats := buildCategories(listCommands(), scope)
	var r interaction.Response
	if ic.Values[0] == overviewKey {
		r = overview(cats, ic.Invoker.ID, scope)
	} else {
		i := slices.IndexFunc(cats, func(c category) bool { return c.key == ic.Values[0] })
		if i < 0 {
			return ic.Refuse(msgStalePicker)
		}
		r = categoryPage(cats, cats[i], ic.Invoker.ID, scope)
	}
	if ic.Invoker.ID == authorID || ic.OnPrivateMessage() {
		return ic.UpdateMessage(r)
	}
	r.Visibility = interaction.Private
	return ic.Reply(r)
}
