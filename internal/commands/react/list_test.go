package react

import (
	"fmt"
	"strings"
	"testing"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/database/guilds"
	"ReZeroTsu/internal/interaction"
	"ReZeroTsu/internal/interaction/interactiontest"

	"github.com/bwmarrin/discordgo"
)

const (
	roleA = "1000000000000000001"
	roleB = "1000000000000000002"
)

func reactroleList(t *testing.T) *commands.Subcommand {
	t.Helper()
	for _, s := range command(t, "reactrole").Subcommands {
		if s.Name == "list" {
			return s
		}
	}
	t.Fatal("/reactrole has no list subcommand")
	return nil
}

func invokeList(t *testing.T) *interactiontest.Fake {
	t.Helper()
	fake := &interactiontest.Fake{}
	cmd := command(t, "reactrole")
	ic := interactiontest.NewCommandContext(fake, cmd.Visibility, cmd.Response, discordgo.ApplicationCommandInteractionData{Name: "reactrole"})
	if err := ic.Defer(0); err != nil {
		t.Fatal(err)
	}
	if err := reactroleList(t).Run(ic); err != nil {
		t.Fatal(err)
	}
	return fake
}

func listed(t *testing.T, fake *interactiontest.Fake) interaction.Response {
	t.Helper()
	return only(t, fake, "EditReply").Response
}

func allText(r interaction.Response) string {
	var b strings.Builder
	for _, e := range r.Embeds {
		b.WriteString(e.Description)
		for _, f := range e.Fields {
			b.WriteString(f.Name + "\n" + f.Value + "\n")
		}
	}
	return b.String()
}

func pickerIn(r interaction.Response) (discordgo.SelectMenu, bool) {
	for _, row := range r.Components {
		for _, c := range row.(discordgo.ActionsRow).Components {
			if m, ok := c.(discordgo.SelectMenu); ok {
				return m, true
			}
		}
	}
	return discordgo.SelectMenu{}, false
}

func pickRemove(user, customID string, values ...string) (*interaction.Context, *interactiontest.Fake) {
	fake := &interactiontest.Fake{}
	i := &discordgo.Interaction{Type: discordgo.InteractionMessageComponent, GuildID: "g", ChannelID: "c",
		Member: &discordgo.Member{User: &discordgo.User{ID: user}},
		Data:   discordgo.MessageComponentInteractionData{CustomID: customID, ComponentType: discordgo.SelectMenuComponent, Values: values}}
	return interaction.NewContext(fake, i, nil, interaction.Private, interaction.Direct), fake
}

func firstPickerID(t *testing.T, author string) string {
	t.Helper()
	p := RenderReactrolePage(t.Context(), nil, nil, &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{GuildID: "g"}}, 0, 0, author)
	pk, ok := pickerIn(p.Response)
	if !ok {
		t.Fatal("no picker")
	}
	return pk.CustomID
}

func TestListShowsLinksForKnownChannelsAndBareIDsForLegacyEntries(t *testing.T) {
	useStore(t, map[string]*guilds.ReactJoin{
		"m1": {ChannelID: "c1", RoleEmojiMap: []map[string][]string{{roleA: {"😀"}}}},
		"m2": {RoleEmojiMap: []map[string][]string{{roleB: {"name:123"}}}},
	})
	out := allText(listed(t, invokeList(t)))
	if !strings.Contains(out, "https://discord.com/channels/g/c1/m1") {
		t.Errorf("no link for m1 in %q", out)
	}
	if strings.Contains(out, "/m2") {
		t.Errorf("legacy entry m2 got a link in %q", out)
	}
	if !strings.Contains(out, "m2") {
		t.Errorf("legacy entry m2 shows no ID in %q", out)
	}
	if !strings.Contains(out, "😀 → <@&"+roleA+">") || !strings.Contains(out, "<@&"+roleB+">") {
		t.Errorf("bindings missing in %q", out)
	}
}

func TestListWithNothingSetSaysSo(t *testing.T) {
	useStore(t, nil)
	r := listed(t, invokeList(t))
	if len(r.Embeds) != 1 || !strings.Contains(r.Embeds[0].Description, "no reaction roles") {
		t.Errorf("response = %+v", r)
	}
	if _, ok := pickerIn(r); ok {
		t.Error("empty list has a picker")
	}
}

func bigStore(n int) map[string]*guilds.ReactJoin {
	m := map[string]*guilds.ReactJoin{}
	for i := 0; i < n; i++ {
		m[fmt.Sprintf("%018d", i+1)] = &guilds.ReactJoin{ChannelID: "c1", RoleEmojiMap: []map[string][]string{{roleA: {"😀"}}}}
	}
	return m
}

func TestListPagesAndOffersAPickerPerPage(t *testing.T) {
	useStore(t, bigStore(30))
	r := listed(t, invokeList(t))
	if len(r.Embeds[0].Fields) != 25 {
		t.Errorf("first page fields = %d", len(r.Embeds[0].Fields))
	}
	pk, ok := pickerIn(r)
	if !ok || len(pk.Options) != 25 || len(r.Components) != 2 {
		t.Fatalf("picker = %+v ok=%v components=%d", pk, ok, len(r.Components))
	}
	p := RenderReactrolePage(t.Context(), nil, nil, &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{GuildID: "g"}}, 1, 30, "user")
	pk2, ok := pickerIn(p.Response)
	if !ok || len(pk2.Options) != 5 || pk2.Options[0].Value != fmt.Sprintf("%018d", 26) {
		t.Errorf("second page picker = %+v", pk2)
	}
}

func TestRemovePickerDeletesTheEntryEvenWhenTheMessageIsGone(t *testing.T) {
	st := useStore(t, map[string]*guilds.ReactJoin{
		"m1": {ChannelID: "c1", RoleEmojiMap: []map[string][]string{{roleA: {"😀"}}}},
		"m2": {RoleEmojiMap: []map[string][]string{{roleB: {"x:1"}}}},
	})
	id := firstPickerID(t, "user")

	// m1's message was deleted from Discord: taking the reaction off fails, the binding still goes.
	ic, fake := pickRemove("user", id, "m1")
	fake.Fail = map[string]error{"RemoveOwnReaction": &discordgo.RESTError{Message: &discordgo.APIErrorMessage{Code: discordgo.ErrCodeUnknownMessage}}}
	if err := handleRemovePicker(ic, nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := st.entries["m1"]; ok {
		t.Error("m1 still has bindings")
	}
	if _, ok := st.entries["m2"]; !ok {
		t.Error("m2 was removed")
	}
	upd := only(t, fake, "UpdateMessage")
	if out := allText(upd.Response); !strings.Contains(out, "m2") {
		t.Errorf("page not re-rendered: %q", out)
	}

	// m2 has no known channel: removed with no reaction call at all, and the list ends empty.
	ic, fake = pickRemove("user", id, "m2")
	if err := handleRemovePicker(ic, nil); err != nil {
		t.Fatal(err)
	}
	if len(st.entries) != 0 {
		t.Errorf("entries left: %v", st.entries)
	}
	for _, op := range fake.Ops() {
		if op == "RemoveOwnReaction" {
			t.Error("reaction removed for a legacy entry")
		}
	}
	upd = only(t, fake, "UpdateMessage")
	if upd.Response.Components == nil || len(upd.Response.Components) != 0 || !strings.Contains(allText(upd.Response), "no reaction roles") {
		t.Errorf("empty update = %+v", upd.Response)
	}
}

func TestRemovePickerRefusesAnotherMember(t *testing.T) {
	st := useStore(t, map[string]*guilds.ReactJoin{"m1": {ChannelID: "c1", RoleEmojiMap: []map[string][]string{{roleA: {"😀"}}}}})
	id := firstPickerID(t, "user")
	ic, fake := pickRemove("other", id, "m1")
	if err := handleRemovePicker(ic, nil); err != nil {
		t.Fatal(err)
	}
	if len(st.entries) != 1 {
		t.Error("entry removed by a non-owner")
	}
	only(t, fake, "Reply")
}

func TestTheOldReactionRoleCommandsAreGone(t *testing.T) {
	for _, c := range commands.AllCommands() {
		switch c.Name {
		case "add-react-autorole", "remove-react-autorole", "reacts-autorole":
			t.Errorf("%s is still registered", c.Name)
		}
	}
}
