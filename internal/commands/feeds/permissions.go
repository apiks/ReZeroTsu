package feeds

import (
	"strings"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/discord"
	"ReZeroTsu/internal/interaction"

	"github.com/bwmarrin/discordgo"
)

type namedPermission struct {
	bit  int64
	name string
}

// feedPostPermissions are what a feed needs to post. Feeds post through the bot account only, so Manage
// Webhooks is not among them. Read Message History is there because an unknown send is verified by reading recent
// messages. The thread variant of Send Messages is swapped in by missingPostPermissions.
var feedPostPermissions = []namedPermission{
	{discordgo.PermissionViewChannel, "View Channel"},
	{discordgo.PermissionSendMessages, "Send Messages"},
	{discordgo.PermissionEmbedLinks, "Embed Links"},
	{discordgo.PermissionReadMessageHistory, "Read Message History"},
}

// feedPinPermissions are what auto-pin needs on top of posting.
var feedPinPermissions = []namedPermission{
	{discordgo.PermissionManageMessages, "Manage Messages"},
	{discord.PermissionPinMessages, "Pin Messages"},
}

// missingNames lists the names of the needed permissions that perms lacks. Administrator has every permission,
// Pin Messages included.
func missingNames(perms int64, needed []namedPermission) []string {
	if perms&discordgo.PermissionAdministrator != 0 {
		return nil
	}
	var out []string
	for _, p := range needed {
		if perms&p.bit == 0 {
			out = append(out, p.name)
		}
	}
	return out
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

// checkFeedPermissions reads the bot's permissions in the channel. refusal is non-empty when the bot cannot post
// there; pinWarning is non-empty, with pin on, when it cannot pin. When the permissions or the channel cannot be
// read neither is set, so the check never blocks on its own failure.
func checkFeedPermissions(ic *interaction.Context, channelID string, pin bool) (refusal, pinWarning string) {
	perms, err := ic.BotChannelPermissions(channelID)
	if err != nil {
		return "", ""
	}
	needed := feedPostPermissions
	if ch, err := ic.Channel(channelID); err == nil && ch != nil &&
		(ch.Type == discordgo.ChannelTypeGuildPublicThread || ch.Type == discordgo.ChannelTypeGuildPrivateThread) {
		needed = append([]namedPermission(nil), feedPostPermissions...)
		needed[1] = namedPermission{discordgo.PermissionSendMessagesInThreads, "Send Messages in Threads"}
	}
	mention := commands.ChannelMention(channelID)
	if missing := missingNames(perms, needed); len(missing) > 0 {
		return "I can't post feeds in " + mention + ". I'm missing " + joinList(missing) + " there.", ""
	}
	if pin {
		if missing := missingNames(perms, feedPinPermissions); len(missing) > 0 {
			return "", "Warning: I can't pin posts in " + mention + ". I'm missing " + joinList(missing) + ". The feed is saved, but nothing will be pinned."
		}
	}
	return "", ""
}
