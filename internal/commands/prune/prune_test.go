package prune

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/discord"
	"ReZeroTsu/internal/interaction"
	"ReZeroTsu/internal/interaction/interactiontest"

	"github.com/bwmarrin/discordgo"
)

const here = "chan-here"

func history(n int, age time.Duration) []*discordgo.Message {
	out := make([]*discordgo.Message, n)
	for i := range out {
		out[i] = &discordgo.Message{ID: fmt.Sprintf("m%04d", n-i), Timestamp: time.Now().Add(-age)}
	}
	return out
}

func restErr(status, code int) error {
	return &discordgo.RESTError{
		Response: &http.Response{StatusCode: status},
		Message:  &discordgo.APIErrorMessage{Code: code, Message: "raw discord text"},
	}
}

// invoke runs /prune the way the dispatcher does for a private deferred command: acknowledge, then Run.
func invoke(t *testing.T, fake *interactiontest.Fake, opts ...*discordgo.ApplicationCommandInteractionDataOption) error {
	t.Helper()
	i := &discordgo.Interaction{
		Type: discordgo.InteractionApplicationCommand, GuildID: "g", ChannelID: here,
		Member: &discordgo.Member{User: &discordgo.User{ID: "mod"}},
		Data:   discordgo.ApplicationCommandInteractionData{Name: "prune", Options: opts},
	}
	ic := interaction.NewContext(fake, i, nil, interaction.Private, interaction.Deferred)
	if err := ic.Defer(0); err != nil {
		t.Fatal(err)
	}
	return handlePrune(ic)
}

func number(n int64) *discordgo.ApplicationCommandInteractionDataOption {
	return &discordgo.ApplicationCommandInteractionDataOption{Name: "number", Type: discordgo.ApplicationCommandOptionInteger, Value: float64(n)}
}

func channel(id string) *discordgo.ApplicationCommandInteractionDataOption {
	return &discordgo.ApplicationCommandInteractionDataOption{Name: "channel", Type: discordgo.ApplicationCommandOptionChannel, Value: id}
}

// finalText is the text the invoker ends up seeing: the last reply edit.
func finalText(t *testing.T, fake *interactiontest.Fake) string {
	t.Helper()
	calls := fake.Calls()
	for i := len(calls) - 1; i >= 0; i-- {
		if calls[i].Op == "EditReply" {
			return calls[i].Response.Content
		}
	}
	t.Fatalf("no EditReply in %v", fake.Ops())
	return ""
}

// assertPrivateOnly checks nothing public happened: the only acknowledgement is a private defer, nothing was
// posted, and the reply is never deleted.
func assertPrivateOnly(t *testing.T, fake *interactiontest.Fake) {
	t.Helper()
	for i, c := range fake.Calls() {
		switch c.Op {
		case "Defer":
			if i != 0 || c.Visibility != interaction.Private {
				t.Fatalf("call %d: Defer visibility %v", i, c.Visibility)
			}
		case "EditReply", "ListMessages", "DeleteMessage", "DeleteMessages":
		default:
			t.Fatalf("unexpected %s call; ops = %v", c.Op, fake.Ops())
		}
	}
}

func TestPruneIsDeclaredPrivateAndDeferred(t *testing.T) {
	for _, c := range commands.AllCommands() {
		if c.Name != "prune" {
			continue
		}
		if c.Visibility != commands.Private || c.Response != commands.Deferred || c.Run == nil || c.Handler != nil {
			t.Fatalf("prune: visibility %v, response %v, run %v, handler %v", c.Visibility, c.Response, c.Run != nil, c.Handler != nil)
		}
		return
	}
	t.Fatal("prune is not registered")
}

func TestPruneConfirmsPrivatelyAndNeverDeletesItsReply(t *testing.T) {
	fake := &interactiontest.Fake{History: map[string][]*discordgo.Message{here: history(10, time.Hour)}}
	start := time.Now()
	if err := invoke(t, fake, number(4)); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("prune waited before finishing; the confirmation is no longer deleted on a timer")
	}
	assertPrivateOnly(t, fake)
	if got := finalText(t, fake); !strings.Contains(got, "Removed 4 valid messages") {
		t.Fatalf("confirmation = %q", got)
	}
	if strings.Contains(finalText(t, fake), "Deleting this message") {
		t.Fatal("confirmation still promises to delete itself")
	}
	if left := len(fake.History[here]); left != 6 {
		t.Fatalf("%d messages left, want 6", left)
	}
	// The newest messages were the ones removed.
	if fake.History[here][0].ID != "m0006" {
		t.Fatalf("newest left = %s, want m0006", fake.History[here][0].ID)
	}
}

func TestPruneNamesTheChannelAsAMention(t *testing.T) {
	fake := &interactiontest.Fake{History: map[string][]*discordgo.Message{"other": history(3, time.Hour)}}
	if err := invoke(t, fake, number(2), channel("other")); err != nil {
		t.Fatal(err)
	}
	if got := finalText(t, fake); !strings.Contains(got, "<#other>") {
		t.Fatalf("confirmation = %q, want the channel as a mention", got)
	}
	if len(fake.History["other"]) != 1 {
		t.Fatalf("%d left in the chosen channel", len(fake.History["other"]))
	}
}

func TestPruneSingleMessageUsesPlainDelete(t *testing.T) {
	fake := &interactiontest.Fake{History: map[string][]*discordgo.Message{here: history(3, time.Hour)}}
	if err := invoke(t, fake, number(1)); err != nil {
		t.Fatal(err)
	}
	assertPrivateOnly(t, fake)
	var deletes []string
	for _, op := range fake.Ops() {
		if strings.HasPrefix(op, "Delete") {
			deletes = append(deletes, op)
		}
	}
	if len(deletes) != 1 || deletes[0] != "DeleteMessage" {
		t.Fatalf("deletes = %v", deletes)
	}
}

func TestPruneLongRunTellsThePrunerItIsWorking(t *testing.T) {
	fake := &interactiontest.Fake{History: map[string][]*discordgo.Message{here: history(250, time.Hour)}}
	if err := invoke(t, fake, number(150)); err != nil {
		t.Fatal(err)
	}
	assertPrivateOnly(t, fake)
	var edits []string
	for _, c := range fake.Calls() {
		if c.Op == "EditReply" {
			edits = append(edits, c.Response.Content)
		}
	}
	if len(edits) != 2 || !strings.Contains(edits[0], "might take a while") || !strings.Contains(edits[1], "Removed 150 valid messages") {
		t.Fatalf("edits = %q", edits)
	}
	if len(fake.History[here]) != 100 {
		t.Fatalf("%d left, want 100", len(fake.History[here]))
	}
	for _, c := range fake.Calls() {
		if c.Op == "DeleteMessages" && len(c.MessageIDs) > 100 {
			t.Fatalf("bulk delete of %d", len(c.MessageIDs))
		}
	}
}

func TestPruneSkipsMessagesOlderThanFourteenDays(t *testing.T) {
	msgs := append(history(2, time.Hour), history(5, 15*24*time.Hour)...)
	for i, m := range msgs {
		m.ID = fmt.Sprintf("x%d", 100-i)
	}
	fake := &interactiontest.Fake{History: map[string][]*discordgo.Message{here: msgs}}
	if err := invoke(t, fake, number(5)); err != nil {
		t.Fatal(err)
	}
	if got := finalText(t, fake); !strings.Contains(got, "Removed 2 valid messages") {
		t.Fatalf("confirmation = %q", got)
	}
	if len(fake.History[here]) != 5 {
		t.Fatalf("%d left, want the 5 old ones", len(fake.History[here]))
	}
}

func TestPruneRefusalsAreSentencesShownPrivately(t *testing.T) {
	tests := []struct {
		name    string
		opts    []*discordgo.ApplicationCommandInteractionDataOption
		history []*discordgo.Message
		want    string
	}{
		{"no number", nil, nil, "Please provide a number"},
		{"zero", []*discordgo.ApplicationCommandInteractionDataOption{number(0)}, nil, "Minimum is 1"},
		{"too many", []*discordgo.ApplicationCommandInteractionDataOption{number(5001)}, nil, "Maximum is 5000"},
		{"empty channel", []*discordgo.ApplicationCommandInteractionDataOption{number(3)}, nil, "No valid messages could be found"},
		{"all too old", []*discordgo.ApplicationCommandInteractionDataOption{number(3)}, history(3, 20*24*time.Hour), "more than 14 days old"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &interactiontest.Fake{History: map[string][]*discordgo.Message{here: tt.history}}
			if err := invoke(t, fake, tt.opts...); err != nil {
				t.Fatal(err)
			}
			assertPrivateOnly(t, fake)
			if got := finalText(t, fake); !strings.Contains(got, tt.want) {
				t.Fatalf("text = %q, want it to contain %q", got, tt.want)
			}
			for _, op := range fake.Ops() {
				if strings.HasPrefix(op, "Delete") {
					t.Fatalf("deleted messages despite the refusal: %v", fake.Ops())
				}
			}
		})
	}
}

func TestPrunePermissionFailureNamesTheChannelAndThePermissions(t *testing.T) {
	for _, failing := range []string{"ListMessages", "DeleteMessages", "DeleteMessage"} {
		t.Run(failing, func(t *testing.T) {
			fake := &interactiontest.Fake{
				History: map[string][]*discordgo.Message{"other": history(5, time.Hour)},
				Fail:    map[string]error{failing: restErr(403, discord.MissingPermissionsCode)},
			}
			amount := int64(3)
			if failing == "DeleteMessage" {
				amount = 1
			}
			if err := invoke(t, fake, number(amount), channel("other")); err != nil {
				t.Fatal(err)
			}
			assertPrivateOnly(t, fake)
			want := "I can't do that in <#other>. I need View Channel, Read Message History and Manage Messages there."
			if got := finalText(t, fake); got != want {
				t.Fatalf("text = %q, want %q", got, want)
			}
		})
	}
}

func TestPruneMissingAccessAndUnexpectedErrors(t *testing.T) {
	fake := &interactiontest.Fake{Fail: map[string]error{"ListMessages": restErr(403, discord.MissingAccessCode)}}
	if err := invoke(t, fake, number(3), channel("other")); err != nil {
		t.Fatal(err)
	}
	if got := finalText(t, fake); got != "I can't see <#other>. Give me View Channel there and try again." {
		t.Fatalf("text = %q", got)
	}

	fake = &interactiontest.Fake{Fail: map[string]error{"ListMessages": restErr(500, 0)}}
	if err := invoke(t, fake, number(3)); err != nil {
		t.Fatal(err)
	}
	if got := finalText(t, fake); got != "Something went wrong." {
		t.Fatalf("text = %q", got)
	}
}
