package bot

import (
	"context"
	"strings"
	"testing"
	"time"

	"ReZeroTsu/internal/animeschedule"
	"ReZeroTsu/internal/database/anime_subs"
	"ReZeroTsu/internal/database/guilds"
)

// dmEmbeds lists the titles and descriptions of every embed DMed to the member u1, in order.
func dmEmbeds(d *fakeDiscord) (titles, descs []string) {
	for _, m := range d.deliveredTo("dm-u1") {
		for _, e := range m.Msg.Embeds {
			titles = append(titles, e.Title)
			descs = append(descs, e.Description)
		}
	}
	return titles, descs
}

// runMemberDMs runs the DM run for the member u1 at now, reading their document from st as the run does.
func runMemberDMs(t *testing.T, d *fakeDiscord, st *fakeSentState, now time.Time, rows ...animeschedule.ShowEntry) {
	t.Helper()
	doc, err := st.GetAnimeSubs(context.Background(), "u1")
	if err != nil {
		t.Fatal(err)
	}
	if err := processAnimeSubUser(context.Background(), d, newTestSenders(d), st, doc, subRows(rows), now); err != nil {
		t.Fatal(err)
	}
}

func TestEpisodeDMsCallShowsByTheMembersPreferredNameElseTheTitle(t *testing.T) {
	episode := namedRow("Frieren", "Ep 5", -600)
	movie := namedRow("Lagann", "Movie", -600)
	movie.MediaType = animeschedule.MediaMovie
	// A show with no English name is called by its title, never by its romaji or native name.
	bare := airingAt("Bare", "Ep 1", animeschedule.TrackSub, -600)
	bare.Romaji, bare.Native = "Bare Romaji", "Bare Native"
	for _, tc := range []struct {
		stored    string
		titles    []string
		movieLine string
	}{
		{"", []string{"Frieren", "Lagann", "Bare"}, "**Lagann** is out, subbed!"},
		{"title", []string{"Frieren", "Lagann", "Bare"}, "**Lagann** is out, subbed!"},
		{"romaji", []string{"Frieren Romaji", "Lagann Romaji", "Bare Romaji"}, "**Lagann Romaji** is out, subbed!"},
		{"english", []string{"Frieren English", "Lagann English", "Bare"}, "**Lagann English** is out, subbed!"},
		{"native", []string{"Frieren Native", "Lagann Native", "Bare Native"}, "**Lagann Native** is out, subbed!"},
	} {
		t.Run("preferred name "+tc.stored, func(t *testing.T) {
			st := newFakeSentState()
			st.AnimeSubs["u1"] = &anime_subs.AnimeSubs{ID: "u1", PreferredName: tc.stored, Shows: []*anime_subs.ShowSub{
				{Show: "Frieren", Route: "Frieren"}, {Show: "Lagann", Route: "Lagann"}, {Show: "Bare", Route: "Bare"},
			}}
			// A server's preferred name never reaches a member's DMs.
			st.Settings["g1"] = &guilds.GuildSettings{Donghua: true, PreferredName: "native"}
			d := newFakeDiscord()
			runMemberDMs(t, d, st, trackedNow, episode, movie, bare)
			titles, descs := dmEmbeds(d)
			if strings.Join(titles, "|") != strings.Join(tc.titles, "|") {
				t.Errorf("titles = %q, want %q", titles, tc.titles)
			}
			if len(descs) != 3 || descs[0] != "**Ep 5** subbed is out!" || descs[1] != tc.movieLine {
				t.Errorf("descriptions = %q, want the movie's line %q", descs, tc.movieLine)
			}
		})
	}
}

// A member who changes their preferred name between two DM runs changes how names read and nothing else: no episode
// is DMed again and none is skipped.
func TestChangingTheMembersPreferredNameBetweenDMRunsSendsNothingAgainAndSkipsNothing(t *testing.T) {
	aired := namedRow("Frieren", "Ep 5", -600)
	later := namedRow("Apothecary", "Ep 3", 1800)
	st := newFakeSentState()
	st.AnimeSubs["u1"] = &anime_subs.AnimeSubs{ID: "u1", PreferredName: "english", Shows: []*anime_subs.ShowSub{
		{Show: "Frieren", Route: "Frieren"}, {Show: "Apothecary", Route: "Apothecary"},
	}}
	d := newFakeDiscord()
	runMemberDMs(t, d, st, trackedNow, aired, later)
	if titles, _ := dmEmbeds(d); len(titles) != 1 || titles[0] != "Frieren English" {
		t.Fatalf("first run DMed %q, want only Frieren English", titles)
	}

	st.AnimeSubs["u1"].PreferredName = "native"
	afterLater := trackedNow.Add(time.Hour)
	runMemberDMs(t, d, st, afterLater, aired, later)
	runMemberDMs(t, d, st, afterLater, aired, later)
	if titles, _ := dmEmbeds(d); len(titles) != 2 || titles[1] != "Apothecary Native" {
		t.Errorf("DMed %q across the change, want Frieren English once then Apothecary Native once", titles)
	}
}
