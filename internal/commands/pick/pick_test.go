package pick

import (
	"strings"
	"testing"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/discord"
	"ReZeroTsu/internal/interaction"
	"ReZeroTsu/internal/interaction/interactiontest"

	"github.com/bwmarrin/discordgo"
)

func pickFrom(items string) *interactiontest.Fake {
	data := discordgo.ApplicationCommandInteractionData{Name: "pick", Options: []*discordgo.ApplicationCommandInteractionDataOption{
		{Name: "items", Type: discordgo.ApplicationCommandOptionString, Value: items},
	}}
	fake := &interactiontest.Fake{}
	if err := run(interactiontest.NewCommandContext(fake, interaction.Public, interaction.Direct, data)); err != nil {
		panic(err)
	}
	return fake
}

func TestPickRepliesOncePubliclyWithoutDeferring(t *testing.T) {
	calls := pickFrom("a|b").Calls()
	if len(calls) != 1 || calls[0].Op != "Reply" || calls[0].Visibility != interaction.Public {
		t.Fatalf("calls = %+v, want exactly one public Reply", calls)
	}
	if got := calls[0].Response.Content; got != "**Picked:** a" && got != "**Picked:** b" {
		t.Errorf("content = %q", got)
	}
}

func TestPickRefusesTooFewItemsPrivately(t *testing.T) {
	for _, items := range []string{"", "   ", "only"} {
		calls := pickFrom(items).Calls()
		if len(calls) != 1 || calls[0].Op != "Reply" || calls[0].Visibility != interaction.Private {
			t.Fatalf("%q: calls = %+v, want exactly one private Reply", items, calls)
		}
		if !strings.Contains(calls[0].Response.Content, "2 items") {
			t.Errorf("%q: refusal = %q", items, calls[0].Response.Content)
		}
	}
}

func TestPickSplitsALongResultIntoPublicFollowUps(t *testing.T) {
	long := strings.Repeat("x", discord.MaxContentLength+10)
	fake := pickFrom(long + "|" + long)
	calls := fake.Calls()
	if len(calls) != 2 || calls[0].Op != "Reply" || calls[1].Op != "FollowUp" {
		t.Fatalf("ops = %v, want Reply,FollowUp", fake.Ops())
	}
	if calls[0].Visibility != interaction.Public || calls[1].Visibility != interaction.Public {
		t.Errorf("visibilities = %v, %v", calls[0].Visibility, calls[1].Visibility)
	}
	if len(calls[0].Response.Content) != discord.MaxContentLength {
		t.Errorf("first chunk = %d bytes", len(calls[0].Response.Content))
	}
}

func TestPickIsRegisteredAsAPublicDirectCommand(t *testing.T) {
	for _, c := range commands.AllCommands() {
		if c.Name == "pick" {
			if c.Run == nil || c.Handler != nil || c.Visibility != commands.Public || c.Response != commands.Direct {
				t.Errorf("pick registration = %+v", c)
			}
			return
		}
	}
	t.Fatal("pick is not registered")
}
