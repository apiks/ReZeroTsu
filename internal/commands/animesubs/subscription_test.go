package animesubs

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"ReZeroTsu/internal/animeschedule"
	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/interaction"
	"ReZeroTsu/internal/interaction/interactiontest"

	"github.com/bwmarrin/discordgo"
)

// useStore points the subscription commands at the in-memory store for the test.
func useStore(t *testing.T, st *fakeStore) {
	t.Helper()
	old := storeOf
	storeOf = func(*database.Client) subsStore { return st }
	t.Cleanup(func() { storeOf = old })
}

func subscriptionCommand(t *testing.T) *commands.Command {
	t.Helper()
	for _, c := range commands.AllCommands() {
		if c.Name == "subscription" {
			return c
		}
	}
	t.Fatal("/subscription is not registered")
	return nil
}

func subcommand(t *testing.T, name string) *commands.Subcommand {
	t.Helper()
	for _, s := range subscriptionCommand(t).Subcommands {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("/subscription has no %q subcommand", name)
	return nil
}

func animeOption(value string) []*discordgo.ApplicationCommandInteractionDataOption {
	return []*discordgo.ApplicationCommandInteractionDataOption{{Name: "anime", Type: discordgo.ApplicationCommandOptionString, Value: value}}
}

// invoke runs a subcommand the way dispatch does for a private deferred command: acknowledge privately, then run.
func invoke(t *testing.T, name string, opts ...*discordgo.ApplicationCommandInteractionDataOption) *interactiontest.Fake {
	t.Helper()
	fake := &interactiontest.Fake{}
	cmd := subscriptionCommand(t)
	ic := interactiontest.NewCommandContext(fake, cmd.Visibility, cmd.Response, discordgo.ApplicationCommandInteractionData{Name: "subscription", Options: opts})
	if err := ic.Defer(0); err != nil {
		t.Fatal(err)
	}
	if err := subcommand(t, name).Run(ic); err != nil {
		t.Fatal(err)
	}
	return fake
}

// reply is the one edit of the deferred placeholder, which a subcommand makes exactly once.
func reply(t *testing.T, fake *interactiontest.Fake) interaction.Response {
	t.Helper()
	calls := fake.Calls()
	if len(calls) != 2 || calls[0].Op != "Defer" || calls[0].Visibility != interaction.Private || calls[1].Op != "EditReply" {
		t.Fatalf("want a private Defer then one EditReply, got %+v", calls)
	}
	return calls[1].Response
}

func text(r interaction.Response) string {
	var b strings.Builder
	b.WriteString(r.Content)
	for _, e := range r.Embeds {
		b.WriteString(e.Title + " " + e.Description)
	}
	return b.String()
}

func TestSubscriptionRegistersFourPrivateSubcommandsAndNoOldSlashCommands(t *testing.T) {
	cmd := subscriptionCommand(t)
	if cmd.Visibility != commands.Private || cmd.Response != commands.Deferred {
		t.Errorf("visibility/response = %v/%v, want private and deferred", cmd.Visibility, cmd.Response)
	}
	var names []string
	for _, s := range cmd.Subcommands {
		names = append(names, s.Name)
		if s.Visibility != 0 && s.Visibility != commands.Private {
			t.Errorf("%s overrides the private visibility", s.Name)
		}
	}
	if strings.Join(names, ",") != "add,remove,list,clear" {
		t.Errorf("subcommands = %v", names)
	}
	for _, s := range cmd.Subcommands {
		if s.Name == "remove" && (len(s.Options) != 1 || !s.Options[0].Autocomplete || s.AutocompleteOption == nil) {
			t.Error("remove must autocomplete its anime option")
		}
	}
	for _, d := range commands.Definitions() {
		switch d.Name {
		case "sub", "unsub", "subs", "clearsubs":
			t.Errorf("old slash command %q is still registered", d.Name)
		}
	}
}

func TestAddSubscribesAndConfirmsPrivately(t *testing.T) {
	withLookup(t, "Frieren")
	st := newFakeStore()
	useStore(t, st)
	got := text(reply(t, invoke(t, "add", animeOption("frieren")...)))
	if !strings.Contains(got, "Subscribed to") || strings.Join(st.names("user"), ",") != "Frieren" {
		t.Errorf("reply %q, subscriptions %v", got, st.names("user"))
	}
}

func TestAddRefusesWhatIsAlreadyTheMembersEntry(t *testing.T) {
	withLookup(t, "Frieren")
	st := newFakeStore().with("user", "Frieren ")
	useStore(t, st)
	got := text(reply(t, invoke(t, "add", animeOption("Frieren")...)))
	if !strings.Contains(got, "already subscribed") || len(st.docs["user"]) != 1 || len(st.calls) != 0 {
		t.Errorf("reply %q, subscriptions %v, calls %v", got, st.names("user"), st.calls)
	}
}

func TestAddStopsAtTheSubscriptionLimit(t *testing.T) {
	withLookup(t, "Frieren")
	st := newFakeStore()
	for i := 0; i < maxUserSubscriptions; i++ {
		st.with("user", fmt.Sprintf("show %d", i))
	}
	useStore(t, st)
	got := text(reply(t, invoke(t, "add", animeOption("Frieren")...)))
	if !strings.Contains(got, "maximum") || len(st.docs["user"]) != maxUserSubscriptions {
		t.Errorf("reply %q, %d subscriptions", got, len(st.docs["user"]))
	}
}

func TestAddSeedsFromTodaysAiredEntry(t *testing.T) {
	withLookup(t, "Frieren")
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	t.Cleanup(animeschedule.SetClock(func() time.Time { return now }))
	aired := now.Add(-time.Minute).Unix()
	restore := animeschedule.Seed(map[string][]animeschedule.ShowEntry{animeschedule.DateKey(now): {{Name: "Frieren", AirTimeUnix: aired}}})
	t.Cleanup(restore)
	st := newFakeStore()
	useStore(t, st)
	invoke(t, "add", animeOption("Frieren")...)
	subs := st.docs["user"]
	if len(subs) != 1 || !subs[0].Notified || subs[0].LastNotifiedAirUnix != aired {
		t.Errorf("subscription = %+v, want notified at %d so today's episode is not DMed again", subs, aired)
	}
}

func TestAddRejectsAShowThatIsNotAiring(t *testing.T) {
	withLookup(t, "Frieren")
	st := newFakeStore()
	useStore(t, st)
	got := text(reply(t, invoke(t, "add", animeOption("nope")...)))
	if !strings.Contains(got, "could not find") || len(st.calls) != 0 {
		t.Errorf("reply %q, calls %v", got, st.calls)
	}
}

func TestRemoveUnsubscribesByStoredTitle(t *testing.T) {
	st := newFakeStore().with("user", "Frieren ", "Dandadan")
	useStore(t, st)
	got := text(reply(t, invoke(t, "remove", animeOption("frieren")...)))
	if !strings.Contains(got, "unsubscribed") || strings.Join(st.names("user"), ",") != "Dandadan" {
		t.Errorf("reply %q, subscriptions %v", got, st.names("user"))
	}
}

func TestRemoveTellsAMemberWhoIsNotSubscribed(t *testing.T) {
	st := newFakeStore().with("user", "Dandadan")
	useStore(t, st)
	got := text(reply(t, invoke(t, "remove", animeOption("Frieren")...)))
	if !strings.Contains(got, "not subscribed") || len(st.calls) != 0 {
		t.Errorf("reply %q, calls %v", got, st.calls)
	}
}

func TestRemoveOfAShowNotFollowedPointsToTheListCommand(t *testing.T) {
	commands.SetCommandIDs([]*discordgo.ApplicationCommand{{ID: "123", Name: "subscription", Type: discordgo.ChatApplicationCommand}})
	t.Cleanup(func() { commands.SetCommandIDs(nil) })
	st := newFakeStore().with("user", "Dandadan")
	useStore(t, st)
	got := text(reply(t, invoke(t, "remove", animeOption("Frieren")...)))
	if !strings.Contains(got, "See </subscription list:123> for the shows you follow.") {
		t.Errorf("reply %q lacks the list pointer with a command mention", got)
	}
}

func autocomplete(t *testing.T, st *fakeStore, user, typed string) []*discordgo.ApplicationCommandOptionChoice {
	t.Helper()
	useStore(t, st)
	i := &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{Type: discordgo.InteractionApplicationCommandAutocomplete, GuildID: "g",
		Member: &discordgo.Member{User: &discordgo.User{ID: user}}}}
	return subcommand(t, "remove").AutocompleteOption(t.Context(), nil, nil, i, "anime", typed)
}

func TestRemoveAutocompleteOffersOnlyTheInvokersSubscriptions(t *testing.T) {
	st := newFakeStore().with("user", "Frieren", "Dandadan").with("other", "Frieren Secret")
	got := autocomplete(t, st, "user", "")
	if len(got) != 2 || got[0].Value != "Frieren" || got[1].Value != "Dandadan" {
		t.Errorf("choices = %+v", got)
	}
	got = autocomplete(t, st, "user", "ANDA")
	if len(got) != 1 || got[0].Name != "Dandadan" {
		t.Errorf("match-anywhere choices = %+v", got)
	}
	if got := autocomplete(t, st, "stranger", ""); len(got) != 0 {
		t.Errorf("a member with no subscriptions was offered %+v", got)
	}
}

func seedWeek(t *testing.T) {
	t.Helper()
	t.Cleanup(animeschedule.Seed(map[string][]animeschedule.ShowEntry{
		"2026-11-02": {
			{Name: "Sousou no Frieren", Route: "frieren", Romaji: "Sousou no Frieren", English: "Frieren: Beyond Journey's End", Native: "葬送のフリーレン"},
			{Name: "Dandadan", Route: "dandadan", English: "Dandadan", Native: "ダンダダン"},
		},
		"2026-11-04": {{Name: "Sousou no Frieren", Route: "frieren", English: "Frieren: Beyond Journey's End"}},
	}))
}

func addAutocomplete(t *testing.T, typed string) []*discordgo.ApplicationCommandOptionChoice {
	t.Helper()
	i := &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{Type: discordgo.InteractionApplicationCommandAutocomplete,
		User: &discordgo.User{ID: "user"}}}
	return subcommand(t, "add").AutocompleteOption(t.Context(), nil, nil, i, "anime", typed)
}

func TestAddAutocompletesAiringShowsByAnyOfTheirFourNames(t *testing.T) {
	seedWeek(t)
	for _, typed := range []string{"frieren", "BEYOND journey", "葬送", "sousou"} {
		got := addAutocomplete(t, typed)
		if len(got) != 1 || got[0].Value != "Sousou no Frieren" {
			t.Errorf("typed %q: choices = %+v", typed, got)
		}
	}
	if got := addAutocomplete(t, ""); len(got) != 2 {
		t.Errorf("blank typed offered %d shows, want each airing show once", len(got))
	}
}

func TestAddChoicesAreNamedTitleThenEnglishWhenTheyDiffer(t *testing.T) {
	seedWeek(t)
	if got := addAutocomplete(t, "frieren"); got[0].Name != "Sousou no Frieren (Frieren: Beyond Journey's End)" {
		t.Errorf("name = %q", got[0].Name)
	}
	if got := addAutocomplete(t, "dandadan"); got[0].Name != "Dandadan" {
		t.Errorf("name = %q, want the bare title when English matches it", got[0].Name)
	}
}

func TestChoiceNameIsCutToDiscordsLimit(t *testing.T) {
	long := strings.Repeat("x", 120)
	if got := choiceName(animeschedule.ShowEntry{Name: "T", English: long}); len([]rune(got)) != 100 {
		t.Errorf("length = %d", len([]rune(got)))
	}
}

func TestAddResolvesATypedEnglishOrNativeName(t *testing.T) {
	seedWeek(t)
	for _, typed := range []string{"Frieren: Beyond Journey's End", "葬送のフリーレン"} {
		st := newFakeStore()
		useStore(t, st)
		got := text(reply(t, invoke(t, "add", animeOption(typed)...)))
		if !strings.Contains(got, "Subscribed to") || strings.Join(st.names("user"), ",") != "Sousou no Frieren" {
			t.Errorf("typed %q: reply %q, subscriptions %v", typed, got, st.names("user"))
		}
	}
}

func TestAddWithAnExistingTitleKeyedSubscriptionIsAlreadySubscribed(t *testing.T) {
	seedWeek(t)
	st := newFakeStore().with("user", "Sousou no Frieren")
	useStore(t, st)
	got := text(reply(t, invoke(t, "add", animeOption("Frieren: Beyond Journey's End")...)))
	if !strings.Contains(got, "already subscribed") || len(st.docs["user"]) != 1 {
		t.Errorf("reply %q, subscriptions %v", got, st.names("user"))
	}
}

func shows(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("Show %02d", i)
	}
	return out
}

// pickerOptions returns the options of the Remove picker in a rendered page, nil when it has none.
func pickerOptions(r interaction.Response) []discordgo.SelectMenuOption {
	for _, row := range r.Components {
		for _, c := range row.(discordgo.ActionsRow).Components {
			if m, ok := c.(discordgo.SelectMenu); ok {
				return m.Options
			}
		}
	}
	return nil
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

func TestListShowsTheMembersSubscriptionsWithARemovePicker(t *testing.T) {
	st := newFakeStore().with("user", "Frieren", "Dandadan")
	useStore(t, st)
	r := reply(t, invoke(t, "list"))
	if !strings.Contains(text(r), "Frieren") || !strings.Contains(text(r), "Dandadan") {
		t.Errorf("list = %q", text(r))
	}
	opts := pickerOptions(r)
	if len(opts) != 2 || opts[0].Value != "Frieren" || opts[1].Value != "Dandadan" {
		t.Errorf("picker options = %+v", opts)
	}
	if hasPageButtons(r) {
		t.Error("two subscriptions fit on one page")
	}
}

func TestListWithNothingSaysSoWithoutAPicker(t *testing.T) {
	useStore(t, newFakeStore())
	r := reply(t, invoke(t, "list"))
	if !strings.Contains(text(r), "no active") || len(r.Components) != 0 {
		t.Errorf("reply = %+v", r)
	}
}

func TestListPagesCarryTheirOwnPickerOfAtMost25(t *testing.T) {
	st := newFakeStore().with("user", shows(30)...)
	useStore(t, st)
	first := reply(t, invoke(t, "list"))
	if got := pickerOptions(first); len(got) != 25 || got[0].Value != "Show 00" || !hasPageButtons(first) {
		t.Fatalf("first page picker has %d options, buttons %v", len(got), hasPageButtons(first))
	}
	second := RenderSubscriptionPage(t.Context(), nil, nil, nil, 1, 30, "user")
	if second.Refusal != "" {
		t.Fatal(second.Refusal)
	}
	got := pickerOptions(second.Response)
	if len(got) != 5 || got[0].Value != "Show 25" {
		t.Errorf("second page picker = %+v", got)
	}
}

func pick(user, customID string, values ...string) *interaction.Context {
	i := &discordgo.Interaction{Type: discordgo.InteractionMessageComponent, GuildID: "g", ChannelID: "c",
		Member: &discordgo.Member{User: &discordgo.User{ID: user}},
		Data:   discordgo.MessageComponentInteractionData{CustomID: customID, ComponentType: discordgo.SelectMenuComponent, Values: values}}
	return interaction.NewContext(&interactiontest.Fake{}, i, nil, interaction.Private, interaction.Direct)
}

func pickerID(t *testing.T, st *fakeStore, page int) string {
	t.Helper()
	r := RenderSubscriptionPage(t.Context(), nil, nil, nil, page, len(st.docs["user"]), "user")
	for _, row := range r.Response.Components {
		for _, c := range row.(discordgo.ActionsRow).Components {
			if m, ok := c.(discordgo.SelectMenu); ok {
				return m.CustomID
			}
		}
	}
	t.Fatal("no picker on the page")
	return ""
}

func TestChoosingAnEntryRemovesItAndRerendersThePage(t *testing.T) {
	st := newFakeStore().with("user", "Frieren", "Dandadan")
	useStore(t, st)
	ic := pick("user", pickerID(t, st, 0), "Frieren")
	if err := handleRemovePicker(ic, nil); err != nil {
		t.Fatal(err)
	}
	calls := ic.Port.(*interactiontest.Fake).Calls()
	if len(calls) != 1 || calls[0].Op != "UpdateMessage" {
		t.Fatalf("calls = %+v, want one UpdateMessage", calls)
	}
	opts := pickerOptions(calls[0].Response)
	if strings.Join(st.names("user"), ",") != "Dandadan" || len(opts) != 1 || opts[0].Value != "Dandadan" {
		t.Errorf("subscriptions %v, picker %+v", st.names("user"), opts)
	}
}

func TestChoosingTheLastEntryClearsThePicker(t *testing.T) {
	st := newFakeStore().with("user", "Frieren")
	useStore(t, st)
	ic := pick("user", pickerID(t, st, 0), "Frieren")
	if err := handleRemovePicker(ic, nil); err != nil {
		t.Fatal(err)
	}
	r := ic.Port.(*interactiontest.Fake).Calls()[0].Response
	if !strings.Contains(text(r), "no active") || r.Components == nil || len(r.Components) != 0 {
		t.Errorf("response = %+v, want the empty notice and an explicit empty component list", r)
	}
}

func TestPickerRemovesOnlyForTheMemberWhoseListItIs(t *testing.T) {
	st := newFakeStore().with("user", "Frieren").with("stranger", "Frieren")
	useStore(t, st)
	ic := pick("stranger", pickerID(t, st, 0), "Frieren")
	if err := handleRemovePicker(ic, nil); err != nil {
		t.Fatal(err)
	}
	calls := ic.Port.(*interactiontest.Fake).Calls()
	if len(calls) != 1 || calls[0].Op != "Reply" || calls[0].Visibility != interaction.Private {
		t.Fatalf("calls = %+v, want one private refusal", calls)
	}
	if len(st.calls) != 0 || len(st.docs["user"]) != 1 || len(st.docs["stranger"]) != 1 {
		t.Errorf("a refused press must not change the store: calls %v", st.calls)
	}
}

func TestPickerIgnoresAChoiceThatIsNoLongerSubscribed(t *testing.T) {
	st := newFakeStore().with("user", "Dandadan")
	useStore(t, st)
	ic := pick("user", pickerID(t, st, 0), "Frieren")
	if err := handleRemovePicker(ic, nil); err != nil {
		t.Fatal(err)
	}
	calls := ic.Port.(*interactiontest.Fake).Calls()
	if len(calls) != 1 || calls[0].Op != "UpdateMessage" || len(st.calls) != 0 {
		t.Errorf("calls %+v, store calls %v: a stale choice just re-renders", calls, st.calls)
	}
}

func buttons(r interaction.Response) []discordgo.Button {
	var out []discordgo.Button
	for _, row := range r.Components {
		for _, c := range row.(discordgo.ActionsRow).Components {
			if b, ok := c.(discordgo.Button); ok {
				out = append(out, b)
			}
		}
	}
	return out
}

func TestClearAsksBeforeRemovingAnything(t *testing.T) {
	st := newFakeStore().with("user", "A", "B", "C")
	useStore(t, st)
	r := reply(t, invoke(t, "clear"))
	bs := buttons(r)
	if !strings.Contains(text(r), "Remove all 3 subscriptions?") || len(bs) != 2 {
		t.Fatalf("reply %q, buttons %+v", text(r), bs)
	}
	if bs[0].Label != "Remove all" || bs[0].Style != discordgo.DangerButton || bs[1].Label != "Keep them" || bs[1].Style != discordgo.SecondaryButton {
		t.Errorf("buttons = %+v", bs)
	}
	for _, b := range bs {
		if len(b.CustomID) >= 100 || !strings.Contains(b.CustomID, "user") || strings.Count(b.CustomID, ":") != 2 {
			t.Errorf("custom ID %q must carry only the author and stay under 100 characters", b.CustomID)
		}
	}
	if len(st.docs["user"]) != 3 || len(st.calls) != 0 {
		t.Errorf("asking must change nothing: %v, calls %v", st.names("user"), st.calls)
	}
}

func TestClearAsksAboutOneSubscriptionToo(t *testing.T) {
	useStore(t, newFakeStore().with("user", "A"))
	r := reply(t, invoke(t, "clear"))
	if !strings.Contains(text(r), "Remove all 1 subscriptions?") || len(buttons(r)) != 2 {
		t.Errorf("reply %q", text(r))
	}
}

func TestClearWithNothingAnswersWithoutButtons(t *testing.T) {
	useStore(t, newFakeStore())
	r := reply(t, invoke(t, "clear"))
	if !strings.Contains(text(r), "no active") || len(buttons(r)) != 0 {
		t.Errorf("reply %q, buttons %+v", text(r), buttons(r))
	}
}

func clearButtonID(t *testing.T, label string) string {
	t.Helper()
	st := newFakeStore().with("user", "A")
	useStore(t, st)
	for _, b := range buttons(reply(t, invoke(t, "clear"))) {
		if b.Label == label {
			return b.CustomID
		}
	}
	t.Fatalf("no %q button", label)
	return ""
}

func press(user, id string) *interaction.Context {
	i := &discordgo.Interaction{Type: discordgo.InteractionMessageComponent, GuildID: "g", ChannelID: "c",
		Member: &discordgo.Member{User: &discordgo.User{ID: user}},
		Data:   discordgo.MessageComponentInteractionData{CustomID: id, ComponentType: discordgo.ButtonComponent}}
	return interaction.NewContext(&interactiontest.Fake{}, i, nil, interaction.Private, interaction.Direct)
}

func TestRemoveAllClearsTheCurrentListAndDropsTheButtons(t *testing.T) {
	removeID := clearButtonID(t, "Remove all")
	st := newFakeStore().with("user", "A", "B").with("other", "C")
	useStore(t, st)
	ic := press("user", removeID)
	if err := handleClearButton(ic, nil); err != nil {
		t.Fatal(err)
	}
	calls := ic.Port.(*interactiontest.Fake).Calls()
	if len(calls) != 1 || calls[0].Op != "UpdateMessage" {
		t.Fatalf("calls = %+v", calls)
	}
	r := calls[0].Response
	if !strings.Contains(text(r), "All your subscriptions were removed.") || r.Components == nil || len(r.Components) != 0 {
		t.Errorf("response = %+v", r)
	}
	if len(st.docs["user"]) != 0 || strings.Join(st.names("other"), ",") != "C" {
		t.Errorf("user %v other %v", st.names("user"), st.names("other"))
	}
}

func TestKeepThemChangesNothingAndDropsTheButtons(t *testing.T) {
	keepID := clearButtonID(t, "Keep them")
	st := newFakeStore().with("user", "A")
	useStore(t, st)
	ic := press("user", keepID)
	if err := handleClearButton(ic, nil); err != nil {
		t.Fatal(err)
	}
	r := ic.Port.(*interactiontest.Fake).Calls()[0].Response
	if !strings.Contains(text(r), "Nothing was removed.") || r.Components == nil || len(r.Components) != 0 || len(st.docs["user"]) != 1 {
		t.Errorf("response = %+v, subscriptions %v", r, st.names("user"))
	}
}

func TestAnotherMembersPressOnClearIsRefusedPrivately(t *testing.T) {
	removeID := clearButtonID(t, "Remove all")
	st := newFakeStore().with("user", "A").with("stranger", "B")
	useStore(t, st)
	ic := press("stranger", removeID)
	if err := handleClearButton(ic, nil); err != nil {
		t.Fatal(err)
	}
	calls := ic.Port.(*interactiontest.Fake).Calls()
	if len(calls) != 1 || calls[0].Op != "Reply" || calls[0].Visibility != interaction.Private {
		t.Fatalf("calls = %+v, want one private refusal", calls)
	}
	if len(st.calls) != 0 || len(st.docs["user"]) != 1 || len(st.docs["stranger"]) != 1 {
		t.Errorf("a refused press must not change the store: %v", st.calls)
	}
}

func TestRemoveAllClearsShowsAddedAfterTheQuestion(t *testing.T) {
	removeID := clearButtonID(t, "Remove all")
	st := newFakeStore().with("user", "A", "B", "Added later")
	useStore(t, st)
	if err := handleClearButton(press("user", removeID), nil); err != nil {
		t.Fatal(err)
	}
	if len(st.docs["user"]) != 0 {
		t.Errorf("left %v", st.names("user"))
	}
}

func TestClearButtonsAreRegisteredForTheirPrefix(t *testing.T) {
	id := clearButtonID(t, "Remove all")
	if !strings.HasPrefix(id, clearPrefix) {
		t.Errorf("custom ID %q lacks prefix %q", id, clearPrefix)
	}
}
