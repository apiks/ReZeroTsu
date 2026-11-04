package animesubs

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"ReZeroTsu/internal/animeschedule"
	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/database/anime_subs"
	"ReZeroTsu/internal/database/guilds"
	"ReZeroTsu/internal/interaction/interactiontest"

	"github.com/bwmarrin/discordgo"
)

// frierenRow is a timetable show whose title, romaji, English and native names all differ.
func frierenRow() animeschedule.ShowEntry {
	e := entry("Sousou no Frieren", "frieren", "Ep 6", at(2*24*time.Hour))
	e.Romaji, e.English, e.Native = "Frieren Romaji", "Frieren: Beyond Journey's End", "葬送のフリーレン"
	return e
}

// apothecaryRow is a timetable show with an English name and no native name.
func apothecaryRow() animeschedule.ShowEntry {
	e := entry("Kusuriya no Hitorigoto", "apothecary", "Ep 3", at(3*24*time.Hour))
	e.English = "The Apothecary Diaries"
	return e
}

// upcomingWithNames is an upcoming show whose names all differ.
func upcomingWithNames() animeschedule.UpcomingShow {
	return animeschedule.UpcomingShow{Name: "Ore dake Level Up", Route: "solo-leveling", Romaji: "Ore dake Level Up Romaji",
		English: "Solo Leveling", Native: "俺だけレベルアップな件"}
}

// seedNames loads Frieren and the Apothecary Diaries on the timetable and Solo Leveling on the upcoming list.
func seedNames(t *testing.T) {
	t.Helper()
	seedUpcoming(t, []animeschedule.UpcomingShow{upcomingWithNames()}, frierenRow(), apothecaryRow())
}

// serverPrefers makes the server every test interaction runs in prefer name, which /subscription must ignore.
func serverPrefers(t *testing.T, name string) {
	t.Helper()
	prev := guildSettings
	guildSettings = func(context.Context, *database.Client, string) (*guilds.GuildSettings, error) {
		return &guilds.GuildSettings{Donghua: true, PreferredName: name}, nil
	}
	t.Cleanup(func() { guildSettings = prev })
}

// memberStore is a store where "user" prefers name and follows subs.
func memberStore(name string, subs ...*anime_subs.ShowSub) *fakeStore {
	st := newFakeStore()
	st.docs["user"] = subs
	if name != "" {
		st.preferred["user"] = name
	}
	return st
}

// followed are the member's subscriptions: two timetable shows, an upcoming show stored under an older title, and a
// show outside the loaded weeks and the upcoming list.
func followed() []*anime_subs.ShowSub {
	return []*anime_subs.ShowSub{
		{Show: "Sousou no Frieren", Route: "frieren"},
		{Show: "Kusuriya no Hitorigoto", Route: "apothecary"},
		{Show: "Solo Leveling Old Title", Route: "solo-leveling"},
		{Show: "Gone Show", Route: "gone-show"},
	}
}

func listLines(t *testing.T) string {
	t.Helper()
	return reply(t, invoke(t, "list")).Embeds[0].Description
}

func pickerLabelsAndValues(opts []discordgo.SelectMenuOption) string {
	var out []string
	for _, o := range opts {
		out = append(out, o.Label+"="+o.Value)
	}
	return strings.Join(out, "|")
}

func TestListCallsEachShowByTheMembersPreferredNameEvenWhereTheServerPrefersAnother(t *testing.T) {
	seedNames(t)
	serverPrefers(t, "english")
	useStore(t, memberStore("native", followed()...))
	got := listLines(t)
	for _, want := range []string{
		"1. 葬送のフリーレン · Ep 6 ",
		// No native name: the title, never the English name.
		"2. Kusuriya no Hitorigoto · Ep 3 ",
		"3. 俺だけレベルアップな件 · premiere date TBA",
		// Outside the loaded weeks and the upcoming list: the stored title.
		"4. Gone Show · no upcoming episode listed",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("list %q lacks %q", got, want)
		}
	}
}

func TestListUsesEachPreferredName(t *testing.T) {
	seedNames(t)
	for _, tc := range []struct{ name, want string }{
		{"romaji", "1. Frieren Romaji · "},
		{"english", "1. Frieren: Beyond Journey's End · "},
		{"native", "1. 葬送のフリーレン · "},
		{"title", "1. Sousou no Frieren · "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			useStore(t, memberStore(tc.name, &anime_subs.ShowSub{Show: "Sousou no Frieren", Route: "frieren"}))
			if got := listLines(t); !strings.HasPrefix(got, tc.want) {
				t.Errorf("list %q, want it to start %q", got, tc.want)
			}
		})
	}
}

func TestListClipsALongShownNameToTheListBudget(t *testing.T) {
	long := entry("Short Title", "long", "Ep 1", at(time.Hour))
	long.Native = strings.Repeat("長", 90)
	seedEntries(t, long)
	useStore(t, memberStore("native", &anime_subs.ShowSub{Show: "Short Title", Route: "long"}))
	if got, want := listLines(t), "1. "+strings.Repeat("長", maxListTitleUnits)+"… · "; !strings.HasPrefix(got, want) {
		t.Errorf("list %q, want it to start %q", got, want)
	}
}

func TestRemovePickerIsLabelledByTheShownNameAndRemovesTheStoredTitle(t *testing.T) {
	seedNames(t)
	serverPrefers(t, "romaji")
	st := memberStore("english", followed()...)
	useStore(t, st)
	got := pickerLabelsAndValues(pickerOptions(reply(t, invoke(t, "list"))))
	want := "Frieren: Beyond Journey's End=Sousou no Frieren|The Apothecary Diaries=Kusuriya no Hitorigoto|" +
		"Solo Leveling=Solo Leveling Old Title|Gone Show=Gone Show"
	if got != want {
		t.Errorf("picker = %q, want %q", got, want)
	}

	ic := pick("user", pickerID(t, st, 0), "Kusuriya no Hitorigoto")
	if err := handleRemovePicker(ic, nil); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(st.names("user"), ","); got != "Sousou no Frieren,Solo Leveling Old Title,Gone Show" {
		t.Errorf("subscriptions = %v, want the Apothecary Diaries removed", got)
	}
	r := ic.Port.(*interactiontest.Fake).Calls()[0].Response
	if got := pickerLabelsAndValues(pickerOptions(r)); !strings.HasPrefix(got, "Frieren: Beyond Journey's End=Sousou no Frieren|Solo Leveling=") {
		t.Errorf("re-rendered picker = %q, want the member's shown names", got)
	}
	if desc := r.Embeds[0].Description; !strings.HasPrefix(desc, "1. Frieren: Beyond Journey's End · ") {
		t.Errorf("re-rendered list = %q, want the member's shown names", desc)
	}
}

func TestALaterListPageUsesTheMembersPreferredName(t *testing.T) {
	seedNames(t)
	subs := make([]*anime_subs.ShowSub, 0, 26)
	for _, n := range shows(25) {
		subs = append(subs, &anime_subs.ShowSub{Show: n})
	}
	subs = append(subs, &anime_subs.ShowSub{Show: "Sousou no Frieren", Route: "frieren"})
	useStore(t, memberStore("native", subs...))
	p := RenderSubscriptionPage(t.Context(), nil, nil, nil, 1, 26, "user")
	if p.Refusal != "" {
		t.Fatal(p.Refusal)
	}
	if got := p.Response.Embeds[0].Description; !strings.HasPrefix(got, "26. 葬送のフリーレン · ") {
		t.Errorf("second page = %q", got)
	}
	if got := pickerLabelsAndValues(pickerOptions(p.Response)); got != "葬送のフリーレン=Sousou no Frieren" {
		t.Errorf("second page picker = %q", got)
	}
}

func removeSuggestions(t *testing.T, st *fakeStore, typed string) string {
	t.Helper()
	var out []string
	for _, c := range autocomplete(t, st, "user", typed) {
		out = append(out, fmt.Sprintf("%s=%v", c.Name, c.Value))
	}
	return strings.Join(out, "|")
}

func TestRemoveSuggestionsAreLabelledByTheShownNameAndMatchItOrTheTitle(t *testing.T) {
	seedNames(t)
	serverPrefers(t, "english")
	st := memberStore("native", followed()...)
	if got, want := removeSuggestions(t, st, ""), "葬送のフリーレン=Sousou no Frieren|Kusuriya no Hitorigoto=Kusuriya no Hitorigoto|"+
		"俺だけレベルアップな件=Solo Leveling Old Title|Gone Show=Gone Show"; got != want {
		t.Errorf("suggestions = %q, want %q", got, want)
	}
	for _, typed := range []string{"フリーレン", "sousou"} {
		if got := removeSuggestions(t, st, typed); got != "葬送のフリーレン=Sousou no Frieren" {
			t.Errorf("typed %q: suggestions = %q, want Frieren by its native name", typed, got)
		}
	}
	if got := removeSuggestions(t, st, "beyond"); got != "" {
		t.Errorf("typed an English name the member does not prefer: suggestions = %q, want none", got)
	}
}

func TestRemovingASuggestionRemovesTheRightSubscriptionAndNamesItByTheShownName(t *testing.T) {
	seedNames(t)
	serverPrefers(t, "english")
	st := memberStore("native", followed()...)
	useStore(t, st)
	got := reply(t, invoke(t, "remove", animeOption("Sousou no Frieren")...)).Embeds[0].Description
	if want := "Success! You have unsubscribed from `葬送のフリーレン`."; got != want {
		t.Errorf("reply = %q, want %q", got, want)
	}
	if got := strings.Join(st.names("user"), ","); got != "Kusuriya no Hitorigoto,Solo Leveling Old Title,Gone Show" {
		t.Errorf("subscriptions = %v, want Frieren removed", got)
	}
}

func TestAddConfirmsByTheMembersPreferredNameWithTheNextEpisode(t *testing.T) {
	seedNames(t)
	serverPrefers(t, "romaji")
	useStore(t, memberStore("native"))
	got := reply(t, invoke(t, "add", animeOption("frieren")...)).Embeds[0].Description
	want := "Subscribed to **葬送のフリーレン**.\nNext episode: Ep 6, " + fullAndRelative(at(2*24*time.Hour)) + "."
	if got != want {
		t.Errorf("reply = %q, want %q", got, want)
	}
}

func TestAddAnswersAboutAFollowedShowByTheMembersPreferredName(t *testing.T) {
	seedNames(t)
	st := memberStore("english", &anime_subs.ShowSub{Show: "Sousou no Frieren", Route: "frieren"})
	useStore(t, st)
	if got := reply(t, invoke(t, "add", animeOption("frieren")...)).Embeds[0].Description; got != "You are already subscribed to `Frieren: Beyond Journey's End`." {
		t.Errorf("same tracks: reply = %q", got)
	}
	if got := reply(t, invoke(t, "add", animeOption("frieren")[0], trackOption("dub"))).Embeds[0].Description; got != "**Frieren: Beyond Journey's End** now alerts for Dub." {
		t.Errorf("new tracks: reply = %q", got)
	}
}

func TestAddInAServerThatHidesDonghuaNamesTheFollowedDonghuaByTheMembersPreferredName(t *testing.T) {
	soulLand := entry("Douluo Dalu", "soul-land", "Ep 2", at(time.Hour))
	soulLand.Donghua, soulLand.English = true, "Soul Land"
	seedEntries(t, soulLand)
	serverDonghua(t, false, nil)
	useStore(t, memberStore("english", &anime_subs.ShowSub{Show: "Douluo Dalu", Route: "soul-land"}))
	got := reply(t, invoke(t, "add", animeOption("soul-land")...)).Embeds[0].Description
	if !strings.HasPrefix(got, "You already follow `Soul Land`. ") {
		t.Errorf("reply = %q, want the member's shown name", got)
	}
}

func addSuggestionNames(t *testing.T, st *fakeStore, typed string) string {
	t.Helper()
	return strings.Join(names(suggestions(t, st, "user", typed)), "|")
}

func TestAddSuggestionsReadShownNameThenTitleWhenTheyDiffer(t *testing.T) {
	seedNames(t)
	for _, tc := range []struct{ name, want string }{
		{"native", "葬送のフリーレン (Sousou no Frieren)|Kusuriya no Hitorigoto|俺だけレベルアップな件 (Ore dake Level Up) · premiere date TBA"},
		{"english", "Frieren: Beyond Journey's End (Sousou no Frieren)|The Apothecary Diaries (Kusuriya no Hitorigoto)|Solo Leveling (Ore dake Level Up) · premiere date TBA"},
		// Under Title, today's "Title (English)".
		{"title", "Sousou no Frieren (Frieren: Beyond Journey's End)|Kusuriya no Hitorigoto (The Apothecary Diaries)|Ore dake Level Up (Solo Leveling) · premiere date TBA"},
		{"", "Sousou no Frieren (Frieren: Beyond Journey's End)|Kusuriya no Hitorigoto (The Apothecary Diaries)|Ore dake Level Up (Solo Leveling) · premiere date TBA"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := addSuggestionNames(t, memberStore(tc.name), ""); got != tc.want {
				t.Errorf("suggestions = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAddSuggestionsUseTheMembersPreferredNameInAServerThatPrefersAnother(t *testing.T) {
	seedNames(t)
	serverPrefers(t, "english")
	got := strings.Join(suggestionsAt(t, inServer, memberStore("native"), "frieren"), "|")
	if got != "葬送のフリーレン (Sousou no Frieren)" {
		t.Errorf("suggestions = %q", got)
	}
}

func TestAddSuggestionsShowTheTitleAloneWhenTheShownNameMatchesIt(t *testing.T) {
	same := entry("Dandadan", "dandadan", "Ep 1", at(time.Hour))
	same.English = " DANDADAN "
	seedEntries(t, same)
	if got := addSuggestionNames(t, memberStore("english"), ""); got != "Dandadan" {
		t.Errorf("suggestions = %q, want the title alone", got)
	}
}

func TestAddSuggestionsCutTheCombinedNameAndKeepTheirLabels(t *testing.T) {
	long := entry("Short Title", "long", "Ep 1", at(time.Hour))
	long.Native, long.MediaType = strings.Repeat("長", 120), animeschedule.MediaMovie
	seedEntries(t, long)
	st := memberStore("native", &anime_subs.ShowSub{Show: "Short Title", Route: "long"})
	got := addSuggestionNames(t, st, "")
	if want := " · Movie · subscribed"; !strings.HasSuffix(got, want) || len([]rune(got)) != maxChoiceLength {
		t.Errorf("suggestion %q (%d characters), want %d ending %q", got, len([]rune(got)), maxChoiceLength, want)
	}
	if !strings.HasPrefix(got, "長長") {
		t.Errorf("suggestion %q, want it to start with the shown name", got)
	}
}

func TestWithTitlePreferredEverySubscriptionReplyReadsAsToday(t *testing.T) {
	// The timetable has since renamed the show; Title keeps the stored title, as today.
	renamed := frierenRow()
	renamed.Name = "Frieren New Title"
	seedUpcoming(t, []animeschedule.UpcomingShow{upcomingWithNames()}, renamed)
	serverPrefers(t, "native")
	st := memberStore("title", &anime_subs.ShowSub{Show: "Sousou no Frieren", Route: "frieren"}, &anime_subs.ShowSub{Show: "Gone Show"})
	useStore(t, st)
	r := reply(t, invoke(t, "list"))
	if got := r.Embeds[0].Description; !strings.HasPrefix(got, "1. Sousou no Frieren · Ep 6 ") || !strings.Contains(got, "\n2. Gone Show · ") {
		t.Errorf("list = %q", got)
	}
	if got := pickerLabelsAndValues(pickerOptions(r)); got != "Sousou no Frieren=Sousou no Frieren|Gone Show=Gone Show" {
		t.Errorf("picker = %q", got)
	}
	if got := removeSuggestions(t, st, ""); got != "Sousou no Frieren=Sousou no Frieren|Gone Show=Gone Show" {
		t.Errorf("remove suggestions = %q", got)
	}
	if got := reply(t, invoke(t, "remove", animeOption("sousou no frieren")...)).Embeds[0].Description; got != "Success! You have unsubscribed from `sousou no frieren`." {
		t.Errorf("remove = %q, want the typed text echoed as today", got)
	}
	if got := reply(t, invoke(t, "add", animeOption("solo-leveling")...)).Embeds[0].Description; !strings.HasPrefix(got, "Subscribed to **Ore dake Level Up**.\n") {
		t.Errorf("add = %q", got)
	}
}
