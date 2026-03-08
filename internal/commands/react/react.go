package react

import (
	"context"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/database/guilds"
	"ReZeroTsu/internal/discord"
	"ReZeroTsu/internal/logger"

	"github.com/bwmarrin/discordgo"
)

// customEmojiReactionRegex matches <:name:id> or <a:name:id>; submatches: prefix, name, id.
var customEmojiReactionRegex = regexp.MustCompile(`(?i)<(a?):([a-zA-Z0-9_]+):(\d+)>`)

const (
	embedTitleReact  = "React Autorole"
	successAdd       = "Success! Reaction autorole set."
	successRemove    = "Success! Removed that emoji autorole from the message."
	successRemoveAll = "Success! Removed entire message react autorole."
	hintRemoveReact  = "Use /remove-react-autorole to remove an entry."
)

func init() {
	commands.Add(&commands.Command{
		Name:       "add-react-autorole",
		Desc:       "Adds a react autorole on a specific message, emoji and role.",
		Permission: commands.PermMod,
		Context:    commands.ContextGuildOnly,
		Module:     "reacts",
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionString, Name: "message-id", Description: "The ID of the message to set a reaction emoji and role for.", Required: true},
			{Type: discordgo.ApplicationCommandOptionString, Name: "emoji", Description: "The emoji to use as the reaction (custom or unicode).", Required: true},
			{Type: discordgo.ApplicationCommandOptionRole, Name: "role", Description: "The role to give/remove when users react with the emoji.", Required: true},
		},
		Handler: handleAddReactAutorole,
	})
	commands.Add(&commands.Command{
		Name:       "remove-react-autorole",
		Desc:       "Removes a set react autorole.",
		Permission: commands.PermMod,
		Context:    commands.ContextGuildOnly,
		Module:     "reacts",
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionString, Name: "message-id", Description: "The ID of the message to remove react autorole(s) for.", Required: true},
			{Type: discordgo.ApplicationCommandOptionString, Name: "emoji", Description: "The emoji to remove. Leave empty to remove all react autoroles for this message.", Required: false},
		},
		Handler: handleRemoveReactAutorole,
	})
	commands.Add(&commands.Command{
		Name:       "reacts-autorole",
		Desc:       "Lists all set reaction autoroles.",
		Permission: commands.PermMod,
		Context:    commands.ContextGuildOnly,
		Module:     "reacts",
		Options:    nil,
		Handler:    handleReactsAutorole,
	})
	commands.RegisterPaginationRenderer("reacts-autorole", RenderReactsAutorolePage)
}

func handleAddReactAutorole(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.GuildID == "" {
		commands.RespondEmbed(s, i, embedTitleReact, "This command can only be used in a server.")
		return
	}
	data := i.ApplicationCommandData()
	optMsgID := commands.ParseOption(data.Options, "message-id")
	optEmoji := commands.ParseOption(data.Options, "emoji")
	optRole := commands.ParseOption(data.Options, "role")
	if optMsgID == nil || optEmoji == nil || optRole == nil {
		commands.RespondEmbed(s, i, embedTitleReact, "Please provide message-id, emoji, and role.")
		return
	}
	messageID := optMsgID.StringValue()
	if _, err := strconv.ParseInt(messageID, 10, 64); err != nil || len(messageID) < 17 {
		commands.RespondEmbed(s, i, embedTitleReact, "Invalid message ID.")
		return
	}
	emojiInput := strings.TrimSpace(optEmoji.StringValue())
	stored, ok := parseEmojiToStoredValue(emojiInput)
	if !ok {
		if emojiInput == "" || strings.HasPrefix(emojiInput, "<") {
			commands.RespondEmbed(s, i, embedTitleReact, "Use the full emoji form: <:name:id> or <a:name:id> for custom, or use a unicode emoji.")
			return
		}
		stored = emojiInput
	}
	emoji := stored
	roleID := optRole.RoleValue(s, i.GuildID).ID

	m, err := db.Guilds().GetReactJoinMap(ctx, i.GuildID)
	if err != nil {
		logger.For("commands").Error("react GetReactJoinMap failed", "guild_id", i.GuildID, "err", err)
		commands.RespondEmbed(s, i, embedTitleReact, "Could not load react joins.")
		return
	}
	entry := m[messageID]
	if entry == nil {
		entry = &guilds.ReactJoin{}
	}
	if entry.RoleEmojiMap == nil {
		entry.RoleEmojiMap = []map[string][]string{}
	}
	// Add binding: roleID -> emoji (append to existing slice or add new map entry). Compare normalized (strip a:) so a:name:id and name:id are the same.
	emojiNorm := strings.TrimPrefix(strings.ToLower(emoji), "a:")
	found := false
	for _, rm := range entry.RoleEmojiMap {
		if emojis := rm[roleID]; emojis != nil {
			for _, e := range emojis {
				storedNorm := strings.TrimPrefix(strings.ToLower(e), "a:")
				if storedNorm == emojiNorm {
					found = true
					break
				}
			}
			if !found {
				rm[roleID] = append(emojis, emoji)
				found = true
			}
			break
		}
	}
	if !found {
		entry.RoleEmojiMap = append(entry.RoleEmojiMap, map[string][]string{roleID: {emoji}})
	}

	if err := db.Guilds().SaveReactJoinEntry(ctx, i.GuildID, messageID, entry); err != nil {
		logger.For("commands").Error("react SaveReactJoinEntry failed", "guild_id", i.GuildID, "err", err)
		commands.RespondEmbed(s, i, embedTitleReact, "Could not save react join.")
		return
	}
	if err := s.MessageReactionAdd(i.ChannelID, messageID, emoji); err != nil {
		args := append(discord.RESTAttrs(err), "guild_id", i.GuildID, "channel_id", i.ChannelID, "err", err)
		logger.For("commands").Error("react MessageReactionAdd failed", args...)
	}
	content := successAdd
	if _, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &content}); err != nil {
		args := append(discord.RESTAttrs(err), "guild_id", i.GuildID, "channel_id", i.ChannelID, "err", err)
		logger.For("commands").Error("react edit failed", args...)
	}
}

func handleRemoveReactAutorole(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.GuildID == "" {
		commands.RespondEmbed(s, i, embedTitleReact, "This command can only be used in a server.")
		return
	}
	data := i.ApplicationCommandData()
	optMsgID := commands.ParseOption(data.Options, "message-id")
	if optMsgID == nil {
		commands.RespondEmbed(s, i, embedTitleReact, "Please provide message-id.")
		return
	}
	messageID := optMsgID.StringValue()
	if _, err := strconv.ParseInt(messageID, 10, 64); err != nil || len(messageID) < 17 {
		commands.RespondEmbed(s, i, embedTitleReact, "Invalid message ID.")
		return
	}
	optEmoji := commands.ParseOption(data.Options, "emoji")

	if optEmoji == nil || optEmoji.StringValue() == "" {
		if err := db.Guilds().DeleteReactJoinEntry(ctx, i.GuildID, messageID); err != nil {
			logger.For("commands").Error("react DeleteReactJoinEntry failed", "guild_id", i.GuildID, "err", err)
			commands.RespondEmbed(s, i, embedTitleReact, "Could not remove react join.")
			return
		}
		content := successRemoveAll
		if _, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &content}); err != nil {
			args := append(discord.RESTAttrs(err), "guild_id", i.GuildID, "channel_id", i.ChannelID, "err", err)
			logger.For("commands").Error("InteractionResponseEdit failed", args...)
		}
		return
	}

	stored, ok := parseEmojiToStoredValue(strings.TrimSpace(optEmoji.StringValue()))
	if !ok {
		commands.RespondEmbed(s, i, embedTitleReact, "Use the full emoji form: <:name:id> or paste the emoji from the server.")
		return
	}
	emoji := stored
	m, err := db.Guilds().GetReactJoinMap(ctx, i.GuildID)
	if err != nil {
		logger.For("commands").Error("react GetReactJoinMap failed", "guild_id", i.GuildID, "err", err)
		commands.RespondEmbed(s, i, embedTitleReact, "Could not load react joins.")
		return
	}
	entry := m[messageID]
	if entry == nil || len(entry.RoleEmojiMap) == 0 {
		commands.RespondEmbed(s, i, embedTitleReact, "No such message ID is set.")
		return
	}
	emojiNorm := strings.TrimPrefix(strings.ToLower(emoji), "a:")
	removed := false
	for _, rm := range entry.RoleEmojiMap {
		for role, emojis := range rm {
			for idx, e := range emojis {
				storedNorm := strings.TrimPrefix(strings.ToLower(e), "a:")
				if storedNorm == emojiNorm {
					emojis = append(emojis[:idx], emojis[idx+1:]...)
					if len(emojis) == 0 {
						delete(rm, role)
					} else {
						rm[role] = emojis
					}
					removed = true
					break
				}
			}
			if removed {
				break
			}
		}
		if removed {
			break
		}
	}
	if !removed {
		commands.RespondEmbed(s, i, embedTitleReact, "That emoji is not set for this message.")
		return
	}
	empty := true
	for _, rm := range entry.RoleEmojiMap {
		if len(rm) > 0 {
			empty = false
			break
		}
	}
	if empty {
		if err := db.Guilds().DeleteReactJoinEntry(ctx, i.GuildID, messageID); err != nil {
			logger.For("commands").Error("react DeleteReactJoinEntry failed", "guild_id", i.GuildID, "err", err)
		}
	} else {
		if err := db.Guilds().SaveReactJoinEntry(ctx, i.GuildID, messageID, entry); err != nil {
			logger.For("commands").Error("react SaveReactJoinEntry failed", "guild_id", i.GuildID, "err", err)
			commands.RespondEmbed(s, i, embedTitleReact, "Could not save after remove.")
			return
		}
	}
	content := successRemove
	if _, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &content}); err != nil {
		args := append(discord.RESTAttrs(err), "guild_id", i.GuildID, "channel_id", i.ChannelID, "err", err)
		logger.For("commands").Error("InteractionResponseEdit failed", args...)
	}
}

func reactAutoroleFieldsForPage(m map[string]*guilds.ReactJoin, messageIDs []string, roleNames map[string]string, guildEmojiIDs map[string]struct{}, start int) []*discordgo.MessageEmbedField {
	fields := make([]*discordgo.MessageEmbedField, 0, discord.MaxEmbedFields)
	end := min(start+discord.MaxEmbedFields, len(messageIDs))
	for j := start; j < end; j++ {
		messageID := messageIDs[j]
		entry := m[messageID]
		value := buildMessageRoleEmojiValue(entry, roleNames, guildEmojiIDs)
		if value == "" {
			continue
		}
		if len(value) > discord.MaxEmbedFieldValueLength {
			value = value[:discord.MaxEmbedFieldValueLength-1] + "…"
		}
		name := "Message ID: " + messageID
		if len(name) > discord.MaxEmbedFieldNameLength {
			name = name[:discord.MaxEmbedFieldNameLength]
		}
		fields = append(fields, &discordgo.MessageEmbedField{Name: name, Value: value})
	}
	return fields
}

func handleReactsAutorole(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.GuildID == "" {
		commands.RespondEmbed(s, i, embedTitleReact, "This command can only be used in a server.")
		return
	}
	roles, err := s.GuildRoles(i.GuildID)
	roleNames := make(map[string]string)
	if err == nil {
		for _, r := range roles {
			roleNames[r.ID] = r.Name
		}
	}
	guildEmojis, _ := s.GuildEmojis(i.GuildID)
	guildEmojiIDs := make(map[string]struct{})
	for _, em := range guildEmojis {
		guildEmojiIDs[em.ID] = struct{}{}
	}
	m, err := db.Guilds().GetReactJoinMap(ctx, i.GuildID)
	if err != nil {
		logger.For("commands").Error("react GetReactJoinMap failed", "guild_id", i.GuildID, "err", err)
		commands.RespondEmbed(s, i, embedTitleReact, "Could not load react joins.")
		return
	}
	if len(m) == 0 {
		commands.RespondEmbed(s, i, embedTitleReact, "There are no set react autoroles.")
		return
	}
	messageIDs := make([]string, 0, len(m))
	for msgID, entry := range m {
		if entry != nil && len(entry.RoleEmojiMap) > 0 {
			messageIDs = append(messageIDs, msgID)
		}
	}
	if len(messageIDs) == 0 {
		commands.RespondEmbed(s, i, embedTitleReact, "There are no set react autoroles.")
		return
	}
	sort.Strings(messageIDs)
	fields := reactAutoroleFieldsForPage(m, messageIDs, roleNames, guildEmojiIDs, 0)
	description := ""
	var components []discordgo.MessageComponent
	if len(messageIDs) > discord.MaxEmbedFields {
		totalPages := (len(messageIDs) + discord.MaxEmbedFields - 1) / discord.MaxEmbedFields
		description = "Page 1 of " + strconv.Itoa(totalPages) + ". Showing 1–" + strconv.Itoa(discord.MaxEmbedFields) + " of " + strconv.Itoa(len(messageIDs)) + " messages."
		components = commands.PaginationComponents("reacts-autorole", 0, len(messageIDs), i.Member.User.ID)
	}
	if len(components) > 0 {
		commands.RespondEmbedWithFieldsAndComponents(s, i, embedTitleReact, description, fields, components, hintRemoveReact)
	} else {
		commands.RespondEmbedWithFields(s, i, embedTitleReact, description, fields, hintRemoveReact)
	}
}

// RenderReactsAutorolePage renders one page of reacts-autorole for pagination.
func RenderReactsAutorolePage(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate, page, total int, authorID string) {
	roles, err := s.GuildRoles(i.GuildID)
	roleNames := make(map[string]string)
	if err == nil {
		for _, r := range roles {
			roleNames[r.ID] = r.Name
		}
	}
	guildEmojis, _ := s.GuildEmojis(i.GuildID)
	guildEmojiIDs := make(map[string]struct{})
	for _, em := range guildEmojis {
		guildEmojiIDs[em.ID] = struct{}{}
	}
	m, err := db.Guilds().GetReactJoinMap(ctx, i.GuildID)
	if err != nil {
		logger.For("commands").Error("react GetReactJoinMap page failed", "guild_id", i.GuildID, "err", err)
		_ = commands.RespondEphemeral(s, i, "Could not load react joins.")
		return
	}
	messageIDs := make([]string, 0, len(m))
	for msgID, entry := range m {
		if entry != nil && len(entry.RoleEmojiMap) > 0 {
			messageIDs = append(messageIDs, msgID)
		}
	}
	sort.Strings(messageIDs)
	n := len(messageIDs)
	if n == 0 {
		_ = commands.RespondEphemeral(s, i, "There are no set react autoroles.")
		return
	}
	totalPages := (n + discord.MaxEmbedFields - 1) / discord.MaxEmbedFields
	if page >= totalPages {
		page = totalPages - 1
	}
	start := page * discord.MaxEmbedFields
	fields := reactAutoroleFieldsForPage(m, messageIDs, roleNames, guildEmojiIDs, start)
	endShow := start + len(fields)
	if endShow > n {
		endShow = n
	}
	description := "Page " + strconv.Itoa(page+1) + " of " + strconv.Itoa(totalPages) + ". Showing " + strconv.Itoa(start+1) + "–" + strconv.Itoa(endShow) + " of " + strconv.Itoa(n) + " messages."
	components := commands.PaginationComponents("reacts-autorole", page, n, commands.UserID(i))
	emb := commands.NewEmbedWithFields(s, embedTitleReact, description, fields, hintRemoveReact)
	err = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Embeds:     []*discordgo.MessageEmbed{emb},
			Components: components,
		},
	})
	if err != nil {
		args := append(discord.RESTAttrs(err), "guild_id", i.GuildID, "channel_id", i.ChannelID, "err", err)
		logger.For("commands").Error("react RenderReactsAutorolePage respond failed", args...)
	}
}

func buildMessageRoleEmojiValue(entry *guilds.ReactJoin, roleNames map[string]string, guildEmojiIDs map[string]struct{}) string {
	var b strings.Builder
	for _, rm := range entry.RoleEmojiMap {
		for role, emojis := range rm {
			displayRole := roleNames[role]
			if displayRole == "" {
				displayRole = "`" + role + "`"
			} else {
				displayRole = "**" + displayRole + "**"
			}
			b.WriteString("• ")
			b.WriteString(displayRole)
			b.WriteString(": ")
			for j, e := range emojis {
				if j > 0 {
					b.WriteString(" ")
				}
				b.WriteString(emojiForDisplay(e, guildEmojiIDs))
			}
			b.WriteString("\n")
		}
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// emojiForDisplay returns the string for messages/embeds. This-guild custom: <:name:id>; other guilds: name:id to avoid :name: fallback. Unicode as-is.
func emojiForDisplay(stored string, guildEmojiIDs map[string]struct{}) string {
	if idx := strings.LastIndex(stored, ":"); idx >= 0 && idx < len(stored)-1 {
		id := stored[idx+1:]
		if _, inGuild := guildEmojiIDs[id]; inGuild {
			if strings.HasPrefix(stored, "a:") {
				return "<" + stored + ">"
			}
			return "<:" + stored + ">"
		}
		return stored
	}
	return stored
}

// parseEmojiToStoredValue parses <:name:id> or <a:name:id>, returns stored value with original case, or ("", false).
func parseEmojiToStoredValue(input string) (stored string, ok bool) {
	input = strings.TrimSpace(input)
	if m := customEmojiReactionRegex.FindStringSubmatch(input); len(m) == 4 {
		nameID := m[2] + ":" + m[3]
		if strings.EqualFold(m[1], "a") {
			return "a:" + nameID, true
		}
		return nameID, true
	}
	return "", false
}
