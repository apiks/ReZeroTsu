package bot

import (
	"context"
	"strings"
	"testing"
	"time"

	"ReZeroTsu/internal/animeschedule"
	"ReZeroTsu/internal/database/guilds"
)

// namedRow is a row of trackedRow's show with a romaji, English and native name, all differing from its title.
func namedRow(title, ep string, offset int64) animeschedule.ShowEntry {
	row := airingAt(title, ep, animeschedule.TrackSub, offset)
	row.Romaji, row.English, row.Native = title+" Romaji", title+" English", title+" Native"
	return row
}

// embedTitles lists the titles of every embed posted to the new-episodes channel, in order.
func embedTitles(d *fakeDiscord) []string {
	var out []string
	for _, m := range d.deliveredTo("c1") {
		for _, e := range m.Msg.Embeds {
			out = append(out, e.Title)
		}
	}
	return out
}

func TestDailyScheduleCallsShowsByTheServersPreferredNameElseTheTitle(t *testing.T) {
	for _, tc := range []struct {
		stored, want, absent string
	}{
		{"", "Frieren", "Frieren English"},
		{"english", "Frieren English", "**Frieren**"},
		{"native", "Frieren Native", "**Frieren**"},
	} {
		h := newScheduleHarness()
		t.Cleanup(animeschedule.SetClock(func() time.Time { return h.now }))
		frieren := namedRow("Frieren", "Ep 5", 0)
		frieren.AirTimeUnix = h.now.Unix() + 3600
		// A show with no English name is called by its title, never by its romaji or native name.
		bare := animeschedule.ShowEntry{Name: "Bare", Romaji: "Bare Romaji", Native: "Bare Native", Route: "bare", Episode: "Ep 1", Track: animeschedule.TrackSub, AirTimeUnix: h.now.Unix() + 7200}
		h.fn = func(bool, animeschedule.PreferredTrack) []animeschedule.ShowEntry {
			return []animeschedule.ShowEntry{frieren, bare}
		}
		h.st.Settings = map[string]*guilds.GuildSettings{"g1": {Donghua: true, PreferredName: tc.stored}}
		if err := h.run(context.Background()); err != nil {
			t.Fatal(err)
		}
		got := h.fd.deliveredTo("sch")
		if len(got) != 1 {
			t.Fatalf("stored %q: delivered %d posts, want 1", tc.stored, len(got))
		}
		desc := got[0].Msg.Embeds[0].Description
		if !strings.Contains(desc, "**"+tc.want+"** — ") || strings.Contains(desc, tc.absent) {
			t.Errorf("stored %q: page %q, want Frieren called %q", tc.stored, desc, tc.want)
		}
		wantBare := "**Bare** — "
		if tc.stored == "native" {
			wantBare = "**Bare Native** — "
		}
		if !strings.Contains(desc, wantBare) || (tc.stored != "native" && strings.Contains(desc, "Bare Native")) || strings.Contains(desc, "Bare Romaji") {
			t.Errorf("stored %q: page %q, want the bare show as %q", tc.stored, desc, wantBare)
		}
	}
}

func TestNewEpisodesCallShowsByTheServersPreferredNameInTheTitleAndAMoviesLine(t *testing.T) {
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
		{"english", []string{"Frieren English", "Lagann English", "Bare"}, "**Lagann English** is out, subbed!"},
		{"native", []string{"Frieren Native", "Lagann Native", "Bare Native"}, "**Lagann Native** is out, subbed!"},
	} {
		t.Run("preferred name "+tc.stored, func(t *testing.T) {
			st := trackedState("")
			st.Settings["g1"].PreferredName = tc.stored
			fn := func(bool, animeschedule.PreferredTrack) []animeschedule.ShowEntry {
				return []animeschedule.ShowEntry{episode, movie, bare}
			}
			// Seeded before the rows aired, so they are still due.
			d := newFakeDiscord()
			seedGuildNewEpisodesForShard(context.Background(), st, []string{"g1"}, fn, trackedNow.Add(-time.Hour))
			processGuildNewEpisodes(context.Background(), d, newTestSenders(d), st, "g1", fn, trackedNow)
			if got := embedTitles(d); strings.Join(got, "|") != strings.Join(tc.titles, "|") {
				t.Errorf("titles = %q, want %q", got, tc.titles)
			}
			if got := descriptions(d); len(got) != 3 || got[1] != tc.movieLine || got[0] != "**Ep 5** subbed is out!" {
				t.Errorf("descriptions = %q, want the movie's line %q", got, tc.movieLine)
			}
		})
	}
}

// A change of the server's preferred name between two runs changes how names read and nothing else: no episode is
// posted again and none is skipped.
func TestChangingThePreferredNameBetweenRunsSendsNothingAgainAndSkipsNothing(t *testing.T) {
	aired := namedRow("Frieren", "Ep 5", -600)
	later := namedRow("Apothecary", "Ep 3", 1800)
	fn := func(bool, animeschedule.PreferredTrack) []animeschedule.ShowEntry {
		return []animeschedule.ShowEntry{aired, later}
	}
	st := trackedState("")
	st.Settings["g1"].PreferredName = "english"
	d := newFakeDiscord()
	seedGuildNewEpisodesForShard(context.Background(), st, []string{"g1"}, fn, trackedNow.Add(-time.Hour))
	processGuildNewEpisodes(context.Background(), d, newTestSenders(d), st, "g1", fn, trackedNow)
	if got := embedTitles(d); len(got) != 1 || got[0] != "Frieren English" {
		t.Fatalf("first run posted %q, want only Frieren English", got)
	}

	st.Settings["g1"].PreferredName = "native"
	afterLater := trackedNow.Add(time.Hour)
	seedGuildNewEpisodesForShard(context.Background(), st, []string{"g1"}, fn, afterLater)
	processGuildNewEpisodes(context.Background(), d, newTestSenders(d), st, "g1", fn, afterLater)
	processGuildNewEpisodes(context.Background(), d, newTestSenders(d), st, "g1", fn, afterLater)
	if got := embedTitles(d); len(got) != 2 || got[1] != "Apothecary Native" {
		t.Errorf("posted %q across the change, want Frieren English once then Apothecary Native once", got)
	}
}
