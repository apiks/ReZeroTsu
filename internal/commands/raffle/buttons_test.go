package raffle

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/database/guilds"
	"ReZeroTsu/internal/interaction"
	"ReZeroTsu/internal/interaction/interactiontest"

	"github.com/bwmarrin/discordgo"
)

// fakeStore is an in-memory raffle store with the single-statement entrant update of the real one.
type fakeStore struct {
	raffles   []*guilds.Raffle
	failSet   error
	failList  error
	updateLog []string
}

func (f *fakeStore) GetRaffles(_ context.Context, _ string) ([]*guilds.Raffle, error) {
	if f.failList != nil {
		return nil, f.failList
	}
	out := make([]*guilds.Raffle, len(f.raffles))
	for i, r := range f.raffles {
		c := *r
		c.ParticipantIDs = slices.Clone(r.ParticipantIDs)
		out[i] = &c
	}
	return out, nil
}

func (f *fakeStore) SetRaffle(_ context.Context, _ string, r *guilds.Raffle) error {
	if f.failSet != nil {
		return f.failSet
	}
	c := *r
	for i, old := range f.raffles {
		if old.Name == r.Name {
			f.raffles[i] = &c
			return nil
		}
	}
	f.raffles = append(f.raffles, &c)
	return nil
}

func (f *fakeStore) UpdateRaffleParticipant(_ context.Context, _, name, userID string, remove bool) error {
	f.updateLog = append(f.updateLog, name+"/"+userID)
	for _, r := range f.raffles {
		if r.Name != name {
			continue
		}
		if remove {
			r.ParticipantIDs = slices.DeleteFunc(r.ParticipantIDs, func(id string) bool { return id == userID })
		} else if !slices.Contains(r.ParticipantIDs, userID) {
			r.ParticipantIDs = append(r.ParticipantIDs, userID)
		}
	}
	return nil
}

func useStore(t *testing.T, st *fakeStore) {
	t.Helper()
	old := storeOf
	storeOf = func(*database.Client) raffleStore { return st }
	t.Cleanup(func() { storeOf = old })
}

func raffleCommand(t *testing.T) *commands.Command {
	t.Helper()
	for _, c := range commands.AllCommands() {
		if c.Name == "raffle" {
			return c
		}
	}
	t.Fatal("/raffle is not registered")
	return nil
}

func subcommand(t *testing.T, name string) *commands.Subcommand {
	t.Helper()
	for _, s := range raffleCommand(t).Subcommands {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("/raffle has no %q subcommand", name)
	return nil
}

// invokeCreate runs /raffle create the way dispatch does for a private deferred command.
func invokeCreate(t *testing.T, name string) *interactiontest.Fake {
	t.Helper()
	fake := &interactiontest.Fake{}
	cmd := raffleCommand(t)
	opts := []*discordgo.ApplicationCommandInteractionDataOption{{Name: "raffle", Type: discordgo.ApplicationCommandOptionString, Value: name}}
	ic := interactiontest.NewCommandContext(fake, cmd.Visibility, cmd.Response, discordgo.ApplicationCommandInteractionData{Name: "raffle", Options: opts})
	if err := ic.Defer(0); err != nil {
		t.Fatal(err)
	}
	if err := subcommand(t, "create").Run(ic); err != nil {
		t.Fatal(err)
	}
	return fake
}

func lastCall(t *testing.T, fake *interactiontest.Fake, op string) interactiontest.Call {
	t.Helper()
	calls := fake.Calls()
	for i := len(calls) - 1; i >= 0; i-- {
		if calls[i].Op == op {
			return calls[i]
		}
	}
	t.Fatalf("no %s call in %v", op, fake.Ops())
	return interactiontest.Call{}
}

func text(r interaction.Response) string {
	var b strings.Builder
	b.WriteString(r.Content)
	for _, e := range r.Embeds {
		b.WriteString(e.Title + " " + e.Description)
	}
	return b.String()
}

func buttonIDs(r interaction.Response) []string {
	var ids []string
	for _, row := range r.Components {
		for _, c := range row.(discordgo.ActionsRow).Components {
			ids = append(ids, c.(discordgo.Button).CustomID)
		}
	}
	return ids
}

func TestRaffleCommandIsAModGuildCommandAndTheThreeOldCommandsAreGone(t *testing.T) {
	cmd := raffleCommand(t)
	if cmd.Permission != commands.PermMod || cmd.Context != commands.ContextGuildOnly || cmd.Visibility != commands.Private || cmd.Response != commands.Deferred {
		t.Errorf("permission/context/visibility/response = %v/%v/%v/%v", cmd.Permission, cmd.Context, cmd.Visibility, cmd.Response)
	}
	create := subcommand(t, "create")
	if len(create.Options) != 1 || create.Options[0].Name != "raffle" || !create.Options[0].Required {
		t.Errorf("create options = %+v", create.Options)
	}
	for _, d := range commands.Definitions() {
		switch d.Name {
		case "create-raffle", "join-raffle", "leave-raffle":
			t.Errorf("old command %q is still registered", d.Name)
		}
	}
}

func TestCreatePostsTheRaffleMessageAndRepliesPrivatelyWithALink(t *testing.T) {
	st := &fakeStore{}
	useStore(t, st)
	fake := invokeCreate(t, "  Grand Raffle ")

	post := lastCall(t, fake, "PostMessage")
	if post.ChannelID != "c" {
		t.Errorf("posted in %q, want the current channel", post.ChannelID)
	}
	body := text(post.Response)
	for _, want := range []string{"Grand Raffle", "0", "No winner yet"} {
		if !strings.Contains(body, want) {
			t.Errorf("raffle message %q lacks %q", body, want)
		}
	}
	if got := buttonIDs(post.Response); strings.Join(got, ",") != "raffle:enter,raffle:leave" {
		t.Errorf("buttons = %v", got)
	}
	if len(st.raffles) != 1 {
		t.Fatalf("stored %+v", st.raffles)
	}
	got := st.raffles[0]
	if got.Name != "Grand Raffle" || got.ChannelID != "c" || got.MessageID != "posted" || got.ReactMessageID != "" || got.WinnerID != "" {
		t.Errorf("stored raffle = %+v", got)
	}
	reply := lastCall(t, fake, "EditReply")
	if !strings.Contains(text(reply.Response), "https://discord.com/channels/g/c/posted") {
		t.Errorf("reply %q has no link to the raffle message", text(reply.Response))
	}
	if first := fake.Calls()[0]; first.Op != "Defer" || first.Visibility != interaction.Private {
		t.Errorf("first call = %+v, want a private Defer", first)
	}
}

func TestCreateRefusesANameThatAlreadyExistsWithoutPosting(t *testing.T) {
	st := &fakeStore{raffles: []*guilds.Raffle{{Name: "Grand Raffle"}}}
	useStore(t, st)
	fake := invokeCreate(t, "grand raffle")
	if slices.Contains(fake.Ops(), "PostMessage") || len(st.raffles) != 1 {
		t.Errorf("ops %v, stored %d raffles", fake.Ops(), len(st.raffles))
	}
	if got := text(lastCall(t, fake, "EditReply").Response); !strings.Contains(got, "already exists") {
		t.Errorf("reply %q", got)
	}
}

func TestCreateExplainsAMissingPermissionAndStoresNothing(t *testing.T) {
	st := &fakeStore{}
	useStore(t, st)
	fake := &interactiontest.Fake{Fail: map[string]error{"PostMessage": &discordgo.RESTError{Response: &http.Response{StatusCode: 403}}}}
	cmd := raffleCommand(t)
	opts := []*discordgo.ApplicationCommandInteractionDataOption{{Name: "raffle", Type: discordgo.ApplicationCommandOptionString, Value: "x"}}
	ic := interactiontest.NewCommandContext(fake, cmd.Visibility, cmd.Response, discordgo.ApplicationCommandInteractionData{Name: "raffle", Options: opts})
	_ = ic.Defer(0)
	if err := subcommand(t, "create").Run(ic); err != nil {
		t.Fatal(err)
	}
	got := text(lastCall(t, fake, "EditReply").Response)
	if !strings.Contains(got, "<#c>") || !strings.Contains(got, "Send Messages") || len(st.raffles) != 0 {
		t.Errorf("reply %q, stored %d", got, len(st.raffles))
	}
}

func TestCreateRemovesTheMessageItPostedWhenTheRaffleCannotBeSaved(t *testing.T) {
	st := &fakeStore{failSet: errors.New("db down")}
	useStore(t, st)
	fake := invokeCreate(t, "x")
	del := lastCall(t, fake, "DeleteMessage")
	if del.ChannelID != "c" || del.MessageID != "posted" {
		t.Errorf("deleted %+v", del)
	}
	if got := text(lastCall(t, fake, "EditReply").Response); got != interaction.SomethingWentWrong {
		t.Errorf("reply %q", got)
	}
}

// press runs a pressed Enter or Leave button on the message of the given raffle.
func press(t *testing.T, customID, userID, messageID string) (*interactiontest.Fake, error) {
	t.Helper()
	fake := &interactiontest.Fake{}
	i := &discordgo.Interaction{Type: discordgo.InteractionMessageComponent, GuildID: "g", ChannelID: "c",
		Member:  &discordgo.Member{User: &discordgo.User{ID: userID}},
		Message: &discordgo.Message{ID: messageID, ChannelID: "c"},
		Data:    discordgo.MessageComponentInteractionData{CustomID: customID, ComponentType: discordgo.ButtonComponent}}
	ic := interaction.NewContext(fake, i, nil, interaction.Private, interaction.Direct)
	return fake, handleButton(ic, nil)
}

func buttonRaffle(entrants ...string) *guilds.Raffle {
	return &guilds.Raffle{Name: "Grand Raffle", ParticipantIDs: entrants, ChannelID: "c", MessageID: "m1"}
}

func TestButtonsReplyPrivatelyForEveryState(t *testing.T) {
	cases := []struct {
		name     string
		button   string
		entrants []string
		want     string
		after    []string
	}{
		{"enter", "raffle:enter", nil, "You entered Grand Raffle", []string{"user"}},
		{"enter again", "raffle:enter", []string{"user"}, "You're already entered in Grand Raffle", []string{"user"}},
		{"leave", "raffle:leave", []string{"user", "other"}, "You left Grand Raffle", []string{"other"}},
		{"leave without entering", "raffle:leave", []string{"other"}, "You're not entered in Grand Raffle", []string{"other"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := &fakeStore{raffles: []*guilds.Raffle{buttonRaffle(tc.entrants...)}}
			useStore(t, st)
			fake, err := press(t, tc.button, "user", "m1")
			if err != nil {
				t.Fatal(err)
			}
			reply := lastCall(t, fake, "Reply")
			if reply.Visibility != interaction.Private || reply.Response.Content != tc.want {
				t.Errorf("reply %q (visibility %v), want private %q", reply.Response.Content, reply.Visibility, tc.want)
			}
			if !slices.Equal(st.raffles[0].ParticipantIDs, tc.after) {
				t.Errorf("entrants = %v, want %v", st.raffles[0].ParticipantIDs, tc.after)
			}
		})
	}
}

func TestAChangeEditsTheRaffleMessageWithTheNewCount(t *testing.T) {
	st := &fakeStore{raffles: []*guilds.Raffle{buttonRaffle("other")}}
	useStore(t, st)
	fake, err := press(t, "raffle:enter", "user", "m1")
	if err != nil {
		t.Fatal(err)
	}
	edit := lastCall(t, fake, "EditMessage")
	if edit.ChannelID != "c" || edit.MessageID != "m1" || !strings.Contains(text(edit.Response), "2") {
		t.Errorf("edit = %+v", edit)
	}
	if got := buttonIDs(edit.Response); strings.Join(got, ",") != "raffle:enter,raffle:leave" {
		t.Errorf("buttons = %v", got)
	}
}

func TestNoChangeLeavesTheRaffleMessageAlone(t *testing.T) {
	st := &fakeStore{raffles: []*guilds.Raffle{buttonRaffle("user")}}
	useStore(t, st)
	fake, _ := press(t, "raffle:enter", "user", "m1")
	if slices.Contains(fake.Ops(), "EditMessage") || len(st.updateLog) != 0 {
		t.Errorf("ops %v, updates %v", fake.Ops(), st.updateLog)
	}
}

func TestAFailedEditDoesNotFailTheConfirmation(t *testing.T) {
	st := &fakeStore{raffles: []*guilds.Raffle{buttonRaffle()}}
	useStore(t, st)
	fake := &interactiontest.Fake{Fail: map[string]error{"EditMessage": errors.New("rate limited")}}
	i := &discordgo.Interaction{Type: discordgo.InteractionMessageComponent, GuildID: "g", ChannelID: "c",
		Member:  &discordgo.Member{User: &discordgo.User{ID: "user"}},
		Message: &discordgo.Message{ID: "m1", ChannelID: "c"},
		Data:    discordgo.MessageComponentInteractionData{CustomID: "raffle:enter"}}
	ic := interaction.NewContext(fake, i, nil, interaction.Private, interaction.Direct)
	if err := handleButton(ic, nil); err != nil {
		t.Fatal(err)
	}
	if got := lastCall(t, fake, "Reply").Response.Content; got != "You entered Grand Raffle" {
		t.Errorf("reply %q", got)
	}
	if len(st.raffles[0].ParticipantIDs) != 1 {
		t.Error("the entry must stay saved")
	}
}

func TestAButtonOnAMessageNoRaffleOwnsIsRefusedPrivately(t *testing.T) {
	st := &fakeStore{raffles: []*guilds.Raffle{buttonRaffle()}}
	useStore(t, st)
	fake, err := press(t, "raffle:enter", "user", "gone")
	if err != nil {
		t.Fatal(err)
	}
	reply := lastCall(t, fake, "Reply")
	if reply.Visibility != interaction.Private || !strings.Contains(reply.Response.Content, "no longer") || len(st.raffles[0].ParticipantIDs) != 0 {
		t.Errorf("reply %+v, entrants %v", reply, st.raffles[0].ParticipantIDs)
	}
}

func TestOnlyThePresserIsEnteredOrRemoved(t *testing.T) {
	st := &fakeStore{raffles: []*guilds.Raffle{buttonRaffle("a", "b")}}
	useStore(t, st)
	if _, err := press(t, "raffle:leave", "b", "m1"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(st.raffles[0].ParticipantIDs, []string{"a"}) {
		t.Errorf("entrants = %v", st.raffles[0].ParticipantIDs)
	}
}

func TestAStoreFailureOnAButtonIsReportedPrivately(t *testing.T) {
	st := &fakeStore{failList: errors.New("db down")}
	useStore(t, st)
	fake, err := press(t, "raffle:enter", "user", "m1")
	if err != nil {
		t.Fatal(err)
	}
	if got := lastCall(t, fake, "Reply"); got.Visibility != interaction.Private || got.Response.Content != interaction.SomethingWentWrong {
		t.Errorf("reply %+v", got)
	}
}
