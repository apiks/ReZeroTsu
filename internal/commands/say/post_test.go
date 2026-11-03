package say

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/interaction"
	"ReZeroTsu/internal/interaction/interactiontest"

	"github.com/bwmarrin/discordgo"
)

func command(t *testing.T, name string) *commands.Command {
	t.Helper()
	for _, c := range commands.AllCommands() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func channelOption(id string) *discordgo.ApplicationCommandInteractionDataOption {
	return &discordgo.ApplicationCommandInteractionDataOption{Name: "channel", Type: discordgo.ApplicationCommandOptionChannel, Value: id}
}

func embedOption(on bool) *discordgo.ApplicationCommandInteractionDataOption {
	return &discordgo.ApplicationCommandInteractionDataOption{Name: "embed", Type: discordgo.ApplicationCommandOptionBoolean, Value: on}
}

// openSay runs /say the way dispatch does for a private direct command: the invoker is "user" in channel "c" of
// guild "g".
func openSay(t *testing.T, opts ...*discordgo.ApplicationCommandInteractionDataOption) *interactiontest.Fake {
	t.Helper()
	cmd := command(t, "say")
	if cmd == nil {
		t.Fatal("/say is not registered")
	}
	fake := &interactiontest.Fake{}
	ic := interactiontest.NewCommandContext(fake, cmd.Visibility, cmd.Response, discordgo.ApplicationCommandInteractionData{Name: "say", Options: opts})
	if err := cmd.Run(ic); err != nil {
		t.Fatal(err)
	}
	return fake
}

func onlyCall(t *testing.T, fake *interactiontest.Fake, op string) interactiontest.Call {
	t.Helper()
	calls := fake.Calls()
	if len(calls) != 1 || calls[0].Op != op {
		t.Fatalf("calls = %v, want only %s", fake.Ops(), op)
	}
	return calls[0]
}

func lastCall(t *testing.T, fake *interactiontest.Fake, op string) interactiontest.Call {
	t.Helper()
	calls := fake.Calls()
	for i := len(calls) - 1; i >= 0; i-- {
		if calls[i].Op == op {
			return calls[i]
		}
	}
	t.Fatalf("no %s call in %v", op, fake.Ops())
	return interactiontest.Call{}
}

func textBox(t *testing.T, f interaction.Form) discordgo.TextInput {
	t.Helper()
	if len(f.Components) != 1 {
		t.Fatalf("form has %d components, want 1", len(f.Components))
	}
	label, ok := f.Components[0].(*discordgo.Label)
	if !ok {
		t.Fatalf("form field is %T, want a Label", f.Components[0])
	}
	box, ok := label.Component.(discordgo.TextInput)
	if !ok {
		t.Fatalf("label holds %T, want a text box", label.Component)
	}
	return box
}

// submitAs runs the form submission handler for a form submitted by userID.
func submitAs(t *testing.T, fake *interactiontest.Fake, customID, userID, typed string) {
	t.Helper()
	data := discordgo.ModalSubmitInteractionData{CustomID: customID, Components: []discordgo.MessageComponent{
		&discordgo.Label{Label: "Message", Component: discordgo.TextInput{CustomID: textBoxID, Value: typed}},
	}}
	i := &discordgo.Interaction{Type: discordgo.InteractionModalSubmit, GuildID: "g", ChannelID: "c", Data: data,
		Member: &discordgo.Member{User: &discordgo.User{ID: userID}}}
	ic := interaction.NewContext(fake, i, nil, interaction.Private, interaction.Direct)
	if err := submitForm(ic, nil); err != nil {
		t.Fatal(err)
	}
}

func submit(t *testing.T, fake *interactiontest.Fake, customID, typed string) {
	t.Helper()
	submitAs(t, fake, customID, "user", typed)
}

func TestSayIsAPrivateDirectModCommandWithChannelAndEmbedOptions(t *testing.T) {
	cmd := command(t, "say")
	if cmd == nil {
		t.Fatal("/say is not registered")
	}
	if cmd.Permission != commands.PermMod || cmd.Context != commands.ContextGuildOnly || cmd.Visibility != commands.Private || cmd.Response != commands.Direct {
		t.Errorf("permission/context/visibility/response = %v/%v/%v/%v", cmd.Permission, cmd.Context, cmd.Visibility, cmd.Response)
	}
	if cmd.Run == nil || cmd.Handler != nil {
		t.Errorf("/say must use the new handler form")
	}
	if len(cmd.Options) != 2 {
		t.Fatalf("options = %+v", cmd.Options)
	}
	ch, em := cmd.Options[0], cmd.Options[1]
	if ch.Name != "channel" || ch.Required || ch.Type != discordgo.ApplicationCommandOptionChannel || !slices.Equal(ch.ChannelTypes, []discordgo.ChannelType{discordgo.ChannelTypeGuildText}) {
		t.Errorf("channel option = %+v", ch)
	}
	if em.Name != "embed" || em.Required || em.Type != discordgo.ApplicationCommandOptionBoolean {
		t.Errorf("embed option = %+v", em)
	}
}

func TestEmbedCommandIsGone(t *testing.T) {
	if command(t, "embed") != nil {
		t.Error("/embed is still registered")
	}
}

func TestSayOpensAParagraphFormLimitedTo2000CharactersForAPlainMessage(t *testing.T) {
	fake := openSay(t)
	call := onlyCall(t, fake, "OpenForm")
	box := textBox(t, call.Form)
	if box.Style != discordgo.TextInputParagraph || box.MaxLength != 2000 || box.Required == nil || !*box.Required {
		t.Errorf("text box = %+v", box)
	}
	if call.Form.CustomID != "say:c:t:user" {
		t.Errorf("custom ID = %q, want the current channel and the plain flag", call.Form.CustomID)
	}
}

func TestSayOpensAFormLimitedTo4000CharactersForAnEmbed(t *testing.T) {
	fake := openSay(t, channelOption("x"), embedOption(true))
	call := onlyCall(t, fake, "OpenForm")
	if box := textBox(t, call.Form); box.MaxLength != 4000 || box.Style != discordgo.TextInputParagraph {
		t.Errorf("text box = %+v", box)
	}
	if call.Form.CustomID != "say:x:e:user" {
		t.Errorf("custom ID = %q, want the chosen channel and the embed flag", call.Form.CustomID)
	}
}

func TestSayWithEmbedFalseIsAPlainMessage(t *testing.T) {
	fake := openSay(t, embedOption(false))
	if box := textBox(t, onlyCall(t, fake, "OpenForm").Form); box.MaxLength != 2000 {
		t.Errorf("max length = %d", box.MaxLength)
	}
}

func TestFormCustomIDStaysUnder100CharactersAndCarriesNoUserText(t *testing.T) {
	long := strings.Repeat("9", 20)
	cmd := command(t, "say")
	fake := &interactiontest.Fake{}
	data := discordgo.ApplicationCommandInteractionData{Name: "say", Options: []*discordgo.ApplicationCommandInteractionDataOption{channelOption(long), embedOption(true)}}
	i := &discordgo.Interaction{Type: discordgo.InteractionApplicationCommand, GuildID: "g", ChannelID: "c", Data: data,
		Member: &discordgo.Member{User: &discordgo.User{ID: long}}}
	if err := cmd.Run(interaction.NewContext(fake, i, nil, cmd.Visibility, cmd.Response)); err != nil {
		t.Fatal(err)
	}
	if id := onlyCall(t, fake, "OpenForm").Form.CustomID; len(id) >= 100 {
		t.Errorf("custom ID is %d characters", len(id))
	}
}

func TestSubmittingPostsTheTextInTheChosenChannelAndRepliesPrivatelyWithALink(t *testing.T) {
	fake := &interactiontest.Fake{}
	submit(t, fake, "say:x:t:user", "line one\nline two")

	posted := lastCall(t, fake, "PostMessage")
	if posted.ChannelID != "x" || posted.Response.Content != "line one\nline two" || len(posted.Response.Embeds) != 0 {
		t.Errorf("posted = %+v", posted)
	}
	reply := lastCall(t, fake, "EditReply").Response.Content
	if !strings.Contains(reply, "https://discord.com/channels/g/x/posted") {
		t.Errorf("reply %q has no link", reply)
	}
	if first := fake.Calls()[0]; first.Op != "Defer" || first.Visibility != interaction.Private {
		t.Errorf("first call = %+v, want a private Defer", first)
	}
}

func TestSubmittingAnEmbedPostsTheTextAsTheEmbedDescription(t *testing.T) {
	fake := &interactiontest.Fake{}
	submit(t, fake, "say:c:e:user", "big news")

	posted := lastCall(t, fake, "PostMessage")
	if posted.ChannelID != "c" || posted.Response.Content != "" || len(posted.Response.Embeds) != 1 || posted.Response.Embeds[0].Description != "big news" {
		t.Errorf("posted = %+v", posted)
	}
	if reply := lastCall(t, fake, "EditReply").Response.Content; !strings.Contains(reply, "https://discord.com/channels/g/c/posted") {
		t.Errorf("reply %q has no link", reply)
	}
}

func TestAnEmbedKeepsAll4000CharactersOfMultibyteText(t *testing.T) {
	fake := &interactiontest.Fake{}
	typed := strings.Repeat("é", 4000)
	submit(t, fake, "say:c:e:user", typed)
	if got := lastCall(t, fake, "PostMessage").Response.Embeds[0].Description; got != typed {
		t.Errorf("description has %d bytes, want %d: the text was cut", len(got), len(typed))
	}
}

func TestSubmittingNothingIsRefusedPrivatelyAndPostsNothing(t *testing.T) {
	for _, typed := range []string{"", "   \n\t "} {
		fake := &interactiontest.Fake{}
		submit(t, fake, "say:c:t:user", typed)
		if slices.Contains(fake.Ops(), "PostMessage") {
			t.Errorf("%q: posted an empty message", typed)
		}
		reply := onlyCall(t, fake, "Reply")
		if reply.Visibility != interaction.Private || !strings.Contains(reply.Response.Content, "Please provide a message.") {
			t.Errorf("%q: reply = %+v", typed, reply)
		}
	}
}

func TestSubmittingMoreThanTheLimitIsRefusedNotCut(t *testing.T) {
	cases := []struct {
		id    string
		typed string
	}{
		{"say:c:t:user", strings.Repeat("a", 2001)},
		{"say:c:e:user", strings.Repeat("a", 4001)},
	}
	for _, tc := range cases {
		fake := &interactiontest.Fake{}
		submit(t, fake, tc.id, tc.typed)
		if slices.Contains(fake.Ops(), "PostMessage") {
			t.Errorf("%s: posted an over-long message", tc.id)
		}
		if r := onlyCall(t, fake, "Reply"); r.Visibility != interaction.Private || !strings.Contains(r.Response.Content, "too long") {
			t.Errorf("%s: reply = %+v", tc.id, r)
		}
	}
}

func TestSubmittingAsSomeoneWhoDidNotOpenTheFormPostsNothing(t *testing.T) {
	fake := &interactiontest.Fake{}
	submitAs(t, fake, "say:c:t:user", "intruder", "hi")
	if slices.Contains(fake.Ops(), "PostMessage") {
		t.Error("posted for a member who did not open the form")
	}
	if r := onlyCall(t, fake, "Reply"); r.Visibility != interaction.Private {
		t.Errorf("reply = %+v", r)
	}
}

func TestAMalformedFormIDIsRefusedAsStale(t *testing.T) {
	fake := &interactiontest.Fake{}
	i := &discordgo.Interaction{Type: discordgo.InteractionModalSubmit, GuildID: "g", ChannelID: "c",
		Data:   discordgo.ModalSubmitInteractionData{CustomID: "say:oops"},
		Member: &discordgo.Member{User: &discordgo.User{ID: "user"}}}
	if err := submitForm(interaction.NewContext(fake, i, nil, interaction.Private, interaction.Direct), nil); err != nil {
		t.Errorf("want a refusal, got error %v", err)
	}
	if r := onlyCall(t, fake, "Reply"); r.Visibility != interaction.Private || r.Response.Content != msgFormStale {
		t.Errorf("reply = %+v", r)
	}
	edit := &discordgo.Interaction{Type: discordgo.InteractionModalSubmit, GuildID: "g", ChannelID: "c",
		Data:   discordgo.ModalSubmitInteractionData{CustomID: "sayedit:oops"},
		Member: &discordgo.Member{User: &discordgo.User{ID: "user"}}}
	fake2 := &interactiontest.Fake{}
	if err := submitEditForm(interaction.NewContext(fake2, edit, nil, interaction.Private, interaction.Direct), nil); err != nil {
		t.Errorf("want a refusal, got error %v", err)
	}
	if r := onlyCall(t, fake2, "Reply"); r.Response.Content != msgEditFormStale {
		t.Errorf("edit reply = %+v", r)
	}
	if slices.Contains(fake.Ops(), "PostMessage") {
		t.Error("posted from a malformed ID")
	}
}

func TestAPermissionFailureNamesTheChannelAndTheMissingPermissions(t *testing.T) {
	forbidden := &discordgo.RESTError{Response: &http.Response{StatusCode: 403}}
	cases := []struct {
		id   string
		want []string
	}{
		{"say:x:t:user", []string{"<#x>", "View Channel", "Send Messages"}},
		{"say:x:e:user", []string{"<#x>", "View Channel", "Send Messages", "Embed Links"}},
	}
	for _, tc := range cases {
		fake := &interactiontest.Fake{Fail: map[string]error{"PostMessage": forbidden}}
		submit(t, fake, tc.id, "hi")
		reply := lastCall(t, fake, "EditReply")
		for _, w := range tc.want {
			if !strings.Contains(reply.Response.Content, w) {
				t.Errorf("%s: reply %q lacks %q", tc.id, reply.Response.Content, w)
			}
		}
		if strings.Contains(reply.Response.Content, "403") {
			t.Errorf("%s: reply leaks the raw error: %q", tc.id, reply.Response.Content)
		}
	}
}

func TestAnUnexpectedPostFailureShowsSomethingWentWrong(t *testing.T) {
	fake := &interactiontest.Fake{Fail: map[string]error{"PostMessage": &discordgo.RESTError{Response: &http.Response{StatusCode: 500}}}}
	submit(t, fake, "say:c:t:user", "hi")
	if reply := lastCall(t, fake, "EditReply").Response.Content; reply != interaction.SomethingWentWrong {
		t.Errorf("reply = %q", reply)
	}
}
