package interaction_test

import (
	"strings"
	"testing"

	"ReZeroTsu/internal/interaction"
	"ReZeroTsu/internal/interaction/interactiontest"

	"github.com/bwmarrin/discordgo"
)

func newContext(f *interactiontest.Fake, v interaction.Visibility, m interaction.ResponseMode) *interaction.Context {
	i := &discordgo.Interaction{Type: discordgo.InteractionApplicationCommand, GuildID: "g", ChannelID: "c",
		Member: &discordgo.Member{User: &discordgo.User{ID: "u"}}}
	return interaction.NewContext(f, i, nil, v, m)
}

func TestContextRepliesWithTheDeclaredVisibilityUnlessOverridden(t *testing.T) {
	f := &interactiontest.Fake{}
	ic := newContext(f, interaction.Private, interaction.Direct)
	_ = ic.Reply(interaction.Response{Content: "a"})
	_ = ic.FollowUp(interaction.Response{Content: "b", Visibility: interaction.Public})
	calls := f.Calls()
	if calls[0].Visibility != interaction.Private || calls[1].Visibility != interaction.Public {
		t.Errorf("visibilities = %v, %v; want private then public", calls[0].Visibility, calls[1].Visibility)
	}
}

func TestContextRefuseShowsTextPrivatelyInEveryState(t *testing.T) {
	tests := []struct {
		name  string
		setup func(ic *interaction.Context)
		want  string
	}{
		{"before acknowledging", func(*interaction.Context) {}, "Reply"},
		{"after a public placeholder", func(ic *interaction.Context) { _ = ic.Defer(interaction.Public) }, "Defer,DeleteReply,FollowUp"},
		{"after a private placeholder", func(ic *interaction.Context) { _ = ic.Defer(interaction.Private) }, "Defer,EditReply"},
		{"after a public reply", func(ic *interaction.Context) {
			_ = ic.Reply(interaction.Response{Content: "x", Visibility: interaction.Public})
		}, "Reply,FollowUp"},
		{"after a form opened", func(ic *interaction.Context) { _ = ic.OpenForm(interaction.Form{CustomID: "f"}) }, "OpenForm,FollowUp"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &interactiontest.Fake{}
			ic := newContext(f, interaction.Public, interaction.Deferred)
			tt.setup(ic)
			if err := ic.Refuse("nope"); err != nil {
				t.Fatal(err)
			}
			if got := strings.Join(f.Ops(), ","); got != tt.want {
				t.Fatalf("ops = %s, want %s", got, tt.want)
			}
			last := f.Calls()[len(f.Calls())-1]
			if last.Response.Content != "nope" || (last.Op != "EditReply" && last.Visibility != interaction.Private) {
				t.Errorf("last call = %+v, want the private text", last)
			}
		})
	}
}
