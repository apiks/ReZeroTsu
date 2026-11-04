package bot

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"ReZeroTsu/internal/animeschedule"
	"ReZeroTsu/internal/database/anime_subs"
	"ReZeroTsu/internal/database/guilds"
)

func TestEpisodeEmbedWording(t *testing.T) {
	tests := []struct {
		name string
		show animeschedule.ShowEntry
		want string
	}{
		{"tv sub", animeschedule.ShowEntry{Name: "Show", Episode: "Ep 5", Track: "sub"}, "**Ep 5** subbed is out!"},
		{"tv dub", animeschedule.ShowEntry{Name: "Show", Episode: "Ep 5", Track: "dub"}, "**Ep 5** dubbed is out!"},
		{"movie dub", animeschedule.ShowEntry{Name: "Film", Episode: "Ep 1F", Track: "dub", MediaType: animeschedule.MediaMovie}, "**Film** is out, dubbed!"},
		{"span dub", animeschedule.ShowEntry{Name: "Show", Episode: "Ep 1-12F", Track: "dub"}, "**Ep 1-12F** dubbed is out!"},
		{"span sub", animeschedule.ShowEntry{Name: "Show", Episode: "Ep 1-12", Track: "sub"}, "**Ep 1-12** subbed is out!"},
		{"span raw", animeschedule.ShowEntry{Name: "Show", Episode: "Ep 1-12", Track: "raw"}, "**Ep 1-12** raw is out!"},
		{"tv raw", animeschedule.ShowEntry{Name: "Show", Episode: "Ep 5", Track: "raw"}, "**Ep 5** raw is out!"},
		{"ova keeps episode wording", animeschedule.ShowEntry{Name: "Show", Episode: "Ep 1F", Track: "sub", MediaType: animeschedule.MediaOVA}, "**Ep 1F** subbed is out!"},
		{"movie sub", animeschedule.ShowEntry{Name: "Film", Episode: "Ep 1F", Track: "sub", MediaType: animeschedule.MediaMovie}, "**Film** is out, subbed!"},
		{"donghua movie sub", animeschedule.ShowEntry{Name: "Film", Episode: "Ep 1F", Track: "sub", MediaType: animeschedule.MediaMovie, Donghua: true}, "**Film** is out, subbed!"},
		{"donghua movie dub", animeschedule.ShowEntry{Name: "Film", Episode: "Ep 1F", Track: "dub", MediaType: animeschedule.MediaMovie, Donghua: true}, "**Film** is out, dubbed!"},
		{"donghua movie raw", animeschedule.ShowEntry{Name: "Film", Episode: "Ep 1F", Track: "raw", MediaType: animeschedule.MediaMovie, Donghua: true}, "**Film** is out, raw!"},
		{"movie raw", animeschedule.ShowEntry{Name: "Film", Episode: "Ep 1F", Track: "raw", MediaType: animeschedule.MediaMovie}, "**Film** is out, raw!"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := buildEpisodeEmbed(tc.show, animeschedule.PreferTitle).Description; got != tc.want {
				t.Errorf("description = %q, want %q", got, tc.want)
			}
		})
	}
}

// A donghua movie row, decoded from the timetable, gets movie wording on every track.
func TestDonghuaMovieTimetableRowDecodesToMovieAlertWording(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	var rows []animeschedule.TimetableAnime
	for _, track := range []string{"sub", "dub", "raw"} {
		var row animeschedule.TimetableAnime
		raw := `{"title":"Film","route":"film","airType":"` + track + `","episodeNumber":1,"episodes":1,"episodeDate":"2026-11-04T15:00:00Z","donghua":true,"mediaTypes":[{"name":"Movie (Chinese)","route":"movie-chinese"}]}`
		if err := json.Unmarshal([]byte(raw), &row); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, row)
	}
	t.Cleanup(animeschedule.SetClock(func() time.Time { return now }))
	t.Cleanup(animeschedule.SeedTimetable(rows))
	for pref, want := range map[animeschedule.PreferredTrack]string{
		animeschedule.PreferSub: "**Film** is out, subbed!",
		animeschedule.PreferDub: "**Film** is out, dubbed!",
		animeschedule.PreferRaw: "**Film** is out, raw!",
	} {
		shows := animeschedule.GetDayShows(now, true, pref)
		if len(shows) != 1 {
			t.Fatalf("%v: shows = %+v, want one", pref, shows)
		}
		if got := buildEpisodeEmbed(shows[0], animeschedule.PreferTitle).Description; got != want {
			t.Errorf("%v: description = %q, want %q", pref, got, want)
		}
	}
}

func TestGuildNewEpisodesMovieWording(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	shows := []animeschedule.ShowEntry{{Name: "Film", Route: "film", Episode: "Ep 1F", AirTimeUnix: now.Unix() - 600, Track: "sub", MediaType: animeschedule.MediaMovie}}
	st := newFakeSentState()
	st.Autoposts[apKey("g1", postTypeNewEpisodes)] = &guilds.Autopost{PostType: postTypeNewEpisodes, ID: "c1"}
	st.AnimeSubs["c1"] = &anime_subs.AnimeSubs{ID: "c1", IsGuild: true, Shows: []*anime_subs.ShowSub{{Show: "Film", Guild: true}}}
	d := newFakeDiscord()
	processGuildNewEpisodes(context.Background(), d, newTestSenders(d), st, "g1", func(bool, animeschedule.PreferredTrack) []animeschedule.ShowEntry { return shows }, now)
	got := d.deliveredTo("c1")
	if len(got) != 1 || len(got[0].Msg.Embeds) != 1 {
		t.Fatalf("delivered = %+v, want one message with one embed", got)
	}
	if desc := got[0].Msg.Embeds[0].Description; desc != "**Film** is out, subbed!" {
		t.Errorf("description = %q", desc)
	}
}
