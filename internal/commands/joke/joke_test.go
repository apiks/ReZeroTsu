package joke

import (
	"context"
	"errors"
	"testing"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/interaction"
	"ReZeroTsu/internal/interaction/interactiontest"

	"github.com/bwmarrin/discordgo"
)

func jokeWith(fetch func(context.Context) (string, error)) []interactiontest.Call {
	fake := &interactiontest.Fake{}
	ic := interactiontest.NewCommandContext(fake, interaction.Public, interaction.Direct, discordgo.ApplicationCommandInteractionData{Name: "joke"})
	if err := runJoke(ic, fetch); err != nil {
		panic(err)
	}
	return fake.Calls()
}

func TestJokeRepliesOncePubliclyWithoutDeferring(t *testing.T) {
	calls := jokeWith(func(context.Context) (string, error) { return "knock knock", nil })
	if len(calls) != 1 || calls[0].Op != "Reply" || calls[0].Visibility != interaction.Public {
		t.Fatalf("calls = %+v, want exactly one public Reply", calls)
	}
	if calls[0].Response.Content != "knock knock" {
		t.Errorf("content = %q", calls[0].Response.Content)
	}
}

// The fetch runs under the interaction's context, so shutdown ends it.
func TestJokeFetchRunsUnderTheInteractionContext(t *testing.T) {
	fake := &interactiontest.Fake{}
	ic := interactiontest.NewCommandContext(fake, interaction.Public, interaction.Direct, discordgo.ApplicationCommandInteractionData{Name: "joke"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ic.Ctx = ctx
	var fetchErr error
	if err := runJoke(ic, func(c context.Context) (string, error) { fetchErr = c.Err(); return "", c.Err() }); err != nil {
		t.Fatal(err)
	}
	if fetchErr == nil {
		t.Error("the fetch context did not derive from the interaction context")
	}
}

func TestJokeFailuresAreRefusedPrivately(t *testing.T) {
	tests := map[string]func(context.Context) (string, error){
		"fetch error": func(context.Context) (string, error) { return "", errors.New("down") },
		"no joke":     func(context.Context) (string, error) { return "", nil },
	}
	for name, fetch := range tests {
		t.Run(name, func(t *testing.T) {
			calls := jokeWith(fetch)
			if len(calls) != 1 || calls[0].Op != "Reply" || calls[0].Visibility != interaction.Private {
				t.Fatalf("calls = %+v, want exactly one private Reply", calls)
			}
		})
	}
}

func TestJokeIsRegisteredAsAPublicDirectCommand(t *testing.T) {
	for _, c := range commands.AllCommands() {
		if c.Name == "joke" {
			if c.Run == nil || c.Handler != nil || c.Visibility != commands.Public || c.Response != commands.Direct {
				t.Errorf("joke registration = %+v", c)
			}
			return
		}
	}
	t.Fatal("joke is not registered")
}
