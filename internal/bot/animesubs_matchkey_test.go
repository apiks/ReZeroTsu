package bot

import (
	"context"
	"testing"
	"time"

	"ReZeroTsu/internal/animeschedule"
	"ReZeroTsu/internal/database/anime_subs"
	"ReZeroTsu/internal/database/guilds"
)

func TestGuildShowNamesMatchUsesMatchKey(t *testing.T) {
	day := func(names ...string) []animeschedule.ShowEntry {
		var out []animeschedule.ShowEntry
		for _, n := range names {
			out = append(out, animeschedule.ShowEntry{Name: n})
		}
		return out
	}
	sub := func(names ...string) []*anime_subs.ShowSub {
		var out []*anime_subs.ShowSub
		for _, n := range names {
			out = append(out, &anime_subs.ShowSub{Show: n})
		}
		return out
	}
	tests := []struct {
		name          string
		day           []animeschedule.ShowEntry
		subscriptions []*anime_subs.ShowSub
		want          bool
	}{
		{"surrounding ASCII space on the schedule name", day("Frieren "), sub("Frieren"), true},
		{"surrounding ASCII space on the stored name", day("Frieren"), sub(" frieren "), true},
		{"different names", day("Frieren"), sub("Dandadan"), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := guildSentMatches(tc.day, tc.subscriptions, tc.day); got != tc.want {
				t.Errorf("guildSentMatches = %v, want %v", got, tc.want)
			}
		})
	}
}

// The same-day seeder must not append a second entry for a name that differs from a stored one only by trim.
func TestSeedDoesNotDuplicateTrimVariantName(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	today := now.Format("2006-01-02")
	list := []animeschedule.ShowEntry{
		{Name: "Aired One ", AirTimeUnix: now.Unix() + 3600},
		{Name: "Late Listed", AirTimeUnix: now.Unix() + 3600},
	}
	st := newFakeSentState()
	st.Autoposts[apKey("g1", postTypeNewEpisodes)] = &guilds.Autopost{PostType: postTypeNewEpisodes, ID: "c1"}
	st.AnimeSubs["c1"] = &anime_subs.AnimeSubs{ID: "c1", IsGuild: true, LastSeedDate: today, Shows: []*anime_subs.ShowSub{
		{Show: "Aired One", Guild: true},
	}}
	seedGuildNewEpisodesForShard(context.Background(), st, []string{"g1"}, func(bool, animeschedule.PreferredTrack) []animeschedule.ShowEntry { return list }, now)
	doc, _ := st.GetAnimeSubs(context.Background(), "c1")
	if len(doc.Shows) != 2 {
		t.Errorf("shows = %d, want 2 (Aired One, Late Listed)", len(doc.Shows))
	}
}
