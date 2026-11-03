// Package interaction is the one seam between command handlers and Discord while an interaction is being
// handled: the Port, its discordgo adapter, and the Context handed to handlers. Handlers in the new form reach
// Discord only through it, so tests drive them with the recording fake in interactiontest.
package interaction

import (
	"github.com/bwmarrin/discordgo"
)

// Visibility says who sees a reply. Discord fixes it when the interaction is first acknowledged.
type Visibility int

const (
	// Public replies are shown to the whole channel.
	Public Visibility = iota + 1
	// Private replies are shown only to the invoker.
	Private
)

// ResponseMode says how a command acknowledges: Direct commands answer at once with their first reply,
// Deferred commands show a placeholder first and fill it in later.
type ResponseMode int

const (
	Direct ResponseMode = iota + 1
	Deferred
)

// Response is the content of one reply, follow-up, edit or posted message.
type Response struct {
	Content    string
	Embeds     []*discordgo.MessageEmbed
	Components []discordgo.MessageComponent
	// Visibility overrides the command's declared visibility for this reply. Zero means "use the declared one".
	Visibility Visibility
}

// Form is a modal to open. Its fields are Label-wrapped components.
type Form struct {
	CustomID   string
	Title      string
	Components []discordgo.MessageComponent
}

// Port is every Discord call made while handling an interaction. Keep it narrow: add an operation only when a
// handler needs it.
type Port interface {
	// Reply sends the first response with the given visibility.
	Reply(r Response) error
	// Defer acknowledges with a placeholder of the given visibility.
	Defer(v Visibility) error
	// EditReply replaces the reply or the deferred placeholder.
	EditReply(r Response) error
	// FollowUp sends an additional message after the first response.
	FollowUp(r Response) error
	// DeleteReply removes the reply or the deferred placeholder.
	DeleteReply() error
	// OpenForm answers with a modal.
	OpenForm(f Form) error
	// UpdateMessage answers a component press by replacing the message the component belongs to.
	UpdateMessage(r Response) error

	// PostMessage posts a message to a channel.
	PostMessage(channelID string, r Response) (*discordgo.Message, error)
	// EditMessage edits a message the bot posted.
	EditMessage(channelID, messageID string, r Response) error
	// ReadMessage fetches one message.
	ReadMessage(channelID, messageID string) (*discordgo.Message, error)
	// AddReaction adds the bot's reaction to a message.
	AddReaction(channelID, messageID, emoji string) error

	// ListMessages lists up to limit messages of a channel, newest first, those before beforeID when it is set.
	ListMessages(channelID string, limit int, beforeID string) ([]*discordgo.Message, error)
	// DeleteMessage deletes one message.
	DeleteMessage(channelID, messageID string) error
	// DeleteMessages bulk-deletes between 2 and 100 messages that are less than 14 days old.
	DeleteMessages(channelID string, messageIDs []string) error

	// Guild reads a guild's state.
	Guild(guildID string) (*discordgo.Guild, error)
	// Roles reads a guild's roles.
	Roles(guildID string) ([]*discordgo.Role, error)
	// Channel reads a channel's state.
	Channel(channelID string) (*discordgo.Channel, error)
}
