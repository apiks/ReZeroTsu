package roll

import (
	"strings"
	"testing"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/interaction"
	"ReZeroTsu/internal/interaction/interactiontest"

	"github.com/bwmarrin/discordgo"
)

func rollWith(number *float64) *interactiontest.Fake {
	data := discordgo.ApplicationCommandInteractionData{Name: "roll"}
	if number != nil {
		data.Options = []*discordgo.ApplicationCommandInteractionDataOption{
			{Name: "number", Type: discordgo.ApplicationCommandOptionInteger, Value: *number},
		}
	}
	fake := &interactiontest.Fake{}
	ic := interactiontest.NewCommandContext(fake, interaction.Public, interaction.Direct, data)
	if err := run(ic); err != nil {
		panic(err)
	}
	return fake
}

func TestRollRepliesOncePubliclyWithoutDeferring(t *testing.T) {
	one := 1.0
	fake := rollWith(&one)
	calls := fake.Calls()
	if len(calls) != 1 || calls[0].Op != "Reply" || calls[0].Visibility != interaction.Public {
		t.Fatalf("calls = %+v, want exactly one public Reply", calls)
	}
	if calls[0].Response.Content != "**Rolled:** 1" {
		t.Errorf("content = %q", calls[0].Response.Content)
	}
}

func TestRollDefaultsToOneHundred(t *testing.T) {
	fake := rollWith(nil)
	if got := fake.Calls()[0].Response.Content; !strings.HasPrefix(got, "**Rolled:** ") {
		t.Errorf("content = %q", got)
	}
}

func TestRollRefusesABadNumberPrivately(t *testing.T) {
	zero := 0.0
	fake := rollWith(&zero)
	calls := fake.Calls()
	if len(calls) != 1 || calls[0].Op != "Reply" || calls[0].Visibility != interaction.Private {
		t.Fatalf("calls = %+v, want exactly one private Reply", calls)
	}
	if !strings.Contains(calls[0].Response.Content, "positive number") {
		t.Errorf("refusal = %q", calls[0].Response.Content)
	}
}

func TestRollIsRegisteredAsAPublicDirectCommand(t *testing.T) {
	for _, c := range commands.AllCommands() {
		if c.Name == "roll" {
			if c.Run == nil || c.Visibility != commands.Public || c.Response != commands.Direct {
				t.Errorf("roll registration = %+v", c)
			}
			return
		}
	}
	t.Fatal("roll is not registered")
}
