package schedule

import (
	"strings"
	"testing"

	"ReZeroTsu/internal/animeschedule"
)

func TestScheduleLineShowsAnEpisodeSpan(t *testing.T) {
	setClock(t)
	t.Cleanup(animeschedule.Seed(map[string][]animeschedule.ShowEntry{
		"2026-11-04": {{Name: "Frieren", Route: "frieren", Episode: "Ep 1-12F", Track: animeschedule.TrackDub, AirTimeUnix: 1700000000}},
	}))
	animeschedule.SetAPIKey("key")
	t.Cleanup(func() { animeschedule.SetAPIKey("") })

	emb := runWithTrack(t, "wednesday", "dub").Embeds[0]
	if !strings.Contains(emb.Description, "Frieren") || !strings.Contains(emb.Description, "Ep 1-12F DUB") {
		t.Errorf("description %q lacks the span line", emb.Description)
	}
}
