package commands

import (
	"strings"
	"sync"

	"github.com/bwmarrin/discordgo"
)

// ChannelMention renders a channel as a clickable mention that stays correct
// after a rename and shows even when the bot cannot see the channel's name.
// An empty ID renders as "".
func ChannelMention(id string) string { return mention("<#", id) }

// RoleMention renders a role as a mention. An empty ID renders as "".
func RoleMention(id string) string { return mention("<@&", id) }

// UserMention renders a user as a mention. An empty ID renders as "".
func UserMention(id string) string { return mention("<@", id) }

func mention(prefix, id string) string {
	if id == "" {
		return ""
	}
	return prefix + id + ">"
}

var (
	commandIDsMu sync.RWMutex
	commandIDs   map[string]string // slash command name -> command ID
)

// SetCommandIDs keeps the slash command IDs returned by a bulk overwrite so
// CommandMention can render clickable mentions. It replaces any earlier set.
func SetCommandIDs(registered []*discordgo.ApplicationCommand) {
	ids := make(map[string]string, len(registered))
	for _, c := range registered {
		if c == nil || c.ID == "" || (c.Type != 0 && c.Type != discordgo.ChatApplicationCommand) {
			continue
		}
		ids[c.Name] = c.ID
	}
	commandIDsMu.Lock()
	commandIDs = ids
	commandIDsMu.Unlock()
}

// CommandMention renders a command, or a group or subcommand path such as
// ("raffle", "draw"), as a clickable mention. Without a known ID for the top
// level command it falls back to plain "/raffle draw" text. An empty path
// renders as "".
func CommandMention(path ...string) string {
	if len(path) == 0 {
		return ""
	}
	full := strings.Join(path, " ")
	commandIDsMu.RLock()
	id := commandIDs[path[0]]
	commandIDsMu.RUnlock()
	if id == "" {
		return "/" + full
	}
	return "</" + full + ":" + id + ">"
}
