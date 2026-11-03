package raffle

import (
	"context"
	"errors"
	"fmt"
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

// manageStore adds the list and post store methods to the fake raffle store: deleting a raffle and pointing it at a
// new button message.
type manageStore struct {
	*fakeStore
	failDelete  error
	failMessage error
	deleted     []string
}

func (m *manageStore) DeleteRaffle(_ context.Context, _, name string) error {
	if m.failDelete != nil {
		return m.failDelete
	}
	m.deleted = append(m.deleted, name)
	m.raffles = slices.DeleteFunc(m.raffles, func(r *guilds.Raffle) bool { return r.Name == name })
	return nil
}

func (m *manageStore) SetRaffleMessage(_ context.Context, _, name, channelID, messageID string) error {
	if m.failMessage != nil {
		return m.failMessage
	}
	for _, r := range m.raffles {
		if r.Name == name {
			r.ChannelID, r.MessageID = channelID, messageID
		}
	}
	return nil
}

func useManage(t *testing.T, raffles ...*guilds.Raffle) *manageStore {
	t.Helper()
	st := &manageStore{fakeStore: &fakeStore{raffles: raffles}}
	useStore(t, st.fakeStore)
	oldList, oldPost := listStoreOf, postStoreOf
	listStoreOf = func(*database.Client) listStore { return st }
	postStoreOf = func(*database.Client) postStore { return st }
	t.Cleanup(func() { listStoreOf, postStoreOf = oldList, oldPost })
	return st
}

// invokeManage runs a /raffle subcommand the way dispatch does for a private deferred command.
func invokeManage(t *testing.T, name string, opts ...*discordgo.ApplicationCommandInteractionDataOption) *interactiontest.Fake {
	t.Helper()
	fake := &interactiontest.Fake{}
	runManage(t, fake, name, opts...)
	return fake
}

func runManage(t *testing.T, fake *interactiontest.Fake, name string, opts ...*discordgo.ApplicationCommandInteractionDataOption) {
	t.Helper()
	cmd := raffleCommand(t)
	ic := interactiontest.NewCommandContext(fake, cmd.Visibility, cmd.Response, discordgo.ApplicationCommandInteractionData{Name: "raffle", Options: opts})
	if err := ic.Defer(0); err != nil {
		t.Fatal(err)
	}
	if err := subcommand(t, name).Run(ic); err != nil {
		t.Fatal(err)
	}
}

func manyRaffles(n int) []*guilds.Raffle {
	out := make([]*guilds.Raffle, n)
	for i := range out {
		out[i] = &guilds.Raffle{Name: fmt.Sprintf("Raffle %02d", i)}
	}
	return out
}

func pickerOf(r interaction.Response) (discordgo.SelectMenu, bool) {
	for _, row := range r.Components {
		for _, c := range row.(discordgo.ActionsRow).Components {
			if m, ok := c.(discordgo.SelectMenu); ok {
				return m, true
			}
		}
	}
	return discordgo.SelectMenu{}, false
}

func hasPageButtons(r interaction.Response) bool {
	for _, row := range r.Components {
		for _, c := range row.(discordgo.ActionsRow).Components {
			if _, ok := c.(discordgo.Button); ok {
				return true
			}
		}
	}
	return false
}

func TestListRegistersAsAPrivateSubcommandWithNoOptions(t *testing.T) {
	list := subcommand(t, "list")
	if len(list.Options) != 0 {
		t.Errorf("list options = %+v", list.Options)
	}
	for _, d := range commands.Definitions() {
		if d.Name == "show-raffles" || d.Name == "my-raffles" {
			t.Errorf("old command %q is still registered", d.Name)
		}
	}
}

func TestListShowsEveryRaffleWithItsEntrantCountAndALinkPrivately(t *testing.T) {
	useManage(t,
		&guilds.Raffle{Name: "Grand Raffle", ParticipantIDs: []string{"a", "b"}, ChannelID: "c", MessageID: "m1"},
		&guilds.Raffle{Name: "Old Raffle", ParticipantIDs: []string{"a"}, ReactMessageID: "r1"},
	)
	fake := invokeManage(t, "list")
	calls := fake.Calls()
	if calls[0].Op != "Defer" || calls[0].Visibility != interaction.Private {
		t.Fatalf("first call = %+v, want a private Defer", calls[0])
	}
	r := lastCall(t, fake, "EditReply").Response
	body := fieldsText(r)
	for _, want := range []string{"Grand Raffle", "2 entrants", "https://discord.com/channels/g/c/m1", "Old Raffle", "1 entrant"} {
		if !strings.Contains(body, want) {
			t.Errorf("list %q lacks %q", body, want)
		}
	}
	picker, ok := pickerOf(r)
	if !ok || len(picker.Options) != 2 || picker.Options[0].Value != "Grand Raffle" {
		t.Errorf("picker = %+v", picker)
	}
	if hasPageButtons(r) {
		t.Error("two raffles fit on one page")
	}
}

func fieldsText(r interaction.Response) string {
	var b strings.Builder
	b.WriteString(text(r))
	for _, e := range r.Embeds {
		for _, f := range e.Fields {
			b.WriteString(" " + f.Name + " " + f.Value)
		}
	}
	return b.String()
}

func TestListWithNoRafflesSaysSoWithoutAPicker(t *testing.T) {
	useManage(t)
	r := lastCall(t, invokeManage(t, "list"), "EditReply").Response
	if !strings.Contains(text(r), "There are no raffles") || len(r.Components) != 0 {
		t.Errorf("reply = %+v", r)
	}
}

func TestListStoreFailureIsReportedPrivately(t *testing.T) {
	st := useManage(t)
	st.failList = errors.New("db down")
	r := lastCall(t, invokeManage(t, "list"), "EditReply").Response
	if r.Content != interaction.SomethingWentWrong {
		t.Errorf("reply = %+v", r)
	}
}

func TestListPagesCarryTheirOwnPickerOfAtMost25(t *testing.T) {
	useManage(t, manyRaffles(30)...)
	first := lastCall(t, invokeManage(t, "list"), "EditReply").Response
	picker, _ := pickerOf(first)
	if len(picker.Options) != 25 || picker.Options[0].Value != "Raffle 00" || !hasPageButtons(first) {
		t.Fatalf("first page picker has %d options, buttons %v", len(picker.Options), hasPageButtons(first))
	}
	second := RenderRafflePage(t.Context(), nil, nil, &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{GuildID: "g"}}, 1, 30, "user")
	if second.Refusal != "" {
		t.Fatal(second.Refusal)
	}
	got, _ := pickerOf(second.Response)
	if len(got.Options) != 5 || got.Options[0].Value != "Raffle 25" {
		t.Errorf("second page picker = %+v", got.Options)
	}
}

func pick(user, customID string, values ...string) *interaction.Context {
	i := &discordgo.Interaction{Type: discordgo.InteractionMessageComponent, GuildID: "g", ChannelID: "c",
		Member: &discordgo.Member{User: &discordgo.User{ID: user}},
		Data:   discordgo.MessageComponentInteractionData{CustomID: customID, ComponentType: discordgo.SelectMenuComponent, Values: values}}
	return interaction.NewContext(&interactiontest.Fake{}, i, nil, interaction.Private, interaction.Direct)
}

func firstPickerID(t *testing.T, authorID string) string {
	t.Helper()
	p := RenderRafflePage(t.Context(), nil, nil, &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{GuildID: "g"}}, 0, 0, authorID)
	picker, ok := pickerOf(p.Response)
	if !ok {
		t.Fatal("no picker on the page")
	}
	return picker.CustomID
}

func TestChoosingARaffleClosesItsMessageDeletesItAndRerendersThePage(t *testing.T) {
	st := useManage(t,
		&guilds.Raffle{Name: "Grand Raffle", ParticipantIDs: []string{"a"}, ChannelID: "c9", MessageID: "m1", WinnerID: "w"},
		&guilds.Raffle{Name: "Other"},
	)
	ic := pick("user", firstPickerID(t, "user"), "Grand Raffle")
	if err := handleRemovePicker(ic, nil); err != nil {
		t.Fatal(err)
	}
	fake := ic.Port.(*interactiontest.Fake)
	edit := lastCall(t, fake, "EditMessage")
	body := text(edit.Response)
	if edit.ChannelID != "c9" || edit.MessageID != "m1" || !strings.Contains(body, "This raffle has ended") || !strings.Contains(body, "<@w>") {
		t.Errorf("close edit = %+v", edit)
	}
	for _, row := range edit.Response.Components {
		for _, c := range row.(discordgo.ActionsRow).Components {
			if !c.(discordgo.Button).Disabled {
				t.Errorf("button %+v is enabled on a closed raffle", c)
			}
		}
	}
	if !slices.Equal(st.deleted, []string{"Grand Raffle"}) {
		t.Errorf("deleted %v", st.deleted)
	}
	update := lastCall(t, fake, "UpdateMessage")
	picker, _ := pickerOf(update.Response)
	if len(picker.Options) != 1 || picker.Options[0].Value != "Other" {
		t.Errorf("picker after removal = %+v", picker.Options)
	}
	if fake.Ops()[0] != "EditMessage" {
		t.Errorf("ops %v: the message must be closed before the record goes", fake.Ops())
	}
}

func TestChoosingTheLastRaffleClearsThePicker(t *testing.T) {
	useManage(t, &guilds.Raffle{Name: "Only"})
	ic := pick("user", firstPickerID(t, "user"), "Only")
	if err := handleRemovePicker(ic, nil); err != nil {
		t.Fatal(err)
	}
	fake := ic.Port.(*interactiontest.Fake)
	if slices.Contains(fake.Ops(), "EditMessage") {
		t.Errorf("a raffle without a message has none to close: %v", fake.Ops())
	}
	r := lastCall(t, fake, "UpdateMessage").Response
	if !strings.Contains(text(r), "There are no raffles") || r.Components == nil || len(r.Components) != 0 {
		t.Errorf("response = %+v, want the empty notice and an explicit empty component list", r)
	}
}

func TestAMessageThatNoLongerExistsDoesNotBlockTheRemoval(t *testing.T) {
	st := useManage(t, &guilds.Raffle{Name: "Gone", ChannelID: "c", MessageID: "m1"})
	ic := pick("user", firstPickerID(t, "user"), "Gone")
	unknown := &discordgo.RESTError{Response: &http.Response{StatusCode: 404}, Message: &discordgo.APIErrorMessage{Code: discordgo.ErrCodeUnknownMessage}}
	ic.Port.(*interactiontest.Fake).Fail = map[string]error{"EditMessage": unknown}
	if err := handleRemovePicker(ic, nil); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(st.deleted, []string{"Gone"}) {
		t.Errorf("deleted %v", st.deleted)
	}
}

func TestAnyOtherCloseFailureKeepsTheRaffleAndExplainsPrivately(t *testing.T) {
	st := useManage(t, &guilds.Raffle{Name: "Kept", ChannelID: "c9", MessageID: "m1"})
	ic := pick("user", firstPickerID(t, "user"), "Kept")
	fake := ic.Port.(*interactiontest.Fake)
	fake.Fail = map[string]error{"EditMessage": &discordgo.RESTError{Response: &http.Response{StatusCode: 403}}}
	if err := handleRemovePicker(ic, nil); err != nil {
		t.Fatal(err)
	}
	last := fake.Calls()[len(fake.Calls())-1]
	if len(st.deleted) != 0 || last.Op != "Reply" || last.Visibility != interaction.Private || !strings.Contains(last.Response.Content, "<#c9>") {
		t.Errorf("deleted %v, last call %+v", st.deleted, last)
	}
}

func TestPickerRemovesOnlyForTheModeratorWhoseListItIs(t *testing.T) {
	st := useManage(t, &guilds.Raffle{Name: "Grand Raffle", ChannelID: "c", MessageID: "m1"})
	ic := pick("stranger", firstPickerID(t, "user"), "Grand Raffle")
	if err := handleRemovePicker(ic, nil); err != nil {
		t.Fatal(err)
	}
	calls := ic.Port.(*interactiontest.Fake).Calls()
	if len(calls) != 1 || calls[0].Op != "Reply" || calls[0].Visibility != interaction.Private {
		t.Fatalf("calls = %+v, want one private refusal", calls)
	}
	if len(st.deleted) != 0 || len(st.raffles) != 1 {
		t.Errorf("a refused press must not change the store: deleted %v", st.deleted)
	}
}

func TestPickerIgnoresAChoiceThatIsAlreadyGone(t *testing.T) {
	st := useManage(t, &guilds.Raffle{Name: "Other"})
	ic := pick("user", firstPickerID(t, "user"), "Gone")
	if err := handleRemovePicker(ic, nil); err != nil {
		t.Fatal(err)
	}
	calls := ic.Port.(*interactiontest.Fake).Calls()
	if len(calls) != 1 || calls[0].Op != "UpdateMessage" || len(st.deleted) != 0 {
		t.Errorf("calls %+v, deleted %v: a stale choice just re-renders", calls, st.deleted)
	}
}

func TestPickerDeleteFailureIsRefusedPrivately(t *testing.T) {
	st := useManage(t, &guilds.Raffle{Name: "Grand Raffle"})
	st.failDelete = errors.New("db down")
	ic := pick("user", firstPickerID(t, "user"), "Grand Raffle")
	if err := handleRemovePicker(ic, nil); err != nil {
		t.Fatal(err)
	}
	calls := ic.Port.(*interactiontest.Fake).Calls()
	if got := calls[len(calls)-1]; got.Op != "Reply" || got.Visibility != interaction.Private {
		t.Errorf("last call = %+v", got)
	}
}

func TestPickerStillDeletesWhenTheRaffleChannelWasDeleted(t *testing.T) {
	st := useManage(t, &guilds.Raffle{Name: "Gone", ChannelID: "c", MessageID: "m1"})
	ic := pick("user", firstPickerID(t, "user"), "Gone")
	ic.Port.(*interactiontest.Fake).Fail = map[string]error{"EditMessage": restError(discordgo.ErrCodeUnknownChannel, 404)}
	if err := handleRemovePicker(ic, nil); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(st.deleted, []string{"Gone"}) {
		t.Errorf("deleted %v", st.deleted)
	}
}

func TestPickerRemovesNothingWhenTwoNamesClipAlike(t *testing.T) {
	shared := strings.Repeat("a", maxChoiceLength)
	st := useManage(t,
		&guilds.Raffle{Name: shared + "-first", ChannelID: "c", MessageID: "m1"},
		&guilds.Raffle{Name: shared + "-second", ChannelID: "c", MessageID: "m2"},
	)
	ic := pick("user", firstPickerID(t, "user"), shared)
	if err := handleRemovePicker(ic, nil); err != nil {
		t.Fatal(err)
	}
	fake := ic.Port.(*interactiontest.Fake)
	if len(st.deleted) != 0 || len(st.raffles) != 2 || slices.Contains(fake.Ops(), "EditMessage") {
		t.Errorf("deleted %v, ops %v: an ambiguous choice must change nothing", st.deleted, fake.Ops())
	}
	last := fake.Calls()[len(fake.Calls())-1]
	if last.Op != "Reply" || last.Visibility != interaction.Private ||
		!strings.Contains(last.Response.Content, shared+"-first") || !strings.Contains(last.Response.Content, shared+"-second") ||
		!strings.Contains(last.Response.Content, "/raffle remove") {
		t.Errorf("last call = %+v", last)
	}
}
