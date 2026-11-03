package interaction

import (
	"fmt"
	"strings"

	"ReZeroTsu/internal/discord"
	"ReZeroTsu/internal/logger"

	"github.com/bwmarrin/discordgo"
)

// SomethingWentWrong is what an unexpected error shows. The details go to the log, never to the user.
const SomethingWentWrong = "Something went wrong."

// ErrorContext says what the failed call was doing, so a sentence can name the problem and the fix.
type ErrorContext struct {
	// ChannelID is the channel the call acted on; zero value when it was not channel-specific.
	ChannelID string
	// Needs is the set of discordgo permission bits the call needs in ChannelID. Used only to word a
	// missing-permissions sentence.
	Needs int64
}

// permissionNames lists the permissions a sentence can name, in the order they are listed.
var permissionNames = []struct {
	bit  int64
	name string
}{
	{discordgo.PermissionViewChannel, "View Channel"},
	{discordgo.PermissionSendMessages, "Send Messages"},
	{discordgo.PermissionSendMessagesInThreads, "Send Messages in Threads"},
	{discordgo.PermissionEmbedLinks, "Embed Links"},
	{discordgo.PermissionAttachFiles, "Attach Files"},
	{discordgo.PermissionAddReactions, "Add Reactions"},
	{discordgo.PermissionUseExternalEmojis, "Use External Emojis"},
	{discordgo.PermissionReadMessageHistory, "Read Message History"},
	{discordgo.PermissionManageMessages, "Manage Messages"},
	{discordgo.PermissionManageWebhooks, "Manage Webhooks"},
	{discordgo.PermissionManageRoles, "Manage Roles"},
}

// joinNames renders "A", "A and B" or "A, B and C".
func joinNames(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

func needNames(needs int64) []string {
	var out []string
	for _, p := range permissionNames {
		if needs&p.bit != 0 {
			out = append(out, p.name)
		}
	}
	return out
}

// ExplainError turns a known Discord error into a sentence that names the problem and the fix. known is false
// for anything else, and the text is then SomethingWentWrong.
func ExplainError(err error, at ErrorContext) (text string, known bool) {
	channel := ""
	if at.ChannelID != "" {
		channel = "<#" + at.ChannelID + ">"
	}
	switch {
	case err == nil:
		return SomethingWentWrong, false
	case discord.IsMissingAccess(err):
		if channel == "" {
			return "I can't see that. Give me access to it and try again.", true
		}
		return "I can't see " + channel + ". Give me View Channel there and try again.", true
	case discord.IsMissingPermissions(err):
		names := needNames(at.Needs)
		switch {
		case channel == "":
			return "I can't do that here. I am missing a permission.", true
		case len(names) == 0:
			return "I can't do that in " + channel + ". I am missing a permission there.", true
		}
		return fmt.Sprintf("I can't do that in %s. I need %s there.", channel, joinNames(names)), true
	case discord.IsUnknownChannel(err):
		return "That channel no longer exists. Pick another one.", true
	case discord.IsUnknownMessage(err):
		return "That message no longer exists.", true
	case discord.IsUnknownRole(err):
		return "That role no longer exists. Pick another one.", true
	case discord.IsCannotDMUser(err):
		return "I can't send that person a direct message. They may have direct messages from server members turned off.", true
	}
	return SomethingWentWrong, false
}

// Fail shows err to the invoker only, in plain language (see Refuse for how, per interaction state). An
// unexpected error is logged with its REST attributes and shown as SomethingWentWrong.
func (c *Context) Fail(err error, at ErrorContext) error {
	text, known := ExplainError(err, at)
	if !known {
		args := append(discord.RESTAttrs(err), "guild_id", c.GuildID, "channel_id", c.ChannelID, "err", err)
		logger.For("commands").Error("handler failed", args...)
	}
	return c.Refuse(text)
}
