package invite

import (
	"strings"
	"testing"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/interaction"
	"ReZeroTsu/internal/interaction/interactiontest"

	"github.com/bwmarrin/discordgo"
)

func TestInviteRepliesOncePubliclyWithTheInviteLink(t *testing.T) {
	fake := &interactiontest.Fake{Bot: &discordgo.User{ID: "99", Username: "Tsu"}}
	ic := interactiontest.NewCommandContext(fake, interaction.Public, interaction.Direct, discordgo.ApplicationCommandInteractionData{Name: "invite"})
	if err := run(ic); err != nil {
		t.Fatal(err)
	}
	calls := fake.Calls()
	if len(calls) != 1 || calls[0].Op != "Reply" || calls[0].Visibility != interaction.Public {
		t.Fatalf("calls = %+v, want exactly one public Reply", calls)
	}
	if emb := calls[0].Response.Embeds[0]; !strings.Contains(emb.URL, "client_id=99") {
		t.Errorf("embed URL = %q", emb.URL)
	}
}

func TestInviteIsRegisteredAsAPublicDirectCommand(t *testing.T) {
	for _, c := range commands.AllCommands() {
		if c.Name == "invite" {
			if c.Run == nil || c.Visibility != commands.Public || c.Response != commands.Direct {
				t.Errorf("invite registration = %+v", c)
			}
			return
		}
	}
	t.Fatal("invite is not registered")
}
