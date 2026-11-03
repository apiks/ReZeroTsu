// Package interactiontest holds the recording fake of interaction.Port, for tests of handlers and dispatch.
package interactiontest

import (
	"fmt"
	"sync"

	"ReZeroTsu/internal/interaction"

	"github.com/bwmarrin/discordgo"
)

// Call is one recorded Port call.
type Call struct {
	Op         string // method name: "Reply", "Defer", "EditReply", ...
	Response   interaction.Response
	Form       interaction.Form
	Visibility interaction.Visibility // Reply, FollowUp and Defer: who sees it
	ChannelID  string
	MessageID  string
	Emoji      string
}

// Fake is a recording interaction.Port. Reads answer from the maps; any operation named in Fail returns its error.
type Fake struct {
	mu    sync.Mutex
	calls []Call

	Guilds   map[string]*discordgo.Guild
	RoleSets map[string][]*discordgo.Role
	Channels map[string]*discordgo.Channel
	Messages map[string]*discordgo.Message // keyed "<channelID>/<messageID>"
	// Fail makes the named operation return the error instead of recording success.
	Fail map[string]error
}

var _ interaction.Port = (*Fake)(nil)

// Calls returns a copy of what was recorded so far.
func (f *Fake) Calls() []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Call(nil), f.calls...)
}

// Ops returns just the operation names, in order.
func (f *Fake) Ops() []string {
	var out []string
	for _, c := range f.Calls() {
		out = append(out, c.Op)
	}
	return out
}

func (f *Fake) record(c Call) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, c)
	return f.Fail[c.Op]
}

func (f *Fake) Reply(r interaction.Response) error {
	return f.record(Call{Op: "Reply", Response: r, Visibility: r.Visibility})
}
func (f *Fake) Defer(v interaction.Visibility) error {
	return f.record(Call{Op: "Defer", Visibility: v})
}
func (f *Fake) EditReply(r interaction.Response) error {
	return f.record(Call{Op: "EditReply", Response: r})
}
func (f *Fake) FollowUp(r interaction.Response) error {
	return f.record(Call{Op: "FollowUp", Response: r, Visibility: r.Visibility})
}
func (f *Fake) DeleteReply() error { return f.record(Call{Op: "DeleteReply"}) }
func (f *Fake) OpenForm(form interaction.Form) error {
	return f.record(Call{Op: "OpenForm", Form: form})
}
func (f *Fake) UpdateMessage(r interaction.Response) error {
	return f.record(Call{Op: "UpdateMessage", Response: r})
}

func (f *Fake) PostMessage(channelID string, r interaction.Response) (*discordgo.Message, error) {
	if err := f.record(Call{Op: "PostMessage", ChannelID: channelID, Response: r}); err != nil {
		return nil, err
	}
	return &discordgo.Message{ID: "posted", ChannelID: channelID, Content: r.Content}, nil
}

func (f *Fake) EditMessage(channelID, messageID string, r interaction.Response) error {
	return f.record(Call{Op: "EditMessage", ChannelID: channelID, MessageID: messageID, Response: r})
}

func (f *Fake) ReadMessage(channelID, messageID string) (*discordgo.Message, error) {
	if err := f.record(Call{Op: "ReadMessage", ChannelID: channelID, MessageID: messageID}); err != nil {
		return nil, err
	}
	if m, ok := f.Messages[channelID+"/"+messageID]; ok {
		return m, nil
	}
	return nil, fmt.Errorf("message %s/%s not found", channelID, messageID)
}

func (f *Fake) AddReaction(channelID, messageID, emoji string) error {
	return f.record(Call{Op: "AddReaction", ChannelID: channelID, MessageID: messageID, Emoji: emoji})
}

func (f *Fake) Guild(guildID string) (*discordgo.Guild, error) {
	if err := f.record(Call{Op: "Guild"}); err != nil {
		return nil, err
	}
	if g, ok := f.Guilds[guildID]; ok {
		return g, nil
	}
	return nil, fmt.Errorf("guild %s not found", guildID)
}

func (f *Fake) Roles(guildID string) ([]*discordgo.Role, error) {
	if err := f.record(Call{Op: "Roles"}); err != nil {
		return nil, err
	}
	return f.RoleSets[guildID], nil
}

func (f *Fake) Channel(channelID string) (*discordgo.Channel, error) {
	if err := f.record(Call{Op: "Channel", ChannelID: channelID}); err != nil {
		return nil, err
	}
	if c, ok := f.Channels[channelID]; ok {
		return c, nil
	}
	return nil, fmt.Errorf("channel %s not found", channelID)
}
