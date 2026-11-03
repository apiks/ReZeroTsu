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

// register adds cmd for one test and removes it afterwards.
func register(t *testing.T, cmd *Command) {
	t.Helper()
	Add(cmd)
	t.Cleanup(func() { delete(registry, cmd.Name) })
}

func slashInteraction(name, guildID string, perms int64, roles ...string) *discordgo.InteractionCreate {
	i := &discordgo.Interaction{
		Type:      discordgo.InteractionApplicationCommand,
		ChannelID: "chan",
		GuildID:   guildID,
		Data:      discordgo.ApplicationCommandInteractionData{Name: name},
	}
	user := &discordgo.User{ID: "user"}
	if guildID != "" {
		i.Member = &discordgo.Member{User: user, Permissions: perms, Roles: roles}
	} else {
		i.User = user
	}
	return &discordgo.InteractionCreate{Interaction: i}
}

func run(t *testing.T, name string, fake *interactiontest.Fake, i *discordgo.InteractionCreate) error {
	t.Helper()
	return dispatch(context.Background(), (*database.Client)(nil), name, fake, i)
}

func TestDispatchRefusalsArePrivateAndBeforeAcknowledging(t *testing.T) {
	handlerCalled := false
	guarded := func(name string, c AllowedContext) *Command {
		return &Command{Name: name, Context: c, Visibility: Public, Response: Deferred,
			Run: func(*interaction.Context) error {
				handlerCalled = true
				return nil
			}}
	}
	register(t, guarded("t-guildonly", ContextGuildOnly))
	register(t, guarded("t-dmonly", ContextDMOnly))

	tests := []struct {
		name string
		cmd  string
		i    *discordgo.InteractionCreate
		want string
	}{
		{"server-only command in a DM", "t-guildonly", slashInteraction("t-guildonly", "", 0), "only be used in a server"},
		{"DM-only command in a server", "t-dmonly", slashInteraction("t-dmonly", "g", 0), "only be used in DMs"},
		{"unknown command", "t-missing", slashInteraction("t-missing", "g", 0), "Unknown command"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &interactiontest.Fake{}
			if err := run(t, tt.cmd, fake, tt.i); err != nil {
				t.Fatalf("dispatch: %v", err)
			}
			calls := fake.Calls()
			replies := 0
			for _, c := range calls {
				if c.Op == "Defer" {
					t.Fatalf("calls = %v, refusal came after acknowledging", fake.Ops())
				}
				if c.Op == "Reply" {
					replies++
				}
			}
			last := calls[len(calls)-1]
			if replies != 1 || last.Op != "Reply" || last.Visibility != interaction.Private {
				t.Fatalf("calls = %+v, want exactly one private Reply as the last call", calls)
			}
			text := last.Response.Content
			for _, e := range last.Response.Embeds {
				text += e.Description
			}
			if !strings.Contains(text, tt.want) {
				t.Errorf("refusal %q does not contain %q", text, tt.want)
			}
			if handlerCalled {
				t.Error("handler ran after a refusal")
			}
		})
	}
}

func TestDispatchNewFormAcknowledgesByDeclaredMode(t *testing.T) {
	register(t, &Command{Name: "t-private-deferred", Visibility: Private, Response: Deferred,
		Run: func(ic *interaction.Context) error { return ic.EditReply(interaction.Response{Content: "done"}) }})
	register(t, &Command{Name: "t-public-direct", Visibility: Public, Response: Direct,
		Run: func(ic *interaction.Context) error { return ic.Reply(interaction.Response{Content: "pong"}) }})
	register(t, &Command{Name: "t-override", Visibility: Public, Response: Direct,
		Run: func(ic *interaction.Context) error {
			return ic.Reply(interaction.Response{Content: "secret", Visibility: interaction.Private})
		}})

	tests := []struct {
		cmd  string
		want []string
		vis  interaction.Visibility
	}{
		{"t-private-deferred", []string{"Defer", "EditReply"}, interaction.Private},
		{"t-public-direct", []string{"Reply"}, interaction.Public},
		{"t-override", []string{"Reply"}, interaction.Private},
	}
	for _, tt := range tests {
		t.Run(tt.cmd, func(t *testing.T) {
			fake := &interactiontest.Fake{}
			if err := run(t, tt.cmd, fake, slashInteraction(tt.cmd, "g", 0)); err != nil {
				t.Fatal(err)
			}
			if got := strings.Join(fake.Ops(), ","); got != strings.Join(tt.want, ",") {
				t.Fatalf("ops = %s, want %v", got, tt.want)
			}
			if got := fake.Calls()[0].Visibility; got != tt.vis {
				t.Errorf("visibility = %v, want %v", got, tt.vis)
			}
		})
	}
}

func TestDispatchLateFailureOnPublicDeferredCommandIsPrivate(t *testing.T) {
	register(t, &Command{Name: "t-panic", Visibility: Public, Response: Deferred,
		Run: func(*interaction.Context) error { panic("boom") }})
	register(t, &Command{Name: "t-err", Visibility: Public, Response: Deferred,
		Run: func(*interaction.Context) error { return errors.New("db down") }})

	for _, name := range []string{"t-panic", "t-err"} {
		t.Run(name, func(t *testing.T) {
			fake := &interactiontest.Fake{}
			if err := run(t, name, fake, slashInteraction(name, "g", 0)); err != nil {
				t.Fatal(err)
			}
			calls := fake.Calls()
			if got := strings.Join(fake.Ops(), ","); got != "Defer,DeleteReply,FollowUp" {
				t.Fatalf("ops = %s, want Defer,DeleteReply,FollowUp", got)
			}
			last := calls[2]
			if last.Visibility != interaction.Private || last.Response.Content != "Something went wrong." {
				t.Errorf("follow-up = %+v, want the private generic error", last)
			}
		})
	}
}

func TestDispatchFailureBeforeAnyReplyOnDirectCommandIsAPrivateReply(t *testing.T) {
	register(t, &Command{Name: "t-direct-panic", Visibility: Public, Response: Direct,
		Run: func(*interaction.Context) error { panic("boom") }})
	fake := &interactiontest.Fake{}
	if err := run(t, "t-direct-panic", fake, slashInteraction("t-direct-panic", "g", 0)); err != nil {
		t.Fatal(err)
	}
	calls := fake.Calls()
	if len(calls) != 1 || calls[0].Op != "Reply" || calls[0].Visibility != interaction.Private {
		t.Errorf("calls = %+v, want one private Reply", calls)
	}
}

func TestDispatchFailureOnPrivateDeferredCommandReplacesThePlaceholder(t *testing.T) {
	register(t, &Command{Name: "t-private-err", Visibility: Private, Response: Deferred,
		Run: func(*interaction.Context) error { return errors.New("x") }})
	fake := &interactiontest.Fake{}
	if err := run(t, "t-private-err", fake, slashInteraction("t-private-err", "g", 0)); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(fake.Ops(), ","); got != "Defer,EditReply" {
		t.Errorf("ops = %s, want Defer,EditReply", got)
	}
}

func TestAddRejectsUndeclaredOrInconsistentCommands(t *testing.T) {
	run1 := func(*interaction.Context) error { return nil }
	tests := []struct {
		name string
		cmd  *Command
	}{
		{"no visibility", &Command{Name: "x", Response: Deferred, Run: run1}},
		{"no response mode", &Command{Name: "x", Visibility: Public, Run: run1}},
		{"no Run", &Command{Name: "x", Visibility: Public, Response: Deferred}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Error("Add did not panic")
				}
				delete(registry, "x")
			}()
			Add(tt.cmd)
		})
	}
}

// A mod command is gated by Discord (default member permissions and server overrides) before the interaction
// reaches the bot, so dispatch runs it for any member it receives, with no bot-side check or lookup.
func TestDispatchDoesNotRefuseAModCommandForAMemberWithoutManageServer(t *testing.T) {
	var ran int
	register(t, &Command{Name: "t-mod", Permission: PermMod, Context: ContextGuildOnly, Visibility: Public, Response: Deferred,
		Run: func(*interaction.Context) error { ran++; return nil }})
	fake := &interactiontest.Fake{StallGuild: true}
	if err := run(t, "t-mod", fake, slashInteraction("t-mod", "g", 0, "some-role")); err != nil {
		t.Fatal(err)
	}
	if ran != 1 {
		t.Errorf("handler ran %d times, calls %v", ran, fake.Ops())
	}
	for _, c := range fake.Calls() {
		if c.Op == "Reply" || c.Op == "Guild" {
			t.Errorf("calls = %v, want only the acknowledgement", fake.Ops())
		}
	}
}
