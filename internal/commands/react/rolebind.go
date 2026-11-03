package react

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/database/guilds"
	"ReZeroTsu/internal/discord"
	"ReZeroTsu/internal/interaction"
	"ReZeroTsu/internal/logger"

	"github.com/bwmarrin/discordgo"
)

const (
	// formPrefix starts the custom ID of the Add reaction role form: reactrole:add:<channelID>:<messageID>. It
	// carries only the target message's IDs, never text a moderator typed.
	formPrefix = "reactrole:add:"

	emojiField = "emoji"
	roleField  = "role"

	msgNoTarget   = "I couldn't tell which message you chose. Right-click the message again."
	msgFormStale  = "That form is no longer valid. Right-click the message and try again."
	msgGuildOnly  = "Reaction roles only work in a server."
	msgBadEmoji   = "That isn't an emoji I can read. Type a standard emoji, or paste a server emoji in its full form: <:name:id> or <a:name:id>."
	msgOneRole    = "Pick one role."
	msgRoleGone   = "That role no longer exists. Pick another one."
	msgNothingSet = "There are no reaction roles on that message."
	msgMaxReacts  = "That message already has 20 different reactions, Discord's limit. Remove one, or use another message."
)

// reactNeeds is what the bot needs in a message's channel to add or remove its own reaction.
const reactNeeds = discordgo.PermissionViewChannel | discordgo.PermissionAddReactions | discordgo.PermissionReadMessageHistory

// bindStore is the part of the guild repository the reaction-role commands use. Entries are keyed by message ID.
type bindStore interface {
	GetReactJoinMap(ctx context.Context, guildID string) (map[string]*guilds.ReactJoin, error)
	SaveReactJoinEntry(ctx context.Context, guildID, messageID string, entry *guilds.ReactJoin) error
	DeleteReactJoinEntry(ctx context.Context, guildID, messageID string) error
}

// bindStoreOf picks the store of a database client; tests replace it.
var bindStoreOf = func(db *database.Client) bindStore { return db.Guilds() }

func init() {
	commands.Add(&commands.Command{
		Name:       "Add reaction role",
		Kind:       commands.KindMessage,
		Visibility: commands.Private,
		Response:   commands.Direct,
		// Guard home: the bot-side moderator check, until spec 7 moves it to Discord's per-command permissions.
		Permission: commands.PermMod,
		Context:    commands.ContextGuildOnly,
		Module:     "reacts",
		Run:        runAddForm,
	})
	commands.Add(&commands.Command{
		Name:       "Remove reaction roles",
		Kind:       commands.KindMessage,
		Visibility: commands.Private,
		Response:   commands.Direct,
		Permission: commands.PermMod,
		Context:    commands.ContextGuildOnly,
		Module:     "reacts",
		Run:        runRemoveAll,
	})
	commands.AddComponent(formPrefix, handleAddForm)
}

// customEmojiReactionRegex matches <:name:id> or <a:name:id>; submatches: prefix, name, id.
var customEmojiReactionRegex = regexp.MustCompile(`(?i)<(a?):([a-zA-Z0-9_]+):(\d+)>`)

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

// emojiFromInput turns what a moderator typed into the stored emoji: "name:id" or "a:name:id" for a server emoji
// pasted in its <:name:id> or <a:name:id> form, and anything else not starting with "<" as typed. ok is false for
// an empty box and for text that starts like a server emoji but is not one.
func emojiFromInput(input string) (stored string, ok bool) {
	input = strings.TrimSpace(input)
	if s, parsed := parseEmojiToStoredValue(input); parsed {
		return s, true
	}
	if input == "" || strings.HasPrefix(input, "<") {
		return "", false
	}
	return input, true
}

// normalizeEmoji makes stored spellings comparable: the animated marker and case do not matter.
func normalizeEmoji(stored string) string {
	return strings.TrimPrefix(strings.ToLower(stored), "a:")
}

// displayEmoji is the stored emoji as it is shown in a reply.
func displayEmoji(stored string) string {
	switch {
	case strings.HasPrefix(stored, "a:") && strings.Count(stored, ":") == 2:
		return "<" + stored + ">"
	case strings.Count(stored, ":") == 1:
		return "<:" + stored + ">"
	}
	return stored
}

// roleLabel is a role mention, or the bold name of a legacy entry that stored a role name.
func roleLabel(role string) string {
	if len(role) >= 17 && strings.Trim(role, "0123456789") == "" {
		return commands.RoleMention(role)
	}
	return "**" + role + "**"
}

func reactErrorContext(channelID string) interaction.ErrorContext {
	return interaction.ErrorContext{ChannelID: channelID, Needs: reactNeeds}
}

// addErrorContext is reactErrorContext for adding a reaction: a server emoji from another server also needs Use
// External Emojis. A stored custom emoji has a colon ("name:id" or "a:name:id"); a standard emoji has none.
func addErrorContext(channelID, emoji string) interaction.ErrorContext {
	at := reactErrorContext(channelID)
	if strings.Contains(emoji, ":") {
		at.Needs |= discordgo.PermissionUseExternalEmojis
	}
	return at
}

// takeReactionsOff removes the bot's own reaction for each emoji from the message, stopping at the first failure.
// It returns the explained error text, or "" when every reaction came off. err is the failure itself, so a caller
// can tell a message or channel that is gone from a real problem.
func takeReactionsOff(ic *interaction.Context, channelID, messageID string, emojis []string) (text string, err error) {
	for _, e := range emojis {
		if err := ic.RemoveOwnReaction(channelID, messageID, e); err != nil {
			text, known := interaction.ExplainError(err, reactErrorContext(channelID))
			if !known {
				logger.For("react").Error("reaction removal failed", "guild_id", ic.GuildID, "channel_id", channelID, "message_id", messageID, "err", err)
			}
			return text, err
		}
	}
	return "", nil
}

// clipLines joins lines with newlines so the result is at most budget runes. When some lines do not fit, it ends
// with "…and N more".
func clipLines(lines []string, budget int) string {
	total := 0
	for i, l := range lines {
		total += utf8.RuneCountInString(l)
		if i > 0 {
			total++
		}
	}
	if total <= budget {
		return strings.Join(lines, "\n")
	}
	used := 0
	kept := 0
	for i, l := range lines {
		note := len("\n…and  more") + len(strconv.Itoa(len(lines)-i))
		cost := utf8.RuneCountInString(l) + 1
		if used+cost+note > budget {
			break
		}
		used += cost
		kept++
	}
	out := strings.Join(lines[:kept], "\n")
	if kept > 0 {
		out += "\n"
	}
	return out + fmt.Sprintf("…and %d more", len(lines)-kept)
}

// boundRole is the role an emoji is already bound to in an entry, or "".
func boundRole(entry *guilds.ReactJoin, emoji string) string {
	norm := normalizeEmoji(emoji)
	for _, rm := range entry.RoleEmojiMap {
		for role, emojis := range rm {
			for _, e := range emojis {
				if normalizeEmoji(e) == norm {
					return role
				}
			}
		}
	}
	return ""
}

// entryEmojis lists each distinct emoji of an entry once, in stored order.
func entryEmojis(entry *guilds.ReactJoin) (emojis []string, pairs []string) {
	seen := map[string]bool{}
	for _, rm := range entry.RoleEmojiMap {
		for role, es := range rm {
			for _, e := range es {
				pairs = append(pairs, displayEmoji(e)+" → "+roleLabel(role))
				if n := normalizeEmoji(e); !seen[n] {
					seen[n] = true
					emojis = append(emojis, e)
				}
			}
		}
	}
	return emojis, pairs
}

// runAddForm opens the Add reaction role form for the right-clicked message.
func runAddForm(ic *interaction.Context) error {
	msg := ic.TargetMessage()
	if msg == nil || msg.ID == "" {
		return ic.Refuse(msgNoTarget)
	}
	channelID := msg.ChannelID
	if channelID == "" {
		channelID = ic.ChannelID
	}
	emoji := interaction.TextBox(emojiField, "Emoji", discordgo.TextInputShort, true)
	emoji.Description = "A standard emoji, or a server emoji pasted as <:name:id>."
	one := 1
	role := interaction.Picker("Role", "The role members get while they keep this reaction.", discordgo.SelectMenu{
		MenuType: discordgo.RoleSelectMenu, CustomID: roleField, MinValues: &one, MaxValues: 1, Placeholder: "Pick a role",
	})
	return ic.OpenForm(interaction.Form{
		CustomID:   formPrefix + channelID + ":" + msg.ID,
		Title:      "Add reaction role",
		Components: []discordgo.MessageComponent{emoji, role},
	})
}

// handleAddForm binds the typed emoji to the picked role on the message the form was opened for.
// Guard homes, moved from /add-react-autorole:
//   - message-ID validation: the target comes from the right-clicked message, carried in the custom ID, so a
//     mistyped ID cannot happen; a custom ID that does not parse is refused below;
//   - emoji parsing: emojiFromInput, same rules, refused before anything is stored or reacted;
//   - duplicate binding: boundRole below, run before anything is reacted or stored. It now also refuses an emoji
//     bound to a different role, which the old code stored although only the first binding could ever fire;
//   - saving and the "Could not save" refusal: SaveReactJoinEntry below, now after the reaction, so an emoji
//     Discord rejects is never stored; a failed save takes the new reaction back down;
//   - the bot's own reaction: AddReaction below, on the message's own channel instead of the channel the command
//     ran in, and a failure is now shown instead of only logged.
func handleAddForm(ic *interaction.Context, _ *discordgo.Session) error {
	parts := strings.Split(strings.TrimPrefix(ic.CustomID, formPrefix), ":")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return ic.Refuse(msgFormStale)
	}
	channelID, messageID := parts[0], parts[1]
	if ic.GuildID == "" {
		return ic.Refuse(msgGuildOnly)
	}
	emoji, ok := emojiFromInput(ic.FormValue(emojiField))
	if !ok {
		return ic.Refuse(msgBadEmoji)
	}
	picked := ic.FormValues(roleField)
	if len(picked) != 1 {
		return ic.Refuse(msgOneRole)
	}
	roleID := picked[0]
	roles, err := ic.Roles(ic.GuildID)
	if err != nil {
		return ic.Fail(err, interaction.ErrorContext{})
	}
	role := findRole(roles, roleID)
	if role == nil {
		return ic.Refuse(msgRoleGone)
	}

	store := bindStoreOf(ic.DB)
	m, err := store.GetReactJoinMap(ic.Context(), ic.GuildID)
	if err != nil {
		return ic.Fail(err, interaction.ErrorContext{})
	}
	entry := m[messageID]
	if entry == nil {
		entry = &guilds.ReactJoin{}
	}
	if existing := boundRole(entry, emoji); existing != "" {
		return ic.Refuse(displayEmoji(emoji) + " is already set on that message for " + roleLabel(existing) + ".")
	}

	if err := ic.AddReaction(channelID, messageID, emoji); err != nil {
		switch {
		case discord.IsUnknownEmoji(err):
			return ic.Refuse(msgBadEmoji)
		case discord.IsMaxReactions(err):
			return ic.Refuse(msgMaxReacts)
		}
		return ic.Fail(err, addErrorContext(channelID, emoji))
	}

	entry.ChannelID = channelID
	bound := false
	for _, rm := range entry.RoleEmojiMap {
		if _, has := rm[roleID]; has {
			rm[roleID] = append(rm[roleID], emoji)
			bound = true
			break
		}
	}
	if !bound {
		entry.RoleEmojiMap = append(entry.RoleEmojiMap, map[string][]string{roleID: {emoji}})
	}
	if err := store.SaveReactJoinEntry(ic.Context(), ic.GuildID, messageID, entry); err != nil {
		// Without the binding the reaction would offer a role nothing hands out, so take it back down.
		if rmErr := ic.RemoveOwnReaction(channelID, messageID, emoji); rmErr != nil {
			logger.For("react").Error("reaction cleanup failed", "guild_id", ic.GuildID, "channel_id", channelID, "message_id", messageID, "err", rmErr)
		}
		return ic.Fail(err, interaction.ErrorContext{})
	}

	reply := "Reaction role set: " + displayEmoji(emoji) + " gives " + commands.RoleMention(roleID) + " on " + commands.MessageLink(ic.GuildID, channelID, messageID) + "."
	if top, known := botHighestPosition(ic, roles); known && role.Position >= top {
		reply += "\nHeads up: " + commands.RoleMention(roleID) + " is at or above my highest role, so I can't hand it out. Move my role above it in Server Settings, Roles."
	}
	return ic.Reply(interaction.Response{Content: reply})
}

func findRole(roles []*discordgo.Role, id string) *discordgo.Role {
	for _, r := range roles {
		if r != nil && r.ID == id {
			return r
		}
	}
	return nil
}

// botHighestPosition is the position of the bot's highest role, read from the bot's cached member. known is false
// when the member is not cached, so no hierarchy claim is made.
func botHighestPosition(ic *interaction.Context, roles []*discordgo.Role) (position int, known bool) {
	bot := ic.BotUser()
	if bot == nil {
		return 0, false
	}
	guild, err := ic.Guild(ic.Context(), ic.GuildID)
	if err != nil || guild == nil {
		return 0, false
	}
	for _, mem := range guild.Members {
		if mem == nil || mem.User == nil || mem.User.ID != bot.ID {
			continue
		}
		for _, id := range mem.Roles {
			if r := findRole(roles, id); r != nil && (!known || r.Position > position) {
				position, known = r.Position, true
			}
		}
		return position, true
	}
	return 0, false
}

// runRemoveAll deletes every binding of the right-clicked message and takes the bot's reactions for them off it.
// Guard homes, moved from /remove-react-autorole with no emoji:
//   - message-ID validation: the target is the right-clicked message;
//   - the delete and its "Could not remove" refusal: DeleteReactJoinEntry below, run before any reaction is touched;
//   - the "No such message ID is set" refusal: msgNothingSet below, which now also covers an entry with no
//     bindings left.
//
// Retired by spec #36, on purpose: the per-emoji form of /remove-react-autorole. Reaction roles have no slash
// remove, and removal is now per message only, so its three refusals are gone with it: the full-emoji-form
// parse error, "That emoji is not set for this message", and "Could not save after remove".
//
// The old command removed no reactions; they are taken off after the delete, so a failure there cannot leave
// bindings behind.
func runRemoveAll(ic *interaction.Context) error {
	msg := ic.TargetMessage()
	if msg == nil || msg.ID == "" {
		return ic.Refuse(msgNoTarget)
	}
	if ic.GuildID == "" {
		return ic.Refuse(msgGuildOnly)
	}
	store := bindStoreOf(ic.DB)
	m, err := store.GetReactJoinMap(ic.Context(), ic.GuildID)
	if err != nil {
		return ic.Fail(err, interaction.ErrorContext{})
	}
	entry := m[msg.ID]
	if entry == nil {
		return ic.Refuse(msgNothingSet)
	}
	emojis, pairs := entryEmojis(entry)
	if len(emojis) == 0 {
		return ic.Refuse(msgNothingSet)
	}
	if err := store.DeleteReactJoinEntry(ic.Context(), ic.GuildID, msg.ID); err != nil {
		return ic.Fail(err, interaction.ErrorContext{})
	}

	channelID := msg.ChannelID
	if channelID == "" {
		channelID = entry.ChannelID
	}
	failure := ""
	if text, _ := takeReactionsOff(ic, channelID, msg.ID, emojis); text != "" {
		failure = "\nI couldn't take my reactions off the message: " + text
	}
	// A legacy entry can hold many pairs, so the list is clipped to leave room for the rest of the reply.
	head := "Removed the reaction roles from " + commands.MessageLink(ic.GuildID, channelID, msg.ID) + ":\n"
	budget := discord.MaxContentLength - utf8.RuneCountInString(head) - utf8.RuneCountInString(failure)
	return ic.Reply(interaction.Response{Content: head + clipLines(pairs, budget) + failure})
}
