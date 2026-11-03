package bot

import (
	"context"
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
		{"tv sub", animeschedule.ShowEntry{Name: "Show", Episode: "Ep 5", AirType: "sub"}, "**Ep 5** subbed is out!"},
		{"tv raw", animeschedule.ShowEntry{Name: "Show", Episode: "Ep 5", AirType: "raw"}, "**Ep 5** raw is out!"},
		{"ova keeps episode wording", animeschedule.ShowEntry{Name: "Show", Episode: "Ep 1F", AirType: "sub", MediaType: animeschedule.MediaOVA}, "**Ep 1F** subbed is out!"},
		{"movie sub", animeschedule.ShowEntry{Name: "Film", Episode: "Ep 1F", AirType: "sub", MediaType: animeschedule.MediaMovie}, "**Film** is out, subbed!"},
		{"movie raw", animeschedule.ShowEntry{Name: "Film", Episode: "Ep 1F", AirType: "raw", MediaType: animeschedule.MediaMovie}, "**Film** is out, raw!"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := buildEpisodeEmbed(tc.show).Description; got != tc.want {
				t.Errorf("description = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestGuildNewEpisodesMovieWording(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	shows := []animeschedule.ShowEntry{{Name: "Film", Route: "film", Episode: "Ep 1F", AirTimeUnix: now.Unix() - 600, AirType: "sub", MediaType: animeschedule.MediaMovie}}
	st := newFakeSentState()
	st.Autoposts[apKey("g1", postTypeNewEpisodes)] = &guilds.Autopost{PostType: postTypeNewEpisodes, ID: "c1"}
	st.AnimeSubs["c1"] = &anime_subs.AnimeSubs{ID: "c1", IsGuild: true, Shows: []*anime_subs.ShowSub{{Show: "Film", Guild: true}}}
	d := newFakeDiscord()
	processGuildNewEpisodes(context.Background(), d, newTestSenders(d), st, "g1", func(bool) []animeschedule.ShowEntry { return shows }, now)
	got := d.deliveredTo("c1")
	if len(got) != 1 || len(got[0].Msg.Embeds) != 1 {
		t.Fatalf("delivered = %+v, want one message with one embed", got)
	}
	if desc := got[0].Msg.Embeds[0].Description; desc != "**Film** is out, subbed!" {
		t.Errorf("description = %q", desc)
	}
}
