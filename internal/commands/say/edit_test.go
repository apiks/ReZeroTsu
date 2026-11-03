package say

import (
	"slices"
	"strings"
	"testing"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/interaction"
	"ReZeroTsu/internal/interaction/interactiontest"

	"github.com/bwmarrin/discordgo"
)

const (
	botID    = "bot"
	onlySay  = "I can only edit messages I posted with /say"
	editName = "Edit message"
)

func botPost(content string, embeds ...*discordgo.MessageEmbed) *discordgo.Message {
	return &discordgo.Message{ID: "m", ChannelID: "c", Content: content, Embeds: embeds, Author: &discordgo.User{ID: botID}}
}

func newFake(msgs ...*discordgo.Message) *interactiontest.Fake {
	f := &interactiontest.Fake{Bot: &discordgo.User{ID: botID}, Messages: map[string]*discordgo.Message{}}
	for _, m := range msgs {
		f.Messages["c/"+m.ID] = m
	}
	return f
}

func openEdit(t *testing.T, msg *discordgo.Message) *interactiontest.Fake {
	t.Helper()
	cmd := command(t, editName)
	if cmd == nil {
		t.Fatal("Edit message is not registered")
	}
	fake := newFake()
	data := discordgo.ApplicationCommandInteractionData{Name: editName, TargetID: "m",
		Resolved: &discordgo.ApplicationCommandInteractionDataResolved{Messages: map[string]*discordgo.Message{"m": msg}}}
	ic := interactiontest.NewCommandContext(fake, cmd.Visibility, cmd.Response, data)
	if err := cmd.Run(ic); err != nil {
		t.Fatal(err)
	}
	return fake
}

func submitEdit(t *testing.T, fake *interactiontest.Fake, customID, userID, typed string) {
	t.Helper()
	data := discordgo.ModalSubmitInteractionData{CustomID: customID, Components: []discordgo.MessageComponent{
		&discordgo.Label{Label: "Message", Component: discordgo.TextInput{CustomID: textBoxID, Value: typed}},
	}}
	i := &discordgo.Interaction{Type: discordgo.InteractionModalSubmit, GuildID: "g", ChannelID: "c", Data: data,
		Member: &discordgo.Member{User: &discordgo.User{ID: userID}}}
	if err := submitEditForm(interaction.NewContext(fake, i, nil, interaction.Private, interaction.Direct), nil); err != nil {
		t.Fatal(err)
	}
}

func TestEditMessageIsAPrivateDirectModMessageCommandAndTheOldEditsAreGone(t *testing.T) {
	cmd := command(t, editName)
	if cmd == nil || cmd.Kind != commands.KindMessage || cmd.Run == nil || cmd.Permission != commands.PermMod ||
		cmd.Context != commands.ContextGuildOnly || cmd.Visibility != interaction.Private || cmd.Response != interaction.Direct {
		t.Fatalf("command = %+v", cmd)
	}
	if command(t, "edit") != nil || command(t, "edit-embed") != nil {
		t.Error("/edit or /edit-embed is still registered")
	}
}

func TestEditingAPlainPostPrefillsTheText(t *testing.T) {
	call := onlyCall(t, openEdit(t, botPost("hello")), "OpenForm")
	box := textBox(t, call.Form)
	if box.Value != "hello" || box.MaxLength != 2000 || box.Style != discordgo.TextInputParagraph {
		t.Errorf("box = %+v", box)
	}
	if call.Form.CustomID != "sayedit:c:m:t:user" {
		t.Errorf("custom ID = %q", call.Form.CustomID)
	}
}

func TestEditingAnEmbedPostPrefillsTheDescription(t *testing.T) {
	call := onlyCall(t, openEdit(t, botPost("", &discordgo.MessageEmbed{Description: "big"})), "OpenForm")
	if box := textBox(t, call.Form); box.Value != "big" || box.MaxLength != 4000 {
		t.Errorf("box = %+v", box)
	}
	if call.Form.CustomID != "sayedit:c:m:e:user" {
		t.Errorf("custom ID = %q", call.Form.CustomID)
	}
}

func TestEditRefusals(t *testing.T) {
	emb := &discordgo.MessageEmbed{Description: "x"}
	webhook := botPost("hi")
	webhook.WebhookID = "w"
	withComponents := botPost("hi")
	withComponents.Components = []discordgo.MessageComponent{&discordgo.ActionsRow{}}
	someoneElse := botPost("hi")
	someoneElse.Author = &discordgo.User{ID: "other"}
	cases := map[string]struct {
		msg  *discordgo.Message
		want string
	}{
		"webhook":        {webhook, onlySay},
		"components":     {withComponents, onlySay},
		"not the bot":    {someoneElse, onlySay},
		"text and embed": {botPost("hi", emb), onlySay},
		"two embeds":     {botPost("", emb, emb), onlySay},
		"empty":          {botPost(""), onlySay},
		"long embed":     {botPost("", &discordgo.MessageEmbed{Description: strings.Repeat("a", 4001)}), "longer than 4000"},
	}
	for name, tc := range cases {
		r := onlyCall(t, openEdit(t, tc.msg), "Reply")
		if r.Visibility != interaction.Private || !strings.Contains(r.Response.Content, tc.want) {
			t.Errorf("%s: reply = %+v", name, r)
		}
	}
}

func TestSubmittingAPlainEditEditsInPlace(t *testing.T) {
	fake := newFake(botPost("old"))
	submitEdit(t, fake, "sayedit:c:m:t:user", "user", "new")
	e := lastCall(t, fake, "EditMessage")
	if e.ChannelID != "c" || e.MessageID != "m" || e.Response.Content != "new" || len(e.Response.Embeds) != 0 {
		t.Errorf("edit = %+v", e)
	}
	if r := lastCall(t, fake, "EditReply").Response.Content; !strings.Contains(r, "https://discord.com/channels/g/c/m") {
		t.Errorf("reply %q", r)
	}
}

func TestSubmittingAnEmbedEditKeepsItsStyling(t *testing.T) {
	old := &discordgo.MessageEmbed{Description: "old", Color: 123, Title: "T"}
	fake := newFake(botPost("", old))
	submitEdit(t, fake, "sayedit:c:m:e:user", "user", "new")
	e := lastCall(t, fake, "EditMessage")
	if len(e.Response.Embeds) != 1 || e.Response.Embeds[0].Description != "new" || e.Response.Embeds[0].Color != 123 || e.Response.Embeds[0].Title != "T" || e.Response.Content != "" {
		t.Errorf("edit = %+v", e)
	}
	if old.Description != "old" {
		t.Error("mutated the fetched embed")
	}
}

func TestSubmittingAnEditGuards(t *testing.T) {
	cases := []struct{ name, id, user, typed, want string }{
		{"not opener", "sayedit:c:m:t:user", "intruder", "x", "Only the member"},
		{"empty", "sayedit:c:m:t:user", "user", "  ", "Please provide a message."},
		{"too long", "sayedit:c:m:t:user", "user", strings.Repeat("a", 2001), "too long"},
		{"kind changed", "sayedit:c:m:e:user", "user", "x", onlySay},
	}
	for _, tc := range cases {
		fake := newFake(botPost("old"))
		submitEdit(t, fake, tc.id, tc.user, tc.typed)
		if slices.Contains(fake.Ops(), "EditMessage") {
			t.Errorf("%s: edited", tc.name)
		}
		if r := lastCall(t, fake, "Reply"); r.Visibility != interaction.Private || !strings.Contains(r.Response.Content, tc.want) {
			t.Errorf("%s: reply %+v", tc.name, r)
		}
	}
}
