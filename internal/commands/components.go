package commands

import (
	"context"
	"runtime/debug"
	"strings"

	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/discord"
	"ReZeroTsu/internal/interaction"
	"ReZeroTsu/internal/logger"

	"github.com/bwmarrin/discordgo"
)

// ComponentHandler handles a button press, a picker choice or a form submission. It reaches Discord through
// ic. s is only for legacy page renderers that still need the raw session; new handlers ignore it.
type ComponentHandler func(ic *interaction.Context, s *discordgo.Session) error

// componentRegistry maps a custom-ID prefix to its handler. Custom IDs carry only small identifiers, never user
// text, and stay within Discord's 100-character limit.
var componentRegistry = make(map[string]ComponentHandler)

// AddComponent registers the handler for every custom ID that starts with prefix. When prefixes overlap, the
// longest one wins. Safe from init().
func AddComponent(prefix string, h ComponentHandler) {
	if prefix == "" || h == nil {
		panic("commands: AddComponent requires a prefix and a handler")
	}
	if _, dup := componentRegistry[prefix]; dup {
		panic("commands: component prefix registered twice: " + prefix)
	}
	componentRegistry[prefix] = h
}

func componentHandler(customID string) ComponentHandler {
	var best string
	var found ComponentHandler
	for prefix, h := range componentRegistry {
		if strings.HasPrefix(customID, prefix) && len(prefix) > len(best) {
			best, found = prefix, h
		}
	}
	return found
}

// componentCustomID returns the custom ID of a button, picker or form interaction, or "".
func componentCustomID(i *discordgo.Interaction) string {
	switch data := i.Data.(type) {
	case discordgo.MessageComponentInteractionData:
		return data.CustomID
	case discordgo.ModalSubmitInteractionData:
		return data.CustomID
	}
	return ""
}

// dispatchComponent runs the handler registered for the interaction's custom ID. A custom ID nobody owns is
// ignored. A failure is shown privately. s is only handed to handlers that still need the raw session.
func dispatchComponent(ctx context.Context, db *database.Client, port interaction.Port, s *discordgo.Session, i *discordgo.InteractionCreate) {
	h := componentHandler(componentCustomID(i.Interaction))
	if h == nil {
		return
	}
	ic := interaction.NewContext(port, i.Interaction, db, interaction.Private, interaction.Direct)
	ic.Ctx = ctx
	defer func() {
		if r := recover(); r != nil {
			l := logger.For("commands").With("custom_id", ic.CustomID, "guild_id", i.GuildID, "channel_id", i.ChannelID, "panic", r, "stack", string(debug.Stack()))
			l.Error("panic in component handler")
			refuse(ic, errMessage)
		}
	}()
	if err := h(ic, s); err != nil {
		args := append(discord.RESTAttrs(err), "custom_id", ic.CustomID, "guild_id", i.GuildID, "channel_id", i.ChannelID, "err", err)
		logger.For("commands").Error("component handler failed", args...)
		refuse(ic, errMessage)
	}
}
