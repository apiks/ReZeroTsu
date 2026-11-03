package say

import (
	"strings"
	"unicode/utf8"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/interaction"

	"github.com/bwmarrin/discordgo"
)

const (
	// editPrefix starts the custom ID of the edit form: "sayedit:<channel ID>:<message ID>:<t or e>:<opener's
	// user ID>". Three snowflakes and a few separators stay far under Discord's 100-character limit.
	editPrefix = "sayedit:"

	msgEditFormStale   = "That form is no longer valid. Right-click the message and try again."
	msgOnlySay         = "I can only edit messages I posted with /say"
	msgDescriptionLong = "That embed's description is longer than 4000 characters, which the edit form cannot hold."
)

func init() {
	commands.Add(&commands.Command{
		Name:       "Edit message",
		Kind:       commands.KindMessage,
		Visibility: commands.Private,
		Response:   commands.Direct,
		Permission: commands.PermMod,
		Context:    commands.ContextGuildOnly,
		Module:     "misc",
		Run:        runEditMessage,
	})
	commands.AddComponent(editPrefix, submitEditForm)
}

// editKind classifies a message the bot may edit: the prefill text, whether it is an embed post, and ok false
// when it is not a plain or embed post made by the bot itself.
func editKind(msg *discordgo.Message, botID string) (text string, embed bool, ok bool) {
	if msg == nil || msg.WebhookID != "" || len(msg.Components) > 0 || msg.Author == nil || msg.Author.ID != botID {
		return "", false, false
	}
	switch {
	case msg.Content != "" && len(msg.Embeds) == 0:
		return msg.Content, false, true
	case msg.Content == "" && len(msg.Embeds) == 1:
		return msg.Embeds[0].Description, true, true
	}
	return "", false, false
}

func editFormID(channelID, messageID string, embed bool, openerID string) string {
	flag := "t"
	if embed {
		flag = "e"
	}
	return editPrefix + channelID + ":" + messageID + ":" + flag + ":" + openerID
}

func parseEditFormID(id string) (channelID, messageID string, embed bool, openerID string, ok bool) {
	parts := strings.Split(strings.TrimPrefix(id, editPrefix), ":")
	if !strings.HasPrefix(id, editPrefix) || len(parts) != 4 || parts[0] == "" || parts[1] == "" || parts[3] == "" || (parts[2] != "t" && parts[2] != "e") {
		return "", "", false, "", false
	}
	return parts[0], parts[1], parts[2] == "e", parts[3], true
}

// runEditMessage opens the /say form prefilled with the right-clicked message's text.
// Guard homes: the bot-authorship, webhook and components checks live in editKind; an over-long embed
// description is refused here because the form box holds at most 4000 characters.
func runEditMessage(ic *interaction.Context) error {
	msg := ic.TargetMessage()
	text, embed, ok := editKind(msg, ic.BotUser().ID)
	if !ok {
		return ic.Refuse(msgOnlySay)
	}
	if utf8.RuneCountInString(text) > maxLength(embed) {
		return ic.Refuse(msgDescriptionLong)
	}
	box := interaction.TextBox(textBoxID, "Message", discordgo.TextInputParagraph, true)
	input := box.Component.(discordgo.TextInput)
	input.MaxLength = maxLength(embed)
	input.Value = text
	box.Component = input

	title := "Edit message"
	if embed {
		title = "Edit embed"
	}
	return ic.OpenForm(interaction.Form{
		CustomID:   editFormID(msg.ChannelID, msg.ID, embed, ic.Invoker.ID),
		Title:      title,
		Components: []discordgo.MessageComponent{box},
	})
}

// submitEditForm edits the message in place. The message is read again, so a message that changed shape since
// the form opened is refused, and an embed keeps its styling: only its description is replaced.
func submitEditForm(ic *interaction.Context, _ *discordgo.Session) error {
	channelID, messageID, embed, openerID, ok := parseEditFormID(ic.CustomID)
	if !ok {
		return ic.Refuse(msgEditFormStale)
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

	needs := int64(discordgo.PermissionViewChannel | discordgo.PermissionReadMessageHistory)
	msg, err := ic.ReadMessage(channelID, messageID)
	if err != nil {
		return ic.Fail(err, interaction.ErrorContext{ChannelID: channelID, Needs: needs})
	}
	if _, isEmbed, ok := editKind(msg, ic.BotUser().ID); !ok || isEmbed != embed {
		return ic.Refuse(msgOnlySay)
	}

	if err := ic.Defer(interaction.Private); err != nil {
		return err
	}
	edit := interaction.Response{Content: text}
	if embed {
		kept := *msg.Embeds[0]
		kept.Description = text
		edit = interaction.Response{Embeds: []*discordgo.MessageEmbed{&kept}}
	}
	if err := ic.EditMessage(channelID, messageID, edit); err != nil {
		return ic.Fail(err, interaction.ErrorContext{ChannelID: channelID, Needs: needs})
	}
	link := commands.MessageLink(ic.GuildID, channelID, messageID)
	return ic.EditReply(interaction.Response{Content: "Edited the message in " + commands.ChannelMention(channelID) + ": " + link})
}
