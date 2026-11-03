package interactiontest

import (
	"strings"
	"testing"

	"ReZeroTsu/internal/interaction"

	"github.com/bwmarrin/discordgo"
)

func TestFakeRejectsPayloadsDiscordWould(t *testing.T) {
	long := func(n int) string { return strings.Repeat("a", n) }
	menu := func(options int, value string) discordgo.MessageComponent {
		opts := make([]discordgo.SelectMenuOption, options)
		for i := range opts {
			opts[i] = discordgo.SelectMenuOption{Label: "x", Value: value}
		}
		return discordgo.ActionsRow{Components: []discordgo.MessageComponent{discordgo.SelectMenu{CustomID: "m", Options: opts}}}
	}
	fields := func(n, valueLen int) []*discordgo.MessageEmbedField {
		out := make([]*discordgo.MessageEmbedField, n)
		for i := range out {
			out[i] = &discordgo.MessageEmbedField{Name: "n", Value: long(valueLen)}
		}
		return out
	}
	cases := []struct {
		name string
		resp interaction.Response
		want string // empty: accepted
	}{
		{"fine", interaction.Response{Content: long(2000), Embeds: []*discordgo.MessageEmbed{{Title: long(256), Description: long(4000)}}}, ""},
		{"content", interaction.Response{Content: long(2001)}, "message content"},
		{"embed total", interaction.Response{Embeds: []*discordgo.MessageEmbed{{Description: long(4000), Fields: fields(3, 1000)}}}, "all embed text together"},
		{"field value", interaction.Response{Embeds: []*discordgo.MessageEmbed{{Fields: fields(1, 1025)}}}, "embed field value"},
		{"field count", interaction.Response{Embeds: []*discordgo.MessageEmbed{{Fields: fields(26, 1)}}}, "26 embed fields"},
		{"button custom ID", interaction.Response{Components: []discordgo.MessageComponent{discordgo.ActionsRow{Components: []discordgo.MessageComponent{discordgo.Button{CustomID: long(101)}}}}}, "button custom ID"},
		{"option value", interaction.Response{Components: []discordgo.MessageComponent{menu(1, long(101))}}, "select option value"},
		{"option count", interaction.Response{Components: []discordgo.MessageComponent{menu(26, "v")}}, "26 select options"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &Fake{}
			err := f.Reply(c.resp)
			if c.want == "" {
				if err != nil || len(f.Calls()) != 1 {
					t.Fatalf("err %v, calls %d, want accepted", err, len(f.Calls()))
				}
				return
			}
			if err == nil || len(f.Calls()) != 0 || len(f.Violations()) == 0 || !strings.Contains(f.Violations()[0], c.want) {
				t.Fatalf("err %v, calls %d, violations %v, want rejection naming %q", err, len(f.Calls()), f.Violations(), c.want)
			}
		})
	}
}

func TestFakeRejectsFormsAndChoicesDiscordWould(t *testing.T) {
	box := func(maxLen int, value string) discordgo.MessageComponent {
		return discordgo.Label{Label: "Text", Component: discordgo.TextInput{CustomID: "t", MaxLength: maxLen, Value: value}}
	}
	f := &Fake{}
	if err := f.OpenForm(interaction.Form{CustomID: "ok", Title: "Title", Components: []discordgo.MessageComponent{box(4000, "x")}}); err != nil {
		t.Fatalf("a valid form was rejected: %v", err)
	}
	for name, form := range map[string]interaction.Form{
		"custom id": {CustomID: strings.Repeat("a", 101), Title: "T"},
		"title":     {CustomID: "c", Title: strings.Repeat("a", 46)},
		"box limit": {CustomID: "c", Title: "T", Components: []discordgo.MessageComponent{box(6000, "")}},
		"prefilled": {CustomID: "c", Title: "T", Components: []discordgo.MessageComponent{box(4000, strings.Repeat("a", 4001))}},
	} {
		if err := (&Fake{}).OpenForm(form); err == nil {
			t.Errorf("%s: a form Discord would reject was accepted", name)
		}
	}

	many := make([]*discordgo.ApplicationCommandOptionChoice, 26)
	for i := range many {
		many[i] = &discordgo.ApplicationCommandOptionChoice{Name: "n", Value: "v"}
	}
	if err := (&Fake{}).Suggest(many); err == nil {
		t.Error("26 choices were accepted")
	}
	if err := (&Fake{}).Suggest([]*discordgo.ApplicationCommandOptionChoice{{Name: strings.Repeat("a", 101), Value: "v"}}); err == nil {
		t.Error("a 101-character choice name was accepted")
	}
}
