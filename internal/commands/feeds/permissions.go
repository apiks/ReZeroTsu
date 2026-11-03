package feeds

import (
	"strings"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/discord"
	"ReZeroTsu/internal/interaction"

	"github.com/bwmarrin/discordgo"
)

// feedPostPermissions are what a feed needs to post. Feeds post through the bot account only, so Manage
// Webhooks is not among them. Read Message History is there because an unknown send is verified by reading recent
// messages. The send entry (index 1) is replaced by discord.SendPermission, which picks the thread variant.
var feedPostPermissions = []discord.NamedPermission{
	{Bit: discordgo.PermissionViewChannel, Name: "View Channel"},
	{Bit: discordgo.PermissionSendMessages, Name: "Send Messages"},
	{Bit: discordgo.PermissionEmbedLinks, Name: "Embed Links"},
	{Bit: discordgo.PermissionReadMessageHistory, Name: "Read Message History"},
}

// feedPinPermissions are what auto-pin needs on top of posting.
var feedPinPermissions = []discord.NamedPermission{
	{Bit: discordgo.PermissionManageMessages, Name: "Manage Messages"},
	{Bit: discord.PermissionPinMessages, Name: "Pin Messages"},
}

// pinWarningLine is the warning as an extra reply line, or "" when there is none.
func pinWarningLine(w string) string {
	if w == "" {
		return ""
	}
	return "\n" + w
}

func joinList(names []string) string {
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// channelIsThread reports whether the channel is a thread, and whether that is known. The type the interaction
// itself carries (the add option's resolved channel) wins over a lookup, so a failed lookup cannot change the answer.
func channelIsThread(ic *interaction.Context, channelID string) (thread, known bool) {
	if r := ic.Command.Resolved; r != nil {
		if ch := r.Channels[channelID]; ch != nil {
			return discord.IsThreadType(ch.Type), true
		}
	}
	if ch, err := ic.Channel(channelID); err == nil && ch != nil {
		return discord.IsThreadType(ch.Type), true
	}
	return false, false
}

// checkFeedPermissions reads the bot's permissions in the channel. refusal is non-empty when the bot cannot post
// there; pinWarning is non-empty, with pin on, when it cannot pin. When the permissions cannot be read neither is
// set, so the check never blocks on its own failure.
func checkFeedPermissions(ic *interaction.Context, channelID string, pin bool) (refusal, pinWarning string) {
	perms, err := ic.BotChannelPermissions(channelID)
	if err != nil {
		return "", ""
	}
	thread, known := channelIsThread(ic, channelID)
	needed := append([]discord.NamedPermission(nil), feedPostPermissions...)
	needed[1] = discord.SendPermission(perms, thread, known)
	mention := commands.ChannelMention(channelID)
	if missing := discord.MissingNames(perms, needed); len(missing) > 0 {
		return "I can't post feeds in " + mention + ". I'm missing " + joinList(missing) + " there.", ""
	}
	if pin {
		if missing := discord.MissingNames(perms, feedPinPermissions); len(missing) > 0 {
			return "", "Warning: I can't pin posts in " + mention + ". I'm missing " + joinList(missing) + ". The feed is saved, but nothing will be pinned."
		}
	}
	return "", ""
}
