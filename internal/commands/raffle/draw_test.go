package raffle

import (
	"context"
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

func (f *fakeStore) SetRaffleWinner(_ context.Context, _, name, winnerID string) error {
	for _, r := range f.raffles {
		if r.Name == name {
			r.WinnerID = winnerID
		}
	}
	return nil
}

func (f *fakeStore) DeleteRaffle(_ context.Context, _, name string) error {
	f.raffles = slices.DeleteFunc(f.raffles, func(r *guilds.Raffle) bool { return r.Name == name })
	return nil
}

func useDrawStore(t *testing.T, st *fakeStore) {
	t.Helper()
	useStore(t, st)
	old := drawStoreOf
	drawStoreOf = func(*database.Client) drawStore { return st }
	t.Cleanup(func() { drawStoreOf = old })
}

// invokeSub runs a /raffle subcommand the way dispatch does: acknowledged by the subcommand's own visibility.
func invokeSub(t *testing.T, fake *interactiontest.Fake, sub, name string) {
	t.Helper()
	cmd := raffleCommand(t)
	s := subcommand(t, sub)
	opts := []*discordgo.ApplicationCommandInteractionDataOption{{Name: "raffle", Type: discordgo.ApplicationCommandOptionString, Value: name}}
	vis := cmd.Visibility
	if s.Visibility != 0 {
		vis = s.Visibility
	}
	ic := interactiontest.NewCommandContext(fake, vis, commands.Deferred, discordgo.ApplicationCommandInteractionData{Name: "raffle", Options: opts})
	if err := ic.Defer(0); err != nil {
		t.Fatal(err)
	}
	if err := s.Run(ic); err != nil {
		t.Fatal(err)
	}
}

func TestDrawAnnouncesPubliclyStoresTheWinnerUpdatesTheMessageAndStaysOpen(t *testing.T) {
	st := &fakeStore{raffles: []*guilds.Raffle{{Name: "Grand Raffle", ParticipantIDs: []string{"only"}, ChannelID: "c", MessageID: "m1"}}}
	useDrawStore(t, st)
	fake := &interactiontest.Fake{}
	invokeSub(t, fake, "draw", "grand raffle")

	if first := fake.Calls()[0]; first.Op != "Defer" || first.Visibility != interaction.Public {
		t.Errorf("first call = %+v, want a public Defer", first)
	}
	if got := lastCall(t, fake, "EditReply").Response.Content; got != "**Grand Raffle** winner is <@only>! Congratulations!" {
		t.Errorf("announcement %q", got)
	}
	if len(st.raffles) != 1 || st.raffles[0].WinnerID != "only" {
		t.Errorf("stored %+v", st.raffles)
	}
	edit := lastCall(t, fake, "EditMessage")
	if edit.ChannelID != "c" || edit.MessageID != "m1" || !strings.Contains(text(edit.Response), "<@only>") {
		t.Errorf("edit = %+v", edit)
	}
	if got := buttonIDs(edit.Response); strings.Join(got, ",") != "raffle:enter,raffle:leave" {
		t.Errorf("buttons = %v", got)
	}

	fake2 := &interactiontest.Fake{}
	invokeSub(t, fake2, "draw", "Grand Raffle")
	if got := lastCall(t, fake2, "EditReply").Response.Content; !strings.Contains(got, "winner is <@only>") {
		t.Errorf("second draw %q", got)
	}
}

func TestDrawWithNoEntrantsRepliesPrivatelyAndPostsNothing(t *testing.T) {
	st := &fakeStore{raffles: []*guilds.Raffle{{Name: "Empty", ChannelID: "c", MessageID: "m1"}}}
	useDrawStore(t, st)
	fake := &interactiontest.Fake{}
	invokeSub(t, fake, "draw", "Empty")
	if slices.Contains(fake.Ops(), "EditMessage") || slices.Contains(fake.Ops(), "PostMessage") || st.raffles[0].WinnerID != "" {
		t.Errorf("ops %v, winner %q", fake.Ops(), st.raffles[0].WinnerID)
	}
	if !slices.Contains(fake.Ops(), "DeleteReply") {
		t.Errorf("the public placeholder must be deleted: %v", fake.Ops())
	}
	last := lastCall(t, fake, "FollowUp")
	if last.Visibility != interaction.Private || last.Response.Content != "There is nobody to draw in that raffle." {
		t.Errorf("follow-up %+v", last)
	}
}

func TestDrawAndRemoveRefuseAnUnknownRaffle(t *testing.T) {
	for _, sub := range []string{"draw", "remove"} {
		t.Run(sub, func(t *testing.T) {
			st := &fakeStore{}
			useDrawStore(t, st)
			fake := &interactiontest.Fake{}
			invokeSub(t, fake, sub, "nope")
			// Draw's public placeholder is replaced by a private follow-up; remove's private placeholder becomes the text.
			op := "FollowUp"
			if sub == "remove" {
				op = "EditReply"
			}
			if got := lastCall(t, fake, op); got.Response.Content != "No such raffle." || (op == "FollowUp" && got.Visibility != interaction.Private) {
				t.Errorf("reply %+v", got)
			}
		})
	}
}

func TestDrawAndRemoveWorkOnARaffleWithoutAButtonMessage(t *testing.T) {
	st := &fakeStore{raffles: []*guilds.Raffle{{Name: "Legacy", ParticipantIDs: []string{"a"}, ReactMessageID: "r1"}}}
	useDrawStore(t, st)
	fake := &interactiontest.Fake{}
	invokeSub(t, fake, "draw", "Legacy")
	if slices.Contains(fake.Ops(), "EditMessage") {
		t.Errorf("no message to edit: %v", fake.Ops())
	}
	if got := lastCall(t, fake, "EditReply").Response.Content; !strings.Contains(got, "winner is <@a>") {
		t.Errorf("announcement %q", got)
	}
	fake = &interactiontest.Fake{}
	invokeSub(t, fake, "remove", "Legacy")
	if slices.Contains(fake.Ops(), "EditMessage") || len(st.raffles) != 0 {
		t.Errorf("ops %v, stored %d", fake.Ops(), len(st.raffles))
	}
	if first := fake.Calls()[0]; first.Visibility != interaction.Private {
		t.Errorf("remove must be private: %+v", first)
	}
}

func TestRemoveClosesTheMessageKeepingTheWinnerAndDeletesTheRecord(t *testing.T) {
	st := &fakeStore{raffles: []*guilds.Raffle{{Name: "Grand Raffle", ParticipantIDs: []string{"a"}, WinnerID: "a", ChannelID: "c", MessageID: "m1"}}}
	useDrawStore(t, st)
	fake := &interactiontest.Fake{}
	invokeSub(t, fake, "remove", "Grand Raffle")

	edit := lastCall(t, fake, "EditMessage")
	body := text(edit.Response)
	if edit.MessageID != "m1" || !strings.Contains(body, "This raffle has ended") || !strings.Contains(body, "<@a>") {
		t.Errorf("edit = %+v", edit)
	}
	for _, row := range edit.Response.Components {
		for _, c := range row.(discordgo.ActionsRow).Components {
			if !c.(discordgo.Button).Disabled {
				t.Errorf("button %+v is not disabled", c)
			}
		}
	}
	if len(st.raffles) != 0 {
		t.Errorf("record not deleted: %+v", st.raffles)
	}
	if got := text(lastCall(t, fake, "EditReply").Response); !strings.Contains(got, "Grand Raffle") {
		t.Errorf("reply %q", got)
	}
}

func TestRemoveStillDeletesWhenTheRaffleMessageIsGone(t *testing.T) {
	st := &fakeStore{raffles: []*guilds.Raffle{{Name: "x", ChannelID: "c", MessageID: "m1"}}}
	useDrawStore(t, st)
	unknown := &discordgo.RESTError{Response: &http.Response{StatusCode: 404}, Message: &discordgo.APIErrorMessage{Code: discordgo.ErrCodeUnknownMessage}}
	fake := &interactiontest.Fake{Fail: map[string]error{"EditMessage": unknown}}
	invokeSub(t, fake, "remove", "x")
	if len(st.raffles) != 0 {
		t.Errorf("record kept: %+v", st.raffles)
	}
}

func TestRemoveKeepsTheRaffleAndExplainsWhenTheMessageCannotBeEdited(t *testing.T) {
	st := &fakeStore{raffles: []*guilds.Raffle{{Name: "x", ChannelID: "c", MessageID: "m1"}}}
	useDrawStore(t, st)
	fake := &interactiontest.Fake{Fail: map[string]error{"EditMessage": &discordgo.RESTError{Response: &http.Response{StatusCode: 403}}}}
	invokeSub(t, fake, "remove", "x")
	if len(st.raffles) != 1 {
		t.Errorf("record deleted: %+v", st.raffles)
	}
	if got := text(lastCall(t, fake, "EditReply").Response); !strings.Contains(got, "<#c>") {
		t.Errorf("reply %q", got)
	}
}

func TestDrawAndRemoveAreRegisteredWithAutocompleteAndTheOldCommandsAreGone(t *testing.T) {
	draw, remove := subcommand(t, "draw"), subcommand(t, "remove")
	if draw.Visibility != interaction.Public || remove.Visibility == interaction.Public {
		t.Errorf("visibility draw=%v remove=%v", draw.Visibility, remove.Visibility)
	}
	for _, s := range []*commands.Subcommand{draw, remove} {
		if len(s.Options) != 1 || s.Options[0].Name != "raffle" || !s.Options[0].Required || !s.Options[0].Autocomplete || s.AutocompleteOption == nil {
			t.Errorf("%s options = %+v", s.Name, s.Options)
		}
	}
	for _, d := range commands.Definitions() {
		if d.Name == "pick-raffle-winner" || d.Name == "remove-raffle" {
			t.Errorf("old command %q is still registered", d.Name)
		}
	}
}

func restError(code int, status int) error {
	return &discordgo.RESTError{Response: &http.Response{StatusCode: status}, Message: &discordgo.APIErrorMessage{Code: code}}
}

func TestRemoveStillDeletesWhenTheRaffleChannelWasDeleted(t *testing.T) {
	st := &fakeStore{raffles: []*guilds.Raffle{{Name: "x", ChannelID: "c", MessageID: "m1"}}}
	useDrawStore(t, st)
	fake := &interactiontest.Fake{Fail: map[string]error{"EditMessage": restError(discordgo.ErrCodeUnknownChannel, 404)}}
	invokeSub(t, fake, "remove", "x")
	if len(st.raffles) != 0 {
		t.Errorf("record kept: %+v", st.raffles)
	}
	if got := text(lastCall(t, fake, "EditReply").Response); strings.Contains(got, "channels/") {
		t.Errorf("reply %q links to a message that is gone", got)
	}
}

func TestRemoveReplyLinksTheClosedMessage(t *testing.T) {
	st := &fakeStore{raffles: []*guilds.Raffle{{Name: "x", ChannelID: "c9", MessageID: "m1"}}}
	useDrawStore(t, st)
	fake := &interactiontest.Fake{}
	invokeSub(t, fake, "remove", "x")
	if got := text(lastCall(t, fake, "EditReply").Response); !strings.Contains(got, "https://discord.com/channels/g/c9/m1") {
		t.Errorf("reply %q has no link", got)
	}
}

func TestADrawWhoseMessageEditFailsTellsTheModeratorPrivatelyAfterTheAnnouncement(t *testing.T) {
	st := &fakeStore{raffles: []*guilds.Raffle{{Name: "x", ParticipantIDs: []string{"only"}, ChannelID: "c9", MessageID: "m1"}}}
	useDrawStore(t, st)
	fake := &interactiontest.Fake{Fail: map[string]error{"EditMessage": &discordgo.RESTError{Response: &http.Response{StatusCode: 403}}}}
	invokeSub(t, fake, "draw", "x")
	ops := fake.Ops()
	if ops[len(ops)-2] != "EditReply" || ops[len(ops)-1] != "FollowUp" {
		t.Fatalf("ops %v, want the announcement then a follow-up", ops)
	}
	follow := lastCall(t, fake, "FollowUp")
	if follow.Visibility != interaction.Private || !strings.Contains(follow.Response.Content, "The winner is announced, but I couldn't update the raffle message") || !strings.Contains(follow.Response.Content, "<#c9>") {
		t.Errorf("follow-up = %+v", follow)
	}
}
