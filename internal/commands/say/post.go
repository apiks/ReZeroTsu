package say

import (
	"strings"
	"unicode/utf8"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/discord"
	"ReZeroTsu/internal/interaction"

	"github.com/bwmarrin/discordgo"
)

const (
	// formPrefix starts the custom ID of the /say form: "say:<channel ID>:<t or e>:<opener's user ID>". It
	// carries the target channel, the embed flag and who opened the form, never user text, and stays far under
	// Discord's 100-character limit (a snowflake is at most 20 digits).
	formPrefix = "say:"
	textBoxID  = "text"

	maxPlainLength = 2000
	// maxEmbedLength is the form's cap for an embed. An embed description holds 4096, but a form text box
	// holds at most 4000.
	maxEmbedLength = 4000

	msgNoMessage    = "Please provide a message."
	msgFormStale    = "That form is no longer valid. Run /say again."
	msgNotOpener    = "Only the member who opened this form can submit it."
	msgTooLongPlain = "That message is too long. A message can hold up to 2000 characters."
	msgTooLongEmbed = "That message is too long. An embed can hold up to 4000 characters."
)

func init() {
	commands.Add(&commands.Command{
		Name:       "say",
		Desc:       "Opens a form to post a message from the bot, as text or as an embed.",
		Visibility: commands.Private,
		Response:   commands.Direct,
		Permission: commands.PermMod,
		Context:    commands.ContextGuildOnly,
		Module:     "misc",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:         discordgo.ApplicationCommandOptionChannel,
				Name:         "channel",
				Description:  "The channel to post in (default: current channel).",
				ChannelTypes: []discordgo.ChannelType{discordgo.ChannelTypeGuildText},
			},
			{
				Type:        discordgo.ApplicationCommandOptionBoolean,
				Name:        "embed",
				Description: "Post the message as an embed.",
			},
		},
		Run: runSay,
	})
	commands.AddComponent(formPrefix, submitForm)
}

// maxLength is the longest message the form accepts.
func maxLength(embed bool) int {
	if embed {
		return maxEmbedLength
	}
	return maxPlainLength
}

// formID builds the custom ID of the form.
func formID(channelID string, embed bool, openerID string) string {
	flag := "t"
	if embed {
		flag = "e"
	}
	return formPrefix + channelID + ":" + flag + ":" + openerID
}

// parseFormID is the inverse of formID; ok is false for an ID this command did not build.
func parseFormID(id string) (channelID string, embed bool, openerID string, ok bool) {
	parts := strings.Split(strings.TrimPrefix(id, formPrefix), ":")
	if !strings.HasPrefix(id, formPrefix) || len(parts) != 3 || parts[0] == "" || parts[2] == "" || (parts[1] != "t" && parts[1] != "e") {
		return "", false, "", false
	}
	return parts[0], parts[1] == "e", parts[2], true
}

// runSay opens the form. The text box is required and capped at the length the chosen kind of message allows.
// Guard homes: the old "Please provide a message." check (empty message) is the required text box, repeated on
// submit for a whitespace-only box; the default channel is resolved here into the custom ID; the channel
// types are the option's ChannelTypes; the send failure text lives in submitForm.
func runSay(ic *interaction.Context) error {
	channelID := ic.ChannelID
	if opt := commands.ParseOption(ic.Command.Options, "channel"); opt != nil {
		if id, ok := opt.Value.(string); ok && id != "" {
			channelID = id
		}
	}
	embed := false
	if opt := commands.ParseOption(ic.Command.Options, "embed"); opt != nil {
		embed, _ = opt.Value.(bool)
	}

	box := interaction.TextBox(textBoxID, "Message", discordgo.TextInputParagraph, true)
	input := box.Component.(discordgo.TextInput)
	input.MaxLength = maxLength(embed)
	box.Component = input

	title := "Say"
	if embed {
		title = "Say as an embed"
	}
	return ic.OpenForm(interaction.Form{
		CustomID:   formID(channelID, embed, ic.Invoker.ID),
		Title:      title,
		Components: []discordgo.MessageComponent{box},
	})
}

// submitForm posts the typed text in the target channel and answers privately with a link.
// Guards: a form can only be submitted by the member it was shown to, and the custom ID records who that was,
// so a different presser is refused as well. The moderator check ran when the form was opened; the opener is
// the only one who can submit, so it is not repeated.
func submitForm(ic *interaction.Context, _ *discordgo.Session) error {
	channelID, embed, openerID, ok := parseFormID(ic.CustomID)
	if !ok {
		return ic.Refuse(msgFormStale)
	}
	if ic.Invoker == nil || ic.Invoker.ID != openerID {
		return ic.Refuse(msgNotOpener)
	}
	text := ic.FormValue(textBoxID)
	if strings.TrimSpace(text) == "" {
		return ic.Refuse(msgNoMessage)
	}
	if utf8.RuneCountInString(text) > maxLength(embed) {
		if embed {
			return ic.Refuse(msgTooLongEmbed)
		}
		return ic.Refuse(msgTooLongPlain)
	}

	if err := ic.Defer(interaction.Private); err != nil {
		return err
	}
	post := interaction.Response{Content: text}
	needs := int64(discordgo.PermissionViewChannel | discordgo.PermissionSendMessages)
	if embed {
		// Built here, not with commands.NewEmbed, which cuts by bytes and would clip multibyte text.
		post = interaction.Response{Embeds: []*discordgo.MessageEmbed{{Description: text, Color: discord.EmbedColor}}}
		needs |= discordgo.PermissionEmbedLinks
	}
	msg, err := ic.PostMessage(channelID, post)
	if err != nil {
		return ic.Fail(err, interaction.ErrorContext{ChannelID: channelID, Needs: needs})
	}
	link := commands.MessageLink(ic.GuildID, msg.ChannelID, msg.ID)
	return ic.EditReply(interaction.Response{Content: "Posted in " + commands.ChannelMention(msg.ChannelID) + ": " + link})
}
