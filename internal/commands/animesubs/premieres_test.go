package animesubs

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"ReZeroTsu/internal/animeschedule"
	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/database/anime_subs"

	"github.com/bwmarrin/discordgo"
)

// frierenUpcoming is an upcoming show whose raw premieres in 10 days, sub in 12 and dub in 40.
func frierenUpcoming() animeschedule.UpcomingShow {
	return animeschedule.UpcomingShow{
		Name: "Frieren", Route: "frieren",
		Premiere:    premiereIn(10 * 24 * time.Hour),
		SubPremiere: premiereIn(12 * 24 * time.Hour),
		DubPremiere: premiereIn(40 * 24 * time.Hour),
	}
}

func addWithTrack(t *testing.T, title, track string) string {
	t.Helper()
	st := newFakeStore()
	useStore(t, st)
	return reply(t, invoke(t, "add", append(animeOption(title), trackOption(track))...)).Embeds[0].Description
}

// suggestionsWithTrack asks for add suggestions with the track option already chosen.
func suggestionsWithTrack(t *testing.T, typed, track string) []string {
	t.Helper()
	old := storeOf
	st := newFakeStore()
	storeOf = func(*database.Client) subsStore { return st }
	t.Cleanup(func() { storeOf = old })
	sub := []*discordgo.ApplicationCommandInteractionDataOption{
		{Name: "anime", Type: discordgo.ApplicationCommandOptionString, Value: typed, Focused: true},
		trackOption(track),
	}
	i := &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{Type: discordgo.InteractionApplicationCommandAutocomplete,
		User: &discordgo.User{ID: "user"},
		Data: discordgo.ApplicationCommandInteractionData{Name: "subscription", Options: []*discordgo.ApplicationCommandInteractionDataOption{
			{Name: "add", Type: discordgo.ApplicationCommandOptionSubCommand, Options: sub}}}}}
	return names(subcommand(t, "add").AutocompleteOption(t.Context(), nil, nil, i, "anime", typed))
}

func TestAddSuggestionsWithoutATrackKeepRawsPremiereLabel(t *testing.T) {
	seedUpcoming(t, []animeschedule.UpcomingShow{frierenUpcoming()})
	got := strings.Join(names(suggestions(t, newFakeStore(), "user", "")), "|")
	if want := "Frieren · premieres " + premiereIn(10*24*time.Hour).Format("2 Jan"); got != want {
		t.Errorf("names = %q, want %q", got, want)
	}
}

func TestAddSuggestionsWithATrackLabelTheSetsEarliestKnownPremiere(t *testing.T) {
	seedUpcoming(t, []animeschedule.UpcomingShow{
		frierenUpcoming(),
		{Name: "Dubless", Route: "dubless", Premiere: premiereIn(5 * 24 * time.Hour)},
	})
	day := func(d time.Duration) string { return premiereIn(d).Format("2 Jan") }
	for _, tc := range []struct{ track, want string }{
		{"dub", "Frieren · premieres " + day(40*24*time.Hour) + "|Dubless · premiere date TBA"},
		{"sub", "Frieren · premieres " + day(12*24*time.Hour) + "|Dubless · premieres " + day(5*24*time.Hour)},
		{"raw", "Frieren · premieres " + day(10*24*time.Hour) + "|Dubless · premieres " + day(5*24*time.Hour)},
		{"sub_dub", "Frieren · premieres " + day(12*24*time.Hour) + "|Dubless · premiere date TBA"},
		{"every", "Frieren · premieres " + day(10*24*time.Hour) + "|Dubless · premieres " + day(5*24*time.Hour)},
	} {
		if got := strings.Join(suggestionsWithTrack(t, "", tc.track), "|"); got != tc.want {
			t.Errorf("track %q: names = %q\nwant %q", tc.track, got, tc.want)
		}
	}
}

func TestAddConfirmationNamesTheChosenTracksPremiereAndLabelsItWhenTheSetHasSeveral(t *testing.T) {
	seedUpcoming(t, []animeschedule.UpcomingShow{frierenUpcoming()})
	lines := func(label string, d time.Duration) string {
		return fmt.Sprintf("It%s premieres %s. You'll get a DM when episode 1 airs.", label, fullAndRelative(premiereIn(d).Unix()))
	}
	for _, tc := range []struct{ track, want string }{
		{"dub", "Subscribed to **Frieren** (Dub).\n" + lines("", 40*24*time.Hour)},
		{"sub", "Subscribed to **Frieren**.\n" + lines("", 12*24*time.Hour)},
		{"sub_dub", "Subscribed to **Frieren** (Sub and Dub).\n" + lines("s sub", 12*24*time.Hour)},
		{"every", "Subscribed to **Frieren** (Every track).\n" + lines("s raw", 10*24*time.Hour)},
	} {
		if got := addWithTrack(t, "Frieren", tc.track); got != tc.want {
			t.Errorf("track %q:\n got %q\nwant %q", tc.track, got, tc.want)
		}
	}
}

func TestAddConfirmationNamesTheDubForASubAndDubSubscriptionWhoseDubPremieresFirst(t *testing.T) {
	u := frierenUpcoming()
	u.DubPremiere = premiereIn(3 * 24 * time.Hour)
	seedUpcoming(t, []animeschedule.UpcomingShow{u})
	want := "Subscribed to **Frieren** (Sub and Dub).\nIts dub premieres " + fullAndRelative(premiereIn(3*24*time.Hour).Unix()) + ". You'll get a DM when episode 1 airs."
	if got := addWithTrack(t, "Frieren", "sub_dub"); got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestAddConfirmationSaysPremiereDateTBAForATrackWithoutOne(t *testing.T) {
	// A dub clock with no raw clock means the dub exists but its premiere cannot be worked out.
	seedUpcoming(t, []animeschedule.UpcomingShow{{Name: "Frieren", Route: "frieren", Premiere: premiereIn(10 * 24 * time.Hour),
		DubTime: time.Date(2026, 1, 1, 15, 0, 0, 0, time.UTC)}})
	want := "Subscribed to **Frieren** (Dub).\nIts premiere date isn't announced yet. You'll get a DM when episode 1 airs."
	if got := addWithTrack(t, "Frieren", "dub"); got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestAddConfirmationForAPlainSubReadsAsTodayWhenTheSubHasNoPremiereOfItsOwn(t *testing.T) {
	seedUpcoming(t, []animeschedule.UpcomingShow{{Name: "Frieren", Route: "frieren", Premiere: premiereIn(10 * 24 * time.Hour)}})
	want := "Subscribed to **Frieren**.\nIt premieres " + fullAndRelative(premiereIn(10*24*time.Hour).Unix()) + ". You'll get a DM when episode 1 airs."
	if got := addReply(t, "Frieren").Embeds[0].Description; got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestListLinesNameTheRightTracksPremiere(t *testing.T) {
	seedUpcoming(t, []animeschedule.UpcomingShow{
		frierenUpcoming(),
		{Name: "Dubless", Route: "dubless", Premiere: premiereIn(5 * 24 * time.Hour)},
		{Name: "Plain", Route: "plain", Premiere: premiereIn(6 * 24 * time.Hour)},
	})
	st := newFakeStore()
	st.docs["user"] = []*anime_subs.ShowSub{
		{Show: "Frieren", Route: "frieren", Tracks: "dub"},
		{Show: "Frieren Both", Route: "frieren", Tracks: "sub_dub"},
		{Show: "Dubless", Route: "dubless", Tracks: "dub"},
		{Show: "Plain", Route: "plain"},
	}
	useStore(t, st)
	got := reply(t, invoke(t, "list")).Embeds[0].Description
	for _, want := range []string{
		fmt.Sprintf("1. Frieren · Dub · premieres <t:%d:R>\n", premiereIn(40*24*time.Hour).Unix()),
		fmt.Sprintf("2. Frieren Both · Sub and Dub · sub premieres <t:%d:R>\n", premiereIn(12*24*time.Hour).Unix()),
		"3. Dubless · Dub · no dub listed yet\n",
		fmt.Sprintf("4. Plain · premieres <t:%d:R>\n", premiereIn(6*24*time.Hour).Unix()),
	} {
		if !strings.Contains(got, want) {
			t.Errorf("list %q lacks line %q", got, want)
		}
	}
}
