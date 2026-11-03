package commands

import (
	"context"
	"errors"
	"strings"
	"testing"

	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/interaction"
	"ReZeroTsu/internal/interaction/interactiontest"

	"github.com/bwmarrin/discordgo"
)

// registerComponent adds a handler for one test and removes it afterwards.
func registerComponent(t *testing.T, prefix string, h ComponentHandler) {
	t.Helper()
	AddComponent(prefix, h)
	t.Cleanup(func() { delete(componentRegistry, prefix) })
}

func componentInteraction(data discordgo.InteractionData, guildID, userID string, msg *discordgo.Message) *discordgo.InteractionCreate {
	i := &discordgo.Interaction{Type: discordgo.InteractionMessageComponent, ChannelID: "chan", GuildID: guildID, Data: data, Message: msg}
	user := &discordgo.User{ID: userID}
	if guildID != "" {
		i.Member = &discordgo.Member{User: user}
	} else {
		i.User = user
	}
	return &discordgo.InteractionCreate{Interaction: i}
}

func press(customID string) discordgo.MessageComponentInteractionData {
	return discordgo.MessageComponentInteractionData{CustomID: customID, ComponentType: discordgo.ButtonComponent}
}

func dispatchPress(t *testing.T, fake *interactiontest.Fake, i *discordgo.InteractionCreate) {
	t.Helper()
	dispatchComponent(context.Background(), (*database.Client)(nil), fake, nil, i)
}

func TestComponentsRouteByPrefix(t *testing.T) {
	var got []string
	record := func(tag string) ComponentHandler {
		return func(ic *interaction.Context, _ *discordgo.Session) error {
			got = append(got, tag+":"+ic.CustomID)
			return nil
		}
	}
	registerComponent(t, "t-btn:", record("button"))
	registerComponent(t, "t-pick:", record("picker"))
	registerComponent(t, "t-form:", record("form"))
	registerComponent(t, "t-btn:long:", record("longer"))

	var pickerValues []string
	registerComponent(t, "t-values:", func(ic *interaction.Context, _ *discordgo.Session) error {
		pickerValues = ic.Values
		return nil
	})
	var submitted *discordgo.ModalSubmitInteractionData
	registerComponent(t, "t-sub:", func(ic *interaction.Context, _ *discordgo.Session) error {
		submitted = ic.Submitted
		return nil
	})

	fake := &interactiontest.Fake{}
	dispatchPress(t, fake, componentInteraction(press("t-btn:1"), "g", "u", nil))
	dispatchPress(t, fake, componentInteraction(discordgo.MessageComponentInteractionData{CustomID: "t-pick:x", ComponentType: discordgo.SelectMenuComponent}, "g", "u", nil))
	form := componentInteraction(discordgo.ModalSubmitInteractionData{CustomID: "t-form:7"}, "g", "u", nil)
	form.Type = discordgo.InteractionModalSubmit
	dispatchPress(t, fake, form)
	dispatchPress(t, fake, componentInteraction(press("t-btn:long:2"), "g", "u", nil))

	want := []string{"button:t-btn:1", "picker:t-pick:x", "form:t-form:7", "longer:t-btn:long:2"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("routed %v, want %v", got, want)
	}

	dispatchPress(t, fake, componentInteraction(discordgo.MessageComponentInteractionData{CustomID: "t-values:1", Values: []string{"a", "b"}}, "g", "u", nil))
	if strings.Join(pickerValues, ",") != "a,b" {
		t.Errorf("picker values = %v, want a,b", pickerValues)
	}
	sub := componentInteraction(discordgo.ModalSubmitInteractionData{CustomID: "t-sub:1"}, "g", "u", nil)
	sub.Type = discordgo.InteractionModalSubmit
	dispatchPress(t, fake, sub)
	if submitted == nil || submitted.CustomID != "t-sub:1" {
		t.Errorf("form submission not handed to the handler: %+v", submitted)
	}
	if len(fake.Calls()) != 0 {
		t.Errorf("handlers replied nothing, but the port saw %v", fake.Ops())
	}
}

func TestUnregisteredComponentIsIgnored(t *testing.T) {
	fake := &interactiontest.Fake{}
	dispatchPress(t, fake, componentInteraction(press("nobody-owns-this"), "g", "u", nil))
	if len(fake.Calls()) != 0 {
		t.Errorf("an unowned custom ID should be ignored as before, saw %v", fake.Ops())
	}
}

func TestComponentHandlerFailureIsPrivate(t *testing.T) {
	registerComponent(t, "t-fail:", func(*interaction.Context, *discordgo.Session) error { return errors.New("boom") })
	registerComponent(t, "t-panic:", func(*interaction.Context, *discordgo.Session) error { panic("boom") })
	for _, id := range []string{"t-fail:1", "t-panic:1"} {
		fake := &interactiontest.Fake{}
		dispatchPress(t, fake, componentInteraction(press(id), "g", "u", nil))
		calls := fake.Calls()
		if len(calls) != 1 || calls[0].Op != "Reply" || calls[0].Visibility != interaction.Private || calls[0].Response.Content != errMessage {
			t.Errorf("%s: want one private %q reply, got %+v", id, errMessage, calls)
		}
	}
}

func TestCustomIDsStayWithinDiscordLimit(t *testing.T) {
	id := BuildPaginationCustomID("reacts-autorole", 1000000, 1000000, "123456789012345678")
	if len(id) > 100 {
		t.Errorf("custom ID is %d characters, Discord allows 100: %s", len(id), id)
	}
}
