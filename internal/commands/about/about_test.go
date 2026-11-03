package about

import (
	"testing"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/interaction"
	"ReZeroTsu/internal/interaction/interactiontest"

	"github.com/bwmarrin/discordgo"
)

func TestAboutRepliesOncePubliclyWithTheBotsDetails(t *testing.T) {
	fake := &interactiontest.Fake{Bot: &discordgo.User{ID: "99", Username: "Tsu"}}
	ic := interactiontest.NewCommandContext(fake, interaction.Public, interaction.Direct, discordgo.ApplicationCommandInteractionData{Name: "about"})
	if err := run(ic); err != nil {
		t.Fatal(err)
	}
	calls := fake.Calls()
	if len(calls) != 1 || calls[0].Op != "Reply" || calls[0].Visibility != interaction.Public {
		t.Fatalf("calls = %+v, want exactly one public Reply", calls)
	}
	if emb := calls[0].Response.Embeds[0]; emb.Title != "Tsu" || emb.URL != topGGBase+"99" {
		t.Errorf("embed = %+v", emb)
	}
}

func TestAboutIsRegisteredAsAPublicDirectCommand(t *testing.T) {
	for _, c := range commands.AllCommands() {
		if c.Name == "about" {
			if c.Run == nil || c.Handler != nil || c.Visibility != commands.Public || c.Response != commands.Direct {
				t.Errorf("about registration = %+v", c)
			}
			return
		}
	}
	t.Fatal("about is not registered")
}
