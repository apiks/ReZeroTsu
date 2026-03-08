package help

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/discord"
	"ReZeroTsu/internal/logger"

	"github.com/bwmarrin/discordgo"
)

// Module order and display names for help.
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

func init() {
	commands.Add(&commands.Command{
		Name:       "help",
		Desc:       "List commands by module or show one category.",
		Permission: commands.PermEveryone,
		Module:     "general",
		Options:    helpOptions(),
		Handler:    handleHelp,
	})
	commands.AddMessageCommand(&commands.MessageCommand{
		Name:       "help",
		Aliases:    []string{"h"},
		Permission: commands.PermEveryone,
		Context:    commands.ContextDMOnly,
		MessageHandler: func(ctx context.Context, db *database.Client, s *discordgo.Session, m *discordgo.MessageCreate, args string, runtime commands.BotRuntime) {
			emb := buildHelpEmbedForDM(s, m.Author.ID)
			_, _ = s.ChannelMessageSendEmbed(m.ChannelID, emb)
		},
	})
}

func buildHelpEmbed(s *discordgo.Session, inDM bool, filterModule string, isOwner, isAdmin, isMod bool) *discordgo.MessageEmbed {
	all := commands.AllCommands()
	var visible []*commands.Command
	for _, c := range all {
		if !canSeeCommand(c, inDM, isOwner, isAdmin, isMod) {
			continue
		}
		if filterModule != "" && c.Module != filterModule {
			continue
		}
		visible = append(visible, c)
	}
	byModule := groupByModule(visible)
	title := "Commands"
	if filterModule != "" {
		displayName := moduleNames[filterModule]
		if displayName == "" {
			displayName = filterModule
		}
		title = "Commands: " + displayName
	}
	if len(byModule) == 0 {
		return commands.NewEmbed(s, title, "No commands available for you in this context.", supportFooter())
	}
	return commands.NewEmbedWithFields(s, title, "", buildFields(byModule), supportFooter())
}

func buildHelpEmbedForDM(s *discordgo.Session, userID string) *discordgo.MessageEmbed {
	return buildHelpEmbed(s, true, "", userID == commands.OwnerID, false, false)
}

func helpOptions() []*discordgo.ApplicationCommandOption {
	categoryChoices := make([]*discordgo.ApplicationCommandOptionChoice, 0, len(moduleOrder))
	for _, key := range moduleOrder {
		categoryChoices = append(categoryChoices, &discordgo.ApplicationCommandOptionChoice{
			Name:  moduleNames[key],
			Value: key,
		})
	}
	return []*discordgo.ApplicationCommandOption{
		{
			Type:        discordgo.ApplicationCommandOptionSubCommand,
			Name:        "all",
			Description: "List all commands grouped by module",
		},
		{
			Type:        discordgo.ApplicationCommandOptionSubCommand,
			Name:        "category",
			Description: "Show commands for one category",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "name",
					Description: "Category to show",
					Required:    true,
					Choices:     categoryChoices,
				},
			},
		},
	}
}

func handleHelp(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ApplicationCommandData()
	if len(data.Options) == 0 {
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: ptr("Please use `/help all` or `/help category`."),
		})
		return
	}
	sub := data.Options[0]
	subName := sub.Name

	var filterModule string
	if subName == "category" {
		nameOpt := commands.ParseOption(sub.Options, "name")
		if nameOpt != nil {
			filterModule = nameOpt.StringValue()
		}
	}

	user := commands.InteractionUser(i)
	inDM := i.GuildID == ""
	if inDM && subName == "category" {
		commands.RespondEmbed(s, i, "Commands", "Category browsing is only available in servers. Use **/help all** to see commands you can use in DMs.", supportFooter())
		return
	}
	isOwner := user.ID == commands.OwnerID
	isAdmin := !inDM && commands.IsGuildAdmin(s, i)
	isMod := !inDM && commands.IsGuildAdminOrMod(ctx, db, s, i)

	emb := buildHelpEmbed(s, inDM, filterModule, isOwner, isAdmin, isMod)
	embeds := []*discordgo.MessageEmbed{emb}
	if _, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Embeds: &embeds}); err != nil {
		args := append(discord.RESTAttrs(err), "guild_id", i.GuildID, "channel_id", i.ChannelID, "err", err)
		logger.For("commands").Error("help edit failed", args...)
	}
}

func canSeeCommand(c *commands.Command, inDM, isOwner, isAdmin, isMod bool) bool {
	if inDM && c.Context == commands.ContextGuildOnly {
		return false
	}
	if !inDM && c.Context == commands.ContextDMOnly {
		return false
	}
	switch c.Permission {
	case commands.PermEveryone:
		return true
	case commands.PermMod:
		return isMod || isAdmin || isOwner
	case commands.PermAdmin:
		return isAdmin || isOwner
	case commands.PermOwner:
		return isOwner
	default:
		return false
	}
}

func groupByModule(cmds []*commands.Command) map[string][]*commands.Command {
	out := make(map[string][]*commands.Command)
	for _, c := range cmds {
		mod := c.Module
		if mod == "" {
			mod = "general"
		}
		out[mod] = append(out[mod], c)
	}
	for mod := range out {
		sort.Slice(out[mod], func(i, j int) bool { return out[mod][i].Name < out[mod][j].Name })
	}
	return out
}

func buildFields(byModule map[string][]*commands.Command) []*discordgo.MessageEmbedField {
	var fields []*discordgo.MessageEmbedField
	for _, key := range moduleOrder {
		cmds, ok := byModule[key]
		if !ok || len(cmds) == 0 {
			continue
		}
		displayName := moduleNames[key]
		if displayName == "" {
			displayName = key
		}
		value := buildModuleValue(cmds)
		// Split if over 1024 chars per field
		for len(value) > discord.MaxEmbedFieldValueLength {
			split := discord.MaxEmbedFieldValueLength
			for split > 0 && split < len(value) && value[split-1] != '\n' {
				split--
			}
			if split == 0 {
				split = discord.MaxEmbedFieldValueLength
			}
			chunk := value[:split]
			value = value[split:]
			fields = append(fields, &discordgo.MessageEmbedField{Name: displayName, Value: chunk, Inline: true})
			displayName = displayName + " (cont.)"
		}
		if value != "" {
			fields = append(fields, &discordgo.MessageEmbedField{Name: displayName, Value: value, Inline: true})
		}
	}
	return fields
}

func buildModuleValue(cmds []*commands.Command) string {
	var b strings.Builder
	for _, c := range cmds {
		fmt.Fprintf(&b, "`/%s` - %s\n", c.Name, c.Desc)
	}
	return b.String()
}

func supportFooter() string {
	return "Need help? Join our support server: " + discord.SupportServerURL
}

func ptr(s string) *string { return &s }
