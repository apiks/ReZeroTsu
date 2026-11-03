package reminders

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/database/reminders"
	"ReZeroTsu/internal/interaction"
	"ReZeroTsu/internal/interaction/interactiontest"

	"github.com/bwmarrin/discordgo"
)

// fakeStore is an in-memory store. Like the real one it only appends and pulls, never rewrites the document.
type fakeStore struct {
	docs        map[string][]reminders.RemindMe
	isGuild     map[string]bool
	getErr      error
	removeCalls []string
}

func newFakeStore() *fakeStore {
	return &fakeStore{docs: map[string][]reminders.RemindMe{}, isGuild: map[string]bool{}}
}

func (f *fakeStore) with(uid string, rs ...reminders.RemindMe) *fakeStore {
	f.docs[uid] = append(f.docs[uid], rs...)
	return f
}

func (f *fakeStore) GetByID(_ context.Context, id string) (*reminders.RemindMeSlice, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if len(f.docs[id]) == 0 {
		return nil, nil
	}
	return &reminders.RemindMeSlice{ID: id, Reminders: append([]reminders.RemindMe(nil), f.docs[id]...)}, nil
}

func (f *fakeStore) Append(_ context.Context, id string, isGuild bool, rm reminders.RemindMe) error {
	f.docs[id] = append(f.docs[id], rm)
	f.isGuild[id] = isGuild
	return nil
}

func (f *fakeStore) RemoveByRemindIDs(_ context.Context, id string, ids []int) error {
	f.removeCalls = append(f.removeCalls, "ids")
	var kept []reminders.RemindMe
	for _, r := range f.docs[id] {
		drop := false
		for _, x := range ids {
			drop = drop || x == r.RemindID
		}
		if !drop {
			kept = append(kept, r)
		}
	}
	f.docs[id] = kept
	return nil
}

func (f *fakeStore) RemoveByRefs(_ context.Context, id string, refs []reminders.RemindRef) error {
	f.removeCalls = append(f.removeCalls, "refs")
	var kept []reminders.RemindMe
	for _, r := range f.docs[id] {
		drop := false
		for _, ref := range refs {
			drop = drop || (ref.RemindID == r.RemindID && ref.Date.Equal(r.Date))
		}
		if !drop {
			kept = append(kept, r)
		}
	}
	f.docs[id] = kept
	return nil
}

func (f *fakeStore) ids(uid string) []int {
	var out []int
	for _, r := range f.docs[uid] {
		out = append(out, r.RemindID)
	}
	return out
}

func useStore(t *testing.T, st *fakeStore) {
	t.Helper()
	old := storeOf
	storeOf = func(*database.Client) store { return st }
	t.Cleanup(func() { storeOf = old })
}

var due = time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)

func rem(id int, msg string) reminders.RemindMe {
	return reminders.RemindMe{RemindID: id, Message: msg, Date: due.Add(time.Duration(id) * time.Hour), CreatedAt: due.Add(-time.Hour)}
}

func many(n int) []reminders.RemindMe {
	var out []reminders.RemindMe
	for i := 1; i <= n; i++ {
		out = append(out, rem(i, fmt.Sprintf("task %d", i)))
	}
	return out
}

func option(name string, t discordgo.ApplicationCommandOptionType, v any) *discordgo.ApplicationCommandInteractionDataOption {
	return &discordgo.ApplicationCommandInteractionDataOption{Name: name, Type: t, Value: v}
}

// slashContext is the context of a subcommand run by "user" in channel "c", in guild "g" or, with guildID "", in DMs.
// Like dispatch for a Deferred command it has acknowledged privately.
func slashContext(t *testing.T, fake *interactiontest.Fake, guildID string, opts ...*discordgo.ApplicationCommandInteractionDataOption) *interaction.Context {
	t.Helper()
	i := &discordgo.Interaction{Type: discordgo.InteractionApplicationCommand, GuildID: guildID, ChannelID: "c",
		Data: discordgo.ApplicationCommandInteractionData{Name: "reminder", Options: opts}}
	u := &discordgo.User{ID: "user", Username: "invoker"}
	if guildID != "" {
		i.Member = &discordgo.Member{User: u}
	} else {
		i.User = u
	}
	ic := interaction.NewContext(fake, i, nil, interaction.Private, interaction.Deferred)
	if err := ic.Defer(interaction.Private); err != nil {
		t.Fatal(err)
	}
	return ic
}

// shown returns the single EditReply of a run, failing when there is not exactly one.
func shown(t *testing.T, fake *interactiontest.Fake) interaction.Response {
	t.Helper()
	calls := fake.Calls()
	if len(calls) != 2 || calls[0].Op != "Defer" || calls[0].Visibility != interaction.Private || calls[1].Op != "EditReply" {
		t.Fatalf("calls = %+v, want a private Defer then one EditReply", calls)
	}
	return calls[1].Response
}

func embedText(r interaction.Response) string {
	var b strings.Builder
	b.WriteString(r.Content)
	for _, e := range r.Embeds {
		b.WriteString(e.Description)
		for _, f := range e.Fields {
			b.WriteString(f.Name + f.Value)
		}
	}
	return b.String()
}

func setOpts(timeStr, msg string) []*discordgo.ApplicationCommandInteractionDataOption {
	return []*discordgo.ApplicationCommandInteractionDataOption{
		option("time", discordgo.ApplicationCommandOptionString, timeStr),
		option("message", discordgo.ApplicationCommandOptionString, msg),
	}
}

func TestSetStoresTheReminderAndShowsWhenItFires(t *testing.T) {
	st := newFakeStore()
	useStore(t, st)
	fake := &interactiontest.Fake{}
	before := time.Now()
	if err := runSet(slashContext(t, fake, "g", setOpts("1h30m", "stretch")...)); err != nil {
		t.Fatal(err)
	}
	text := embedText(shown(t, fake))
	got := st.docs["user"]
	if len(got) != 1 || got[0].Message != "stretch" || got[0].RemindID != 1 || got[0].CommandChannel != "c" || got[0].Origin != reminders.OriginServer {
		t.Fatalf("stored = %+v", got)
	}
	if !st.isGuild["user"] {
		t.Error("document not marked as guild-created for a reminder set in a server")
	}
	if want := got[0].Date.Sub(before); want < 89*time.Minute || want > 91*time.Minute {
		t.Errorf("due in %v, want about 1h30m", want)
	}
	unix := got[0].Date.UTC().Unix()
	if !strings.Contains(text, fmt.Sprintf("<t:%d:F>", unix)) || !strings.Contains(text, fmt.Sprintf("<t:%d:R>", unix)) {
		t.Errorf("confirmation = %q, want the fire time as <t:%d:F> and <t:%d:R>", text, unix, unix)
	}
}

func TestSetInDMsRecordsADMOrigin(t *testing.T) {
	st := newFakeStore()
	useStore(t, st)
	fake := &interactiontest.Fake{}
	if err := runSet(slashContext(t, fake, "", setOpts("5m", "x")...)); err != nil {
		t.Fatal(err)
	}
	shown(t, fake)
	if got := st.docs["user"]; len(got) != 1 || got[0].Origin != reminders.OriginDM || st.isGuild["user"] {
		t.Fatalf("stored = %+v isGuild=%v", got, st.isGuild["user"])
	}
}

func TestSetAssignsTheNextIDAfterTheHighestOne(t *testing.T) {
	st := newFakeStore().with("user", rem(2, "a"), rem(7, "b"))
	useStore(t, st)
	fake := &interactiontest.Fake{}
	if err := runSet(slashContext(t, fake, "g", setOpts("5m", "c")...)); err != nil {
		t.Fatal(err)
	}
	shown(t, fake)
	if got := st.ids("user"); len(got) != 3 || got[2] != 8 {
		t.Errorf("ids = %v, want the new one to be 8", got)
	}
}

func TestSetRefusesBadInputWithoutStoring(t *testing.T) {
	tests := []struct {
		name, timeStr, msg, want string
	}{
		{"unparseable time", "soon", "x", "Invalid time given."},
		{"zero duration", "0m", "x", "Cannot use that time. Please use another."},
		{"blank message", "5m", "", "Please provide time and message."},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := newFakeStore()
			useStore(t, st)
			fake := &interactiontest.Fake{}
			if err := runSet(slashContext(t, fake, "g", setOpts(tc.timeStr, tc.msg)...)); err != nil {
				t.Fatal(err)
			}
			if text := embedText(shown(t, fake)); !strings.Contains(text, tc.want) {
				t.Errorf("reply = %q, want %q", text, tc.want)
			}
			if len(st.docs["user"]) != 0 {
				t.Errorf("stored %+v, want nothing", st.docs["user"])
			}
		})
	}
}

func TestSetAcceptsTheSameTimeFormatsAsBefore(t *testing.T) {
	for _, in := range []string{"2w1d12h30m", "1 hour", "15 minutes", "2 weeks 1 day 12 hours 30 minutes", "1W"} {
		st := newFakeStore()
		useStore(t, st)
		fake := &interactiontest.Fake{}
		if err := runSet(slashContext(t, fake, "g", setOpts(in, "x")...)); err != nil {
			t.Fatal(err)
		}
		if len(st.docs["user"]) != 1 {
			t.Errorf("time %q was not accepted: %s", in, embedText(shown(t, fake)))
		}
	}
}

func TestSetStoreFailureIsExplainedPrivately(t *testing.T) {
	st := newFakeStore()
	st.getErr = fmt.Errorf("db down")
	useStore(t, st)
	fake := &interactiontest.Fake{}
	if err := runSet(slashContext(t, fake, "g", setOpts("5m", "x")...)); err != nil {
		t.Fatal(err)
	}
	if text := embedText(shown(t, fake)); !strings.Contains(text, "Could not load your reminders.") {
		t.Errorf("reply = %q", text)
	}
}

func TestListWithNoRemindersSaysSo(t *testing.T) {
	useStore(t, newFakeStore())
	fake := &interactiontest.Fake{}
	if err := runList(slashContext(t, fake, "g")); err != nil {
		t.Fatal(err)
	}
	if text := embedText(shown(t, fake)); !strings.Contains(text, "No saved reminders for you found.") {
		t.Errorf("reply = %q", text)
	}
}

func pickerOf(t *testing.T, r interaction.Response) *discordgo.SelectMenu {
	t.Helper()
	for _, row := range r.Components {
		for _, c := range row.(discordgo.ActionsRow).Components {
			if m, ok := c.(discordgo.SelectMenu); ok {
				return &m
			}
		}
	}
	t.Fatalf("no select menu in %+v", r.Components)
	return nil
}

func TestListShowsAPageWithARemovePickerForItsEntries(t *testing.T) {
	useStore(t, newFakeStore().with("user", many(30)...))
	fake := &interactiontest.Fake{}
	if err := runList(slashContext(t, fake, "g")); err != nil {
		t.Fatal(err)
	}
	r := shown(t, fake)
	emb := r.Embeds[0]
	if len(emb.Fields) != 25 || !strings.Contains(emb.Description, "Page 1 of 2") {
		t.Errorf("page = %d fields, %q", len(emb.Fields), emb.Description)
	}
	menu := pickerOf(t, r)
	if len(menu.Options) != 25 {
		t.Errorf("picker options = %d, want 25", len(menu.Options))
	}
	if !strings.HasPrefix(menu.CustomID, removePickerPrefix) {
		t.Errorf("picker custom ID = %q", menu.CustomID)
	}
	var buttons int
	for _, row := range r.Components {
		for _, c := range row.(discordgo.ActionsRow).Components {
			if _, ok := c.(discordgo.Button); ok {
				buttons++
			}
		}
	}
	if buttons != 2 {
		t.Errorf("paging buttons = %d, want 2 for a two-page list", buttons)
	}
}

func TestShortListHasAPickerAndNoPagingButtons(t *testing.T) {
	useStore(t, newFakeStore().with("user", many(3)...))
	fake := &interactiontest.Fake{}
	if err := runList(slashContext(t, fake, "g")); err != nil {
		t.Fatal(err)
	}
	r := shown(t, fake)
	if len(r.Components) != 1 || len(pickerOf(t, r).Options) != 3 {
		t.Errorf("components = %+v", r.Components)
	}
}

func pickContext(userID, customID string, values ...string) (*interaction.Context, *interactiontest.Fake) {
	fake := &interactiontest.Fake{}
	i := &discordgo.Interaction{Type: discordgo.InteractionMessageComponent, GuildID: "g", ChannelID: "c",
		Member: &discordgo.Member{User: &discordgo.User{ID: userID}},
		Data:   discordgo.MessageComponentInteractionData{CustomID: customID, ComponentType: discordgo.SelectMenuComponent, Values: values}}
	return interaction.NewContext(fake, i, nil, interaction.Private, interaction.Direct), fake
}

func TestPickerRemovesTheChosenReminderAndRerendersThePage(t *testing.T) {
	st := newFakeStore().with("user", many(3)...)
	useStore(t, st)
	ic, fake := pickContext("user", removePickerID("user", 0), pickValue(st.docs["user"][1]))
	if err := handleRemovePick(ic, nil); err != nil {
		t.Fatal(err)
	}
	if got := st.ids("user"); fmt.Sprint(got) != "[1 3]" {
		t.Errorf("left = %v, want [1 3]", got)
	}
	calls := fake.Calls()
	if len(calls) != 1 || calls[0].Op != "UpdateMessage" {
		t.Fatalf("calls = %+v, want one UpdateMessage", calls)
	}
	if n := len(pickerOf(t, calls[0].Response).Options); n != 2 {
		t.Errorf("picker options after removal = %d, want 2", n)
	}
}

func TestPickerRefusesAnyoneButTheListsOwner(t *testing.T) {
	st := newFakeStore().with("owner", many(2)...)
	useStore(t, st)
	ic, fake := pickContext("intruder", removePickerID("owner", 0), pickValue(st.docs["owner"][0]))
	if err := handleRemovePick(ic, nil); err != nil {
		t.Fatal(err)
	}
	if got := st.ids("owner"); fmt.Sprint(got) != "[1 2]" {
		t.Errorf("owner's reminders = %v, want untouched", got)
	}
	calls := fake.Calls()
	if len(calls) != 1 || calls[0].Op != "Reply" || calls[0].Visibility != interaction.Private {
		t.Errorf("calls = %+v, want one private Reply", calls)
	}
}

func TestPickerNeverRemovesAReminderThatTookTheChosenOnesID(t *testing.T) {
	st := newFakeStore().with("user", rem(1, "old"))
	useStore(t, st)
	value := pickValue(st.docs["user"][0])
	// The listed reminder was delivered meanwhile and a new one took its ID, due at another time.
	st.docs["user"] = []reminders.RemindMe{{RemindID: 1, Message: "new", Date: due.Add(48 * time.Hour)}}
	ic, fake := pickContext("user", removePickerID("user", 0), value)
	if err := handleRemovePick(ic, nil); err != nil {
		t.Fatal(err)
	}
	if got := st.docs["user"]; len(got) != 1 || got[0].Message != "new" {
		t.Errorf("stored = %+v, want the newer reminder kept", got)
	}
	if ops := fake.Ops(); len(ops) != 1 || ops[0] != "UpdateMessage" {
		t.Errorf("ops = %v, want the page re-rendered", ops)
	}
}

func TestPickerOnTheLastReminderShowsAnEmptyList(t *testing.T) {
	st := newFakeStore().with("user", rem(1, "only"))
	useStore(t, st)
	ic, fake := pickContext("user", removePickerID("user", 0), pickValue(st.docs["user"][0]))
	if err := handleRemovePick(ic, nil); err != nil {
		t.Fatal(err)
	}
	calls := fake.Calls()
	if len(calls) != 1 || calls[0].Op != "UpdateMessage" || !strings.Contains(embedText(calls[0].Response), "No saved reminders for you found.") {
		t.Fatalf("calls = %+v", calls)
	}
	if calls[0].Response.Components == nil || len(calls[0].Response.Components) != 0 {
		t.Errorf("components = %#v, want an empty non-nil list so the picker disappears", calls[0].Response.Components)
	}
}

func removeOpts(id int64) []*discordgo.ApplicationCommandInteractionDataOption {
	return []*discordgo.ApplicationCommandInteractionDataOption{option("id", discordgo.ApplicationCommandOptionInteger, float64(id))}
}

func TestRemoveDeletesByID(t *testing.T) {
	st := newFakeStore().with("user", many(3)...)
	useStore(t, st)
	fake := &interactiontest.Fake{}
	if err := runRemove(slashContext(t, fake, "g", removeOpts(2)...)); err != nil {
		t.Fatal(err)
	}
	if text := embedText(shown(t, fake)); !strings.Contains(text, "Deleted reminder with ID `2`") {
		t.Errorf("reply = %q", text)
	}
	if got := st.ids("user"); fmt.Sprint(got) != "[1 3]" {
		t.Errorf("left = %v", got)
	}
}

func TestRemoveExplainsAMissingReminder(t *testing.T) {
	tests := []struct {
		name  string
		store *fakeStore
		id    int64
		want  string
	}{
		{"no reminders at all", newFakeStore(), 1, "No saved reminders for you found."},
		{"unknown ID", newFakeStore().with("user", rem(1, "a")), 9, "No reminder found with that ID."},
		{"non-positive ID", newFakeStore().with("user", rem(1, "a")), 0, "Invalid ID."},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			useStore(t, tc.store)
			fake := &interactiontest.Fake{}
			if err := runRemove(slashContext(t, fake, "g", removeOpts(tc.id)...)); err != nil {
				t.Fatal(err)
			}
			if text := embedText(shown(t, fake)); !strings.Contains(text, tc.want) {
				t.Errorf("reply = %q, want %q", text, tc.want)
			}
		})
	}
}

func TestRemoveAutocompleteMatchesIDAndText(t *testing.T) {
	st := newFakeStore().with("user", rem(1, "buy milk"), rem(12, "call mum"), rem(3, "pay rent"))
	useStore(t, st)
	choices := func(typed string) []string {
		var out []string
		for _, c := range remindIDChoices(context.Background(), st, "user", typed) {
			out = append(out, c.Name)
		}
		return out
	}
	if got := choices("MILK"); len(got) != 1 || got[0] != "1: buy milk" {
		t.Errorf("text match = %v", got)
	}
	if got := choices("2"); len(got) != 1 || !strings.HasPrefix(got[0], "12:") {
		t.Errorf("ID match = %v", got)
	}
	if got := choices("l"); len(got) != 2 {
		t.Errorf("match anywhere = %v, want milk and call", got)
	}
	if got := choices(""); len(got) != 3 || !strings.HasPrefix(got[0], "1:") || !strings.HasPrefix(got[2], "12:") {
		t.Errorf("blank = %v, want all three sorted by ID", got)
	}
}

func TestRemoveAutocompleteKeepsChoiceNamesWithinDiscordsLimit(t *testing.T) {
	long := strings.Repeat("é", 300)
	st := newFakeStore().with("user", rem(1, long))
	for _, c := range remindIDChoices(context.Background(), st, "user", "") {
		if n := len([]rune(c.Name)); n > 100 {
			t.Errorf("choice name has %d characters", n)
		}
	}
}

func TestOldReminderCommandsAreGoneAndReminderIsUserInstallable(t *testing.T) {
	var reminder *discordgo.ApplicationCommand
	for _, d := range commands.Definitions() {
		switch d.Name {
		case "remind", "remove-remind", "reminders":
			t.Errorf("old command %q is still registered", d.Name)
		case "reminder":
			reminder = d
		}
	}
	if reminder == nil {
		t.Fatal("reminder is not registered")
	}
	var subs []string
	for _, o := range reminder.Options {
		subs = append(subs, o.Name)
	}
	if fmt.Sprint(subs) != "[set list remove]" {
		t.Errorf("subcommands = %v", subs)
	}
	installs := *reminder.IntegrationTypes
	if len(installs) != 2 {
		t.Errorf("installs = %v, want guild and user", installs)
	}
	var private bool
	for _, c := range *reminder.Contexts {
		private = private || c == discordgo.InteractionContextPrivateChannel
	}
	if !private {
		t.Errorf("contexts = %v, want private channels too", *reminder.Contexts)
	}
}

func TestReminderRepliesAreDeclaredPrivate(t *testing.T) {
	for _, c := range commands.AllCommands() {
		if c.Name != "reminder" {
			continue
		}
		if c.Visibility != commands.Private {
			t.Errorf("visibility = %v, want Private", c.Visibility)
		}
		for _, s := range c.Subcommands {
			if s.Visibility != 0 && s.Visibility != commands.Private {
				t.Errorf("%s visibility = %v", s.Name, s.Visibility)
			}
		}
		return
	}
	t.Fatal("reminder is not registered")
}
