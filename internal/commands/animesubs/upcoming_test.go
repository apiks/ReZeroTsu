package animesubs

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"ReZeroTsu/internal/animeschedule"
	"ReZeroTsu/internal/database/anime_subs"
)

// seedUpcoming fixes the clock at nextEpisodeNow, loads the timetable entries and the upcoming list.
func seedUpcoming(t *testing.T, upcoming []animeschedule.UpcomingShow, entries ...animeschedule.ShowEntry) {
	t.Helper()
	seedEntries(t, entries...)
	t.Cleanup(animeschedule.SeedUpcoming(upcoming, true))
}

func premiereIn(d time.Duration) time.Time { return nextEpisodeNow.Add(d) }

func TestAddSuggestionsOfferTimetableShowsThenUpcomingShowsWithTheirPremiere(t *testing.T) {
	seedUpcoming(t, []animeschedule.UpcomingShow{
		{Name: "Dated Show", Route: "dated-show", Premiere: time.Date(2027, 1, 10, 15, 0, 0, 0, time.UTC)},
		{Name: "Undated Show", Route: "undated-show"},
		{Name: "Upcoming Film", Route: "upcoming-film", MediaType: animeschedule.MediaMovie},
	}, entry("Airing Show", "airing-show", "Ep 2", at(time.Hour)))
	got := strings.Join(names(suggestions(t, newFakeStore(), "user", "")), "|")
	want := "Airing Show|Dated Show · premieres 10 Jan|Undated Show · premiere date TBA|Upcoming Film · premiere date TBA · Movie"
	if got != want {
		t.Errorf("names = %q\nwant    %q", got, want)
	}
}

func TestAddSuggestionsMatchUpcomingShowsAnywhereInTheirFourNames(t *testing.T) {
	seedUpcoming(t, []animeschedule.UpcomingShow{{Name: "Kimi", Route: "kimi", Romaji: "Kimi no Na", English: "Your Name Is", Native: "君の名"}})
	for _, typed := range []string{"kimi", "name is", "君"} {
		if got := names(suggestions(t, newFakeStore(), "user", typed)); len(got) != 1 {
			t.Errorf("typed %q: names = %v, want the upcoming show", typed, got)
		}
	}
}

func TestAddSuggestionsOfferAShowOnBothListsOnceAsATimetableShow(t *testing.T) {
	seedUpcoming(t, []animeschedule.UpcomingShow{
		{Name: "Frieren", Route: "frieren", Premiere: premiereIn(24 * time.Hour)},
		{Name: "Untracked", Premiere: premiereIn(24 * time.Hour)},
	}, entry("Renamed Frieren", "frieren", "Ep 2", at(time.Hour)), entry("Untracked", "", "Ep 2", at(time.Hour)))
	got := strings.Join(names(suggestions(t, newFakeStore(), "user", "")), "|")
	if want := "Renamed Frieren|Untracked"; got != want {
		t.Errorf("names = %q, want %q", got, want)
	}
}

func TestAddSuggestionsLabelUpcomingShowsByMediaType(t *testing.T) {
	seedUpcoming(t, []animeschedule.UpcomingShow{
		{Name: "Upcoming Film", Route: "upcoming-film", MediaType: animeschedule.MediaMovie},
		{Name: "Upcoming Tv", Route: "upcoming-tv"},
	})
	if got := strings.Join(names(suggestions(t, newFakeStore(), "user", "")), "|"); got != "Upcoming Film · premiere date TBA · Movie|Upcoming Tv · premiere date TBA" {
		t.Errorf("names = %q", got)
	}
}

func TestAddSuggestionsLabelASubscribedUpcomingShow(t *testing.T) {
	seedUpcoming(t, []animeschedule.UpcomingShow{{Name: "Dated Show", Route: "dated-show", Premiere: time.Date(2027, 1, 10, 0, 0, 0, 0, time.UTC)}})
	st := newFakeStore()
	st.docs["user"] = []*anime_subs.ShowSub{{Show: "Old Name", Route: "dated-show"}}
	if got := strings.Join(names(suggestions(t, st, "user", "")), "|"); got != "Dated Show · premieres 10 Jan · subscribed" {
		t.Errorf("names = %q", got)
	}
}

func TestAddChoiceValueIsTheRouteOrTheTitleWhenThereIsNone(t *testing.T) {
	long := strings.Repeat("y", 150)
	seedUpcoming(t, []animeschedule.UpcomingShow{
		{Name: long, Route: "long-title"},
		{Name: "No Route Upcoming"},
	}, entry("Airing Show", "airing-show", "Ep 2", at(time.Hour)), entry("Plain Title", "", "Ep 1", at(time.Hour)))
	var values []string
	for _, c := range suggestions(t, newFakeStore(), "user", "") {
		v := c.Value.(string)
		if len([]rune(v)) > 100 {
			t.Errorf("value %q is over 100 characters", v)
		}
		values = append(values, v)
	}
	if got, want := strings.Join(values, "|"), "airing-show|Plain Title|long-title|No Route Upcoming"; got != want {
		t.Errorf("values = %q, want %q", got, want)
	}
}

func TestAddResolvesAChoiceValueToItsShowByRoute(t *testing.T) {
	seedUpcoming(t, nil,
		entry("Twin", "twin-a", "Ep 1", at(time.Hour)),
		entry("Twin", "twin-b", "Ep 1", at(2*time.Hour)))
	st := newFakeStore()
	useStore(t, st)
	text(reply(t, invoke(t, "add", animeOption("twin-b")...)))
	if len(st.docs["user"]) != 1 || st.docs["user"][0].Route != "twin-b" {
		t.Errorf("subscriptions = %+v, want the show with route twin-b", st.docs["user"])
	}
}

func TestAddSubscribesToAnUpcomingShowByTypedNameAndStoresItsTitleAndRoute(t *testing.T) {
	seedUpcoming(t, []animeschedule.UpcomingShow{{Name: "Dated Show", Route: "dated-show", English: "The Dated Show", Premiere: premiereIn(10 * 24 * time.Hour)}})
	for _, typed := range []string{"The Dated Show", "dated-show"} {
		st := newFakeStore()
		useStore(t, st)
		got := reply(t, invoke(t, "add", animeOption(typed)...)).Embeds[0].Description
		want := fmt.Sprintf("Subscribed to **Dated Show**.\nIt premieres %s. You'll get a DM when episode 1 airs.", fullAndRelative(premiereIn(10*24*time.Hour).Unix()))
		if got != want {
			t.Errorf("typed %q: description = %q, want %q", typed, got, want)
		}
		if subs := st.docs["user"]; len(subs) != 1 || subs[0].Show != "Dated Show" || subs[0].Route != "dated-show" {
			t.Errorf("typed %q: subscriptions = %+v", typed, subs)
		}
	}
}

func TestUpcomingChineseMovieUsesMovieFormsInRepliesAndListLines(t *testing.T) {
	seedUpcoming(t, []animeschedule.UpcomingShow{
		{Name: "Dated Film", Route: "dated-film", MediaType: animeschedule.MediaMovie, Donghua: true, Premiere: premiereIn(20 * 24 * time.Hour)},
		{Name: "Undated Film", Route: "undated-film", MediaType: animeschedule.MediaMovie, Donghua: true},
	})
	dated := fmt.Sprintf("It releases %s.", fullAndRelative(premiereIn(20*24*time.Hour).Unix()))
	if got := addReply(t, "Dated Film").Embeds[0].Description; !strings.Contains(got, dated) || strings.Contains(got, "episode 1") {
		t.Errorf("dated reply = %q, want %q", got, dated)
	}
	if got := addReply(t, "Undated Film").Embeds[0].Description; !strings.Contains(got, "release date") || strings.Contains(got, "episode 1") {
		t.Errorf("undated reply = %q, want the release-date wording", got)
	}
	st := newFakeStore()
	st.docs["user"] = []*anime_subs.ShowSub{{Show: "Dated Film", Route: "dated-film"}, {Show: "Undated Film", Route: "undated-film"}}
	useStore(t, st)
	got := reply(t, invoke(t, "list")).Embeds[0].Description
	for _, want := range []string{
		fmt.Sprintf("1. Dated Film · Movie · Donghua · releases <t:%d:R>\n", premiereIn(20*24*time.Hour).Unix()),
		"2. Undated Film · Movie · Donghua · release date TBA\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("list %q lacks line %q", got, want)
		}
	}
}

func TestAddSaysAnUpcomingShowHasNoPremiereDateYet(t *testing.T) {
	seedUpcoming(t, []animeschedule.UpcomingShow{{Name: "Undated Show", Route: "undated-show"}})
	got := addReply(t, "Undated Show").Embeds[0].Description
	want := "Subscribed to **Undated Show**.\nIts premiere date isn't announced yet. You'll get a DM when episode 1 airs."
	if got != want {
		t.Errorf("description = %q, want %q", got, want)
	}
}

func TestAddPrefersTheTimetableOverTheUpcomingListForAShowOnBoth(t *testing.T) {
	seedUpcoming(t, []animeschedule.UpcomingShow{{Name: "Frieren", Route: "frieren", Premiere: premiereIn(24 * time.Hour)}},
		entry("Frieren", "frieren", "Ep 6", at(2*24*time.Hour)))
	got := addReply(t, "Frieren").Embeds[0].Description
	if want := "Subscribed to **Frieren**.\nNext episode: Ep 6, " + fullAndRelative(at(2*24*time.Hour)) + "."; got != want {
		t.Errorf("description = %q, want %q", got, want)
	}
}

func TestListLinesShowThePremiereOfUpcomingShows(t *testing.T) {
	seedUpcoming(t, []animeschedule.UpcomingShow{
		{Name: "Dated Show", Route: "dated-show", Premiere: premiereIn(10 * 24 * time.Hour)},
		{Name: "Undated Show", Route: "undated-show"},
		{Name: "Upcoming Film", Route: "upcoming-film", MediaType: animeschedule.MediaMovie, Premiere: premiereIn(20 * 24 * time.Hour)},
	})
	st := newFakeStore()
	st.docs["user"] = []*anime_subs.ShowSub{
		{Show: "Old Dated", Route: "dated-show"},
		{Show: "Undated Show", Route: "undated-show"},
		{Show: "Upcoming Film", Route: "upcoming-film"},
	}
	useStore(t, st)
	got := reply(t, invoke(t, "list")).Embeds[0].Description
	for _, want := range []string{
		fmt.Sprintf("1. Old Dated · premieres <t:%d:R>\n", premiereIn(10*24*time.Hour).Unix()),
		"2. Undated Show · premiere date TBA\n",
		fmt.Sprintf("3. Upcoming Film · Movie · releases <t:%d:R>\n", premiereIn(20*24*time.Hour).Unix()),
	} {
		if !strings.Contains(got, want) {
			t.Errorf("list %q lacks line %q", got, want)
		}
	}
}
