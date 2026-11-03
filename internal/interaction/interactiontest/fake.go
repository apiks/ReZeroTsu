// Package interactiontest holds the recording fake of interaction.Port, for tests of handlers and dispatch.
package interactiontest

import (
	"context"
	"fmt"
	"sync"
	"time"

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
	MessageIDs []string // DeleteMessages
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
	// History is each channel's messages, newest first. ListMessages and the delete operations read and change it.
	History map[string][]*discordgo.Message
	// Bot is the bot's own account and HeartbeatLatency the gateway latency, answered by BotUser and Latency.
	Bot              *discordgo.User
	HeartbeatLatency time.Duration
	// BotPerms is the bot's permission bits per channel, answered by BotChannelPermissions; a channel with no
	// entry is an error.
	BotPerms map[string]int64
	// Fail makes the named operation return the error instead of recording success.
	Fail map[string]error
	// StallGuild makes Guild block until its context ends, as a REST fetch that never answers would.
	StallGuild bool
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

// ListMessages answers from History[channelID], which is ordered newest first.
func (f *Fake) ListMessages(channelID string, limit int, beforeID string) ([]*discordgo.Message, error) {
	if err := f.record(Call{Op: "ListMessages", ChannelID: channelID, MessageID: beforeID}); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	all := f.History[channelID]
	start := 0
	if beforeID != "" {
		start = len(all)
		for i, m := range all {
			if m.ID == beforeID {
				start = i + 1
				break
			}
		}
	}
	end := min(start+limit, len(all))
	return append([]*discordgo.Message(nil), all[start:end]...), nil
}

// DeleteMessage removes the message from History, so a test can assert what is left.
func (f *Fake) DeleteMessage(channelID, messageID string) error {
	if err := f.record(Call{Op: "DeleteMessage", ChannelID: channelID, MessageID: messageID}); err != nil {
		return err
	}
	f.remove(channelID, messageID)
	return nil
}

// DeleteMessages removes the messages from History. Like Discord, it rejects fewer than 2 or more than 100.
func (f *Fake) DeleteMessages(channelID string, messageIDs []string) error {
	if err := f.record(Call{Op: "DeleteMessages", ChannelID: channelID, MessageIDs: messageIDs}); err != nil {
		return err
	}
	if len(messageIDs) < 2 || len(messageIDs) > 100 {
		return fmt.Errorf("bulk delete takes 2 to 100 messages, got %d", len(messageIDs))
	}
	for _, id := range messageIDs {
		f.remove(channelID, id)
	}
	return nil
}

func (f *Fake) remove(channelID, messageID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.History[channelID] == nil {
		return
	}
	kept := f.History[channelID][:0:0]
	for _, m := range f.History[channelID] {
		if m.ID != messageID {
			kept = append(kept, m)
		}
	}
	f.History[channelID] = kept
}

func (f *Fake) Guild(ctx context.Context, guildID string) (*discordgo.Guild, error) {
	if f.StallGuild {
		<-ctx.Done()
		return nil, ctx.Err()
	}
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

func (f *Fake) BotChannelPermissions(channelID string) (int64, error) {
	if err := f.record(Call{Op: "BotChannelPermissions", ChannelID: channelID}); err != nil {
		return 0, err
	}
	if p, ok := f.BotPerms[channelID]; ok {
		return p, nil
	}
	return 0, fmt.Errorf("no permissions for channel %s", channelID)
}

func (f *Fake) BotUser() *discordgo.User { return f.Bot }

func (f *Fake) Latency() time.Duration { return f.HeartbeatLatency }

func (f *Fake) Channel(channelID string) (*discordgo.Channel, error) {
	if err := f.record(Call{Op: "Channel", ChannelID: channelID}); err != nil {
		return nil, err
	}
	if c, ok := f.Channels[channelID]; ok {
		return c, nil
	}
	return nil, fmt.Errorf("channel %s not found", channelID)
}

// NewCommandContext builds the context a slash command receives in a guild: invoker "user", guild "g",
// channel "c", the given command data and no stores.
func NewCommandContext(p interaction.Port, v interaction.Visibility, mode interaction.ResponseMode, data discordgo.ApplicationCommandInteractionData) *interaction.Context {
	i := &discordgo.Interaction{Type: discordgo.InteractionApplicationCommand, GuildID: "g", ChannelID: "c", Data: data,
		Member: &discordgo.Member{User: &discordgo.User{ID: "user", Username: "invoker"}}}
	return interaction.NewContext(p, i, nil, v, mode)
}
