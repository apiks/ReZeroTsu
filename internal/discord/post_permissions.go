package discord

import "github.com/bwmarrin/discordgo"

// NamedPermission is a permission bit with the name a moderator sees.
type NamedPermission struct {
	Bit  int64
	Name string
}

// IsThreadType reports whether the channel type is a public or private thread.
func IsThreadType(t discordgo.ChannelType) bool {
	return t == discordgo.ChannelTypeGuildPublicThread || t == discordgo.ChannelTypeGuildPrivateThread
}

// SendPermission is the permission that lets the bot send in a channel: Send Messages, or Send Messages in Threads
// for a thread. When the channel type is unknown (known is false) either of the two passes, so a failed channel
// lookup never turns a working channel into a missing permission.
func SendPermission(perms int64, thread, known bool) NamedPermission {
	inThreads := NamedPermission{discordgo.PermissionSendMessagesInThreads, "Send Messages in Threads"}
	plain := NamedPermission{discordgo.PermissionSendMessages, "Send Messages"}
	if !known {
		if perms&inThreads.Bit != 0 {
			return inThreads
		}
		return plain
	}
	if thread {
		return inThreads
	}
	return plain
}

// MissingNames lists the names of the needed permissions that perms lacks. Administrator has every permission,
// Pin Messages included.
func MissingNames(perms int64, needed []NamedPermission) []string {
	if perms&discordgo.PermissionAdministrator != 0 {
		return nil
	}
	var out []string
	for _, p := range needed {
		if perms&p.Bit == 0 {
			out = append(out, p.Name)
		}
	}
	return out
}
