package schedule

import (
	"fmt"
	"strings"
	"testing"

	"ReZeroTsu/internal/animeschedule"
	"ReZeroTsu/internal/discord"

	"github.com/bwmarrin/discordgo"
)

// openDay seeds the timetable, fixes the clock to Wednesday 2026-11-04 12:00 UTC and opens /schedule on day.
func openDay(t *testing.T, day string, days map[string][]animeschedule.ShowEntry) *discordgo.MessageEmbed {
	t.Helper()
	setClock(t)
	t.Cleanup(animeschedule.Seed(days))
	animeschedule.SetAPIKey("key")
	t.Cleanup(func() { animeschedule.SetAPIKey("") })
	calls := runSchedule(t, day).Calls()
	return calls[len(calls)-1].Response.Embeds[0]
}

func TestScheduleTitleCarriesThePagesUTCDateForEveryWeekday(t *testing.T) {
	want := map[string]string{
		"sunday": "Sunday, 8 Nov 2026", "monday": "Monday, 9 Nov 2026", "tuesday": "Tuesday, 10 Nov 2026",
		"wednesday": "Wednesday, 4 Nov 2026", "thursday": "Thursday, 5 Nov 2026", "friday": "Friday, 6 Nov 2026",
		"saturday": "Saturday, 7 Nov 2026",
	}
	for day, title := range want {
		t.Run(day, func(t *testing.T) {
			emb := openDay(t, day, map[string][]animeschedule.ShowEntry{"2026-11-04": {{Name: "x", AirTimeUnix: 1}}})
			if emb.Title != title {
				t.Errorf("title = %q, want %q", emb.Title, title)
			}
		})
	}
}

func TestScheduleFooterCountsShowsAndExplainsTimeZones(t *testing.T) {
	entry := animeschedule.ShowEntry{Name: "x", AirTimeUnix: 1700000000, AirType: "sub"}
	cases := map[int]string{0: "0 shows", 1: "1 show", 12: "12 shows"}
	for n, count := range cases {
		t.Run(count, func(t *testing.T) {
			shows := make([]animeschedule.ShowEntry, n)
			for i := range shows {
				shows[i] = entry
			}
			emb := openDay(t, "wednesday", map[string][]animeschedule.ShowEntry{"2026-11-04": shows})
			want := count + " · days run on UTC, times show in your time zone"
			if emb.Footer.Text != want {
				t.Errorf("footer = %q, want %q", emb.Footer.Text, want)
			}
			if emb.Author == nil || emb.Author.Name != "AnimeSchedule.net" {
				t.Errorf("author = %+v, want AnimeSchedule.net kept in the author line", emb.Author)
			}
		})
	}
}

func TestScheduleRelativeTimeOnlyOnTodaysPage(t *testing.T) {
	days := map[string][]animeschedule.ShowEntry{
		"2026-11-04": {{Name: "Today Show", Episode: "Episode 1", AirType: "sub", AirTimeUnix: 1700000000}},
		"2026-11-05": {{Name: "Tomorrow Show", Episode: "Episode 1", AirType: "sub", AirTimeUnix: 1700000000}},
	}
	today := openDay(t, "wednesday", days)
	if !strings.Contains(today.Description, "<t:1700000000:t>") || !strings.Contains(today.Description, "<t:1700000000:R>") {
		t.Errorf("today's page = %q, want short-time and relative timestamps", today.Description)
	}
	other := openDay(t, "thursday", days)
	if !strings.Contains(other.Description, "<t:1700000000:t>") || strings.Contains(other.Description, ":R>") {
		t.Errorf("other day's page = %q, want the short-time only", other.Description)
	}
}

func TestScheduleLabelsNonTVEntriesOnly(t *testing.T) {
	mk := func(name string, mt animeschedule.MediaType) animeschedule.ShowEntry {
		return animeschedule.ShowEntry{Name: name, Episode: "E", AirType: "sub", AirTimeUnix: 1700000000, MediaType: mt}
	}
	emb := openDay(t, "thursday", map[string][]animeschedule.ShowEntry{"2026-11-05": {
		mk("A-tv", animeschedule.MediaTV), mk("B-short", animeschedule.MediaTVShort), mk("C-ona", animeschedule.MediaONA),
		mk("D-donghua", animeschedule.MediaDonghua), mk("E-ova", animeschedule.MediaOVA),
		mk("F-special", animeschedule.MediaSpecial), mk("G-movie", animeschedule.MediaMovie),
	}})
	want := map[string]string{"A-tv": "", "B-short": "TV Short", "C-ona": "ONA", "D-donghua": "Donghua", "E-ova": "OVA", "F-special": "Special", "G-movie": "Movie"}
	lines := strings.Split(emb.Description, "\n")
	for _, l := range lines {
		for name, label := range want {
			if !strings.Contains(l, "**"+name+"**") {
				continue
			}
			if label == "" && strings.Count(l, "·") != 1 {
				t.Errorf("TV line %q should carry no label", l)
			}
			if label != "" && !strings.HasSuffix(l, " · "+label) {
				t.Errorf("line %q should end with %q", l, " · "+label)
			}
		}
	}
}

func TestScheduleOverlongPageIsCutAtAWholeLineWithMoreLink(t *testing.T) {
	shows := make([]animeschedule.ShowEntry, 200)
	for i := range shows {
		shows[i] = animeschedule.ShowEntry{Name: fmt.Sprintf("Show number %03d with a fairly long title", i), Episode: "Episode 1", AirType: "sub", AirTimeUnix: 1700000000}
	}
	emb := openDay(t, "thursday", map[string][]animeschedule.ShowEntry{"2026-11-05": shows})
	if len(emb.Description) > discord.MaxEmbedDescriptionLength {
		t.Fatalf("description is %d bytes, over the limit", len(emb.Description))
	}
	lines := strings.Split(emb.Description, "\n")
	last := lines[len(lines)-1]
	kept := len(lines) - 1
	want := fmt.Sprintf("[… and %d more on AnimeSchedule.net](%s)", len(shows)-kept, animeschedule.BaseURL)
	if last != want {
		t.Errorf("last line = %q, want %q", last, want)
	}
	for _, l := range lines[:kept] {
		if !strings.HasPrefix(l, "**Show number ") || !strings.Contains(l, "<t:1700000000:t>") {
			t.Errorf("kept line %q is not a whole entry", l)
		}
	}
	if kept < 10 || kept >= len(shows) {
		t.Errorf("kept %d lines, want a real cut", kept)
	}
}

func TestScheduleDonghuaSettingStillHidesDonghua(t *testing.T) {
	setClock(t)
	t.Cleanup(animeschedule.Seed(map[string][]animeschedule.ShowEntry{"2026-11-04": {
		{Name: "Soul Land", AirTimeUnix: 1, Donghua: true, MediaType: animeschedule.MediaDonghua},
		{Name: "Frieren", AirTimeUnix: 1},
	}}))
	shows := animeschedule.GetDayShows(animeschedule.Today(), false)
	emb := BuildScheduleEmbed(animeschedule.Today(), shows)
	if strings.Contains(emb.Description, "Soul Land") || !strings.Contains(emb.Description, "Frieren") {
		t.Errorf("description = %q, want donghua hidden", emb.Description)
	}
}
