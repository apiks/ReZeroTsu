package interaction_test

import (
	"encoding/json"
	"testing"

	"ReZeroTsu/internal/interaction"

	"github.com/bwmarrin/discordgo"
)

func TestLabelWrapsAComponentInTheFormPayload(t *testing.T) {
	box := interaction.TextBox("body", "Message", discordgo.TextInputParagraph, true)
	picker := interaction.Picker("Pick a role", "Who gets it", discordgo.SelectMenu{CustomID: "role", MenuType: discordgo.RoleSelectMenu})

	raw, err := json.Marshal([]discordgo.MessageComponent{box, picker})
	if err != nil {
		t.Fatal(err)
	}
	var got []map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got[0]["type"] != float64(18) || got[0]["label"] != "Message" {
		t.Errorf("text box label = %v, want type 18 labelled Message", got[0])
	}
	inner := got[0]["component"].(map[string]any)
	if inner["type"] != float64(discordgo.TextInputComponent) || inner["custom_id"] != "body" || inner["style"] != float64(discordgo.TextInputParagraph) || inner["required"] != true {
		t.Errorf("text box inner = %v", inner)
	}
	if got[1]["description"] != "Who gets it" || got[1]["component"].(map[string]any)["type"] != float64(discordgo.RoleSelectMenuComponent) {
		t.Errorf("picker label = %v", got[1])
	}
}

func TestFormValueReadsASubmittedTextBox(t *testing.T) {
	i := &discordgo.Interaction{Type: discordgo.InteractionModalSubmit, Data: discordgo.ModalSubmitInteractionData{
		CustomID: "f:1",
		Components: []discordgo.MessageComponent{
			&discordgo.ActionsRow{Components: []discordgo.MessageComponent{&discordgo.TextInput{CustomID: "body", Value: "hello"}}},
			&discordgo.Label{Label: "Other", Component: &discordgo.TextInput{CustomID: "other", Value: "x"}},
		},
	}}
	ic := interaction.NewContext(nil, i, nil, interaction.Private, interaction.Direct)
	if got := ic.FormValue("body"); got != "hello" {
		t.Errorf("FormValue(body) = %q, want hello", got)
	}
	if got := ic.FormValue("other"); got != "x" {
		t.Errorf("FormValue(other) = %q, want x", got)
	}
	if got := ic.FormValue("missing"); got != "" {
		t.Errorf("FormValue(missing) = %q, want empty", got)
	}
}

func TestContextExposesARightClickTarget(t *testing.T) {
	user := &discordgo.User{ID: "t-user"}
	msg := &discordgo.Message{ID: "t-msg"}
	i := &discordgo.Interaction{Type: discordgo.InteractionApplicationCommand, Data: discordgo.ApplicationCommandInteractionData{
		TargetID: "t-user",
		Resolved: &discordgo.ApplicationCommandInteractionDataResolved{Users: map[string]*discordgo.User{"t-user": user}, Messages: map[string]*discordgo.Message{"t-msg": msg}},
	}}
	ic := interaction.NewContext(nil, i, nil, interaction.Private, interaction.Direct)
	if ic.TargetUser() != user || ic.TargetMessage() != nil {
		t.Errorf("target = %v / %v, want the user only", ic.TargetUser(), ic.TargetMessage())
	}
	ic.Command.TargetID = "t-msg"
	if ic.TargetMessage() != msg || ic.TargetUser() != nil {
		t.Errorf("target = %v / %v, want the message only", ic.TargetUser(), ic.TargetMessage())
	}
}
