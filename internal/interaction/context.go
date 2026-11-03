package interaction

import (
	"context"

	"ReZeroTsu/internal/database"

	"github.com/bwmarrin/discordgo"
)

// Context is what a handler in the new form receives: who invoked it, where, the command data, the stores and
// the Port. It embeds the Port, so a handler calls ic.Reply, ic.PostMessage and so on, and the five calls that
// depend on the command's declared visibility are overridden to fill it in and to remember how the interaction
// was acknowledged.
type Context struct {
	Port

	// Interaction is the raw interaction, for IDs and resolved data.
	Interaction *discordgo.Interaction
	// Invoker is the user who ran the command.
	Invoker *discordgo.User
	// Member is the invoker's guild membership; nil in DMs.
	Member    *discordgo.Member
	GuildID   string
	ChannelID string
	// Command is the slash command data; zero for component and form interactions.
	Command discordgo.ApplicationCommandInteractionData
	// DB holds the stores.
	DB *database.Client

	// Ctx is the bot's context for database work; dispatch sets it. Use Context() to read it.
	Ctx context.Context

	// CustomID is the custom ID of the pressed button or picker, or of the submitted form; empty for commands.
	CustomID string
	// Values holds the chosen options of a picker press.
	Values []string
	// Submitted holds a form submission; nil unless a form was submitted.
	Submitted *discordgo.ModalSubmitInteractionData

	// Visibility is the command's declared visibility.
	Visibility Visibility
	Mode       ResponseMode

	acked       bool
	ackedAs     Visibility
	placeholder bool // the acknowledgement was a deferred placeholder
}

// NewContext builds the context for one interaction.
func NewContext(p Port, i *discordgo.Interaction, db *database.Client, v Visibility, mode ResponseMode) *Context {
	c := &Context{Port: p, Interaction: i, DB: db, Visibility: v, Mode: mode, GuildID: i.GuildID, ChannelID: i.ChannelID, Member: i.Member}
	switch {
	case i.Member != nil && i.Member.User != nil:
		c.Invoker = i.Member.User
	default:
		c.Invoker = i.User
	}
	switch data := i.Data.(type) {
	case discordgo.ApplicationCommandInteractionData:
		c.Command = data
	case discordgo.MessageComponentInteractionData:
		c.CustomID, c.Values = data.CustomID, data.Values
	case discordgo.ModalSubmitInteractionData:
		c.CustomID, c.Submitted = data.CustomID, &data
	}
	return c
}

// Context returns the bot's context for database work, or a background one when dispatch did not set it.
func (c *Context) Context() context.Context {
	if c.Ctx == nil {
		return context.Background()
	}
	return c.Ctx
}

// OnPrivateMessage reports whether the pressed component sits on a private message, one only the presser sees.
func (c *Context) OnPrivateMessage() bool {
	return c.Interaction.Message != nil && c.Interaction.Message.Flags&discordgo.MessageFlagsEphemeral != 0
}

func (c *Context) resolve(v Visibility) Visibility {
	if v == 0 {
		return c.Visibility
	}
	return v
}

// Acknowledged reports whether the first response has been sent.
func (c *Context) Acknowledged() bool { return c.acked }

// Reply sends the first response; with a zero Visibility it uses the command's declared one.
func (c *Context) Reply(r Response) error {
	r.Visibility = c.resolve(r.Visibility)
	err := c.Port.Reply(r)
	if err == nil {
		c.acked, c.ackedAs, c.placeholder = true, r.Visibility, false
	}
	return err
}

// Defer acknowledges with a placeholder; a zero visibility uses the command's declared one.
func (c *Context) Defer(v Visibility) error {
	v = c.resolve(v)
	err := c.Port.Defer(v)
	if err == nil {
		c.acked, c.ackedAs, c.placeholder = true, v, true
	}
	return err
}

// EditReply replaces the reply or placeholder. Its visibility is whatever the acknowledgement fixed.
func (c *Context) EditReply(r Response) error { return c.Port.EditReply(r) }

// FollowUp sends an additional message; with a zero Visibility it uses the command's declared one.
func (c *Context) FollowUp(r Response) error {
	r.Visibility = c.resolve(r.Visibility)
	return c.Port.FollowUp(r)
}

// UpdateMessage answers a component press by replacing its message, which counts as the first response.
func (c *Context) UpdateMessage(r Response) error {
	err := c.Port.UpdateMessage(r)
	if err == nil {
		c.acked, c.placeholder = true, false
	}
	return err
}

// OpenForm answers with a modal, which counts as the first response.
func (c *Context) OpenForm(f Form) error {
	err := c.Port.OpenForm(f)
	if err == nil {
		c.acked, c.placeholder = true, false
	}
	return err
}

// Refuse shows text only to the invoker, whatever state the interaction is in:
//   - before acknowledging, a private reply;
//   - after a public placeholder, the placeholder is deleted and the text follows up privately;
//   - after a private placeholder, the placeholder becomes the text;
//   - after a reply, a private follow-up.
func (c *Context) Refuse(text string) error {
	r := Response{Content: text, Visibility: Private}
	switch {
	case !c.acked:
		return c.Reply(r)
	case c.placeholder && c.ackedAs == Private:
		return c.Port.EditReply(r)
	case c.placeholder:
		if err := c.Port.DeleteReply(); err != nil {
			return err
		}
		return c.Port.FollowUp(r)
	default:
		return c.Port.FollowUp(r)
	}
}
