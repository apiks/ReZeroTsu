package schedule

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf16"

	"ReZeroTsu/internal/animeschedule"
	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/database/guilds"
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
	entry := animeschedule.ShowEntry{Name: "x", AirTimeUnix: 1700000000, Track: "sub"}
	cases := map[int]string{0: "0 shows", 1: "1 show", 12: "12 shows"}
	for n, count := range cases {
		t.Run(count, func(t *testing.T) {
			shows := make([]animeschedule.ShowEntry, n)
			for i := range shows {
				shows[i] = entry
				shows[i].Name = fmt.Sprintf("show %d", i)
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
		"2026-11-04": {{Name: "Today Show", Episode: "Episode 1", Track: "sub", AirTimeUnix: 1700000000}},
		"2026-11-05": {{Name: "Tomorrow Show", Episode: "Episode 1", Track: "sub", AirTimeUnix: 1700000000}},
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
		return animeschedule.ShowEntry{Name: name, Episode: "E", Track: "sub", AirTimeUnix: 1700000000, MediaType: mt}
	}
	cn := func(name string, mt animeschedule.MediaType) animeschedule.ShowEntry {
		e := mk(name, mt)
		e.Donghua = true
		return e
	}
	emb := openDay(t, "thursday", map[string][]animeschedule.ShowEntry{"2026-11-05": {
		mk("A-tv", animeschedule.MediaTV), mk("B-short", animeschedule.MediaTVShort), mk("C-ona", animeschedule.MediaONA),
		mk("E-ova", animeschedule.MediaOVA),
		mk("F-special", animeschedule.MediaSpecial), mk("G-movie", animeschedule.MediaMovie),
		cn("H-cn-tv", animeschedule.MediaTV), cn("I-cn-short", animeschedule.MediaTVShort), cn("J-cn-ona", animeschedule.MediaONA),
		cn("K-cn-ova", animeschedule.MediaOVA), cn("L-cn-special", animeschedule.MediaSpecial), cn("M-cn-movie", animeschedule.MediaMovie),
	}})
	want := map[string]string{"A-tv": "", "B-short": "TV Short", "C-ona": "ONA", "E-ova": "OVA", "F-special": "Special", "G-movie": "Movie",
		"H-cn-tv": "Donghua", "I-cn-short": "TV Short · Donghua", "J-cn-ona": "ONA · Donghua", "K-cn-ova": "OVA · Donghua",
		"L-cn-special": "Special · Donghua", "M-cn-movie": "Movie · Donghua"}
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
		shows[i] = animeschedule.ShowEntry{Name: fmt.Sprintf("Show number %03d with a fairly long title", i), Episode: "Episode 1", Track: "sub", AirTimeUnix: 1700000000}
	}
	emb := openDay(t, "thursday", map[string][]animeschedule.ShowEntry{"2026-11-05": shows})
	if u16(emb.Description) > discord.MaxEmbedDescriptionLength {
		t.Fatalf("description is %d UTF-16 units, over the limit", u16(emb.Description))
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

// u16 is the length Discord counts: UTF-16 code units.
func u16(s string) int { return len(utf16.Encode([]rune(s))) }

func TestScheduleOverlongPageOfMultiByteTitlesCutsByCharactersNotBytes(t *testing.T) {
	shows := make([]animeschedule.ShowEntry, 300)
	lines := make([]string, len(shows))
	for i := range shows {
		shows[i] = animeschedule.ShowEntry{Name: fmt.Sprintf("魔法少女まどか☆マギカ 第%03d話 — 特別編", i), Episode: "Episode 1", Track: "sub", AirTimeUnix: 1700000000}
		lines[i] = scheduleLine(shows[i], false)
	}
	emb := openDay(t, "thursday", map[string][]animeschedule.ShowEntry{"2026-11-05": shows})
	if n := u16(emb.Description); n > discord.MaxEmbedDescriptionLength {
		t.Fatalf("description is %d UTF-16 units, over the limit", n)
	}
	got := strings.Split(emb.Description, "\n")
	kept := len(got) - 1
	// No whole line that fits was dropped: keeping one more would break the limit.
	more := fmt.Sprintf("[… and %d more on AnimeSchedule.net](%s)", len(shows)-kept-1, animeschedule.BaseURL)
	if next := u16(strings.Join(lines[:kept+1], "\n") + "\n" + more); next <= discord.MaxEmbedDescriptionLength {
		t.Errorf("kept %d lines but %d UTF-16 units would still fit with one more", kept, next)
	}
}

func TestScheduleDescriptionWhenNotEvenTheFirstLineFitsHasNoLeadingNewline(t *testing.T) {
	got := scheduleDescription([]string{strings.Repeat("あ", 5000), "second"})
	if strings.HasPrefix(got, "\n") || !strings.HasPrefix(got, "[… and 2 more") {
		t.Errorf("description = %q, want only the linked count", got)
	}
	if u16(got) > discord.MaxEmbedDescriptionLength {
		t.Errorf("description is %d units", u16(got))
	}
}

func TestScheduleEmptyDayTextIsTrueWhereThereAreNoButtons(t *testing.T) {
	emb := BuildScheduleEmbed(mustDate("2026-11-05"), mustDate("2026-11-04"), nil, animeschedule.PreferSub)
	if strings.Contains(emb.Description, "buttons") || !strings.Contains(emb.Description, "/schedule") {
		t.Errorf("description = %q, want a pointer to /schedule instead of the buttons", emb.Description)
	}
}

// inServer runs the body with the server's settings lookup replaced and reports how often it was read.
func inServer(t *testing.T, donghua bool, lookupErr error) *int {
	t.Helper()
	reads := 0
	prev := guildSettings
	guildSettings = func(context.Context, *database.Client, string) (*guilds.GuildSettings, error) {
		reads++
		if lookupErr != nil {
			return nil, lookupErr
		}
		return &guilds.GuildSettings{Donghua: donghua}, nil
	}
	t.Cleanup(func() { guildSettings = prev })
	return &reads
}

func TestScheduleDonghuaSettingHidesDonghuaOnTheCommandAndOnPagePresses(t *testing.T) {
	soulLand := animeschedule.ShowEntry{Name: "Soul Land", AirTimeUnix: 1, Donghua: true, MediaType: animeschedule.MediaONA}
	frieren := animeschedule.ShowEntry{Name: "Frieren", AirTimeUnix: 1}
	for _, tc := range []struct {
		name       string
		donghua    bool
		lookupErr  error
		wantHidden bool
	}{
		{"server hides donghua", false, nil, true},
		{"server shows donghua", true, nil, false},
		{"unreadable settings show donghua", false, errors.New("boom"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seedWeeks(t)
			t.Cleanup(animeschedule.Seed(map[string][]animeschedule.ShowEntry{"2026-11-04": {soulLand, frieren}, "2026-11-05": {soulLand, frieren}}))
			reads := inServer(t, tc.donghua, tc.lookupErr)

			calls := runScheduleIn(t, "wednesday", "guild").Calls()
			check := func(where string, emb *discordgo.MessageEmbed) {
				t.Helper()
				if !strings.Contains(emb.Description, "Frieren") || strings.Contains(emb.Description, "Soul Land") != !tc.wantHidden {
					t.Errorf("%s: description = %q, want donghua hidden=%v", where, emb.Description, tc.wantHidden)
				}
			}
			check("/schedule", calls[len(calls)-1].Response.Embeds[0])
			if *reads != 1 {
				t.Errorf("/schedule read the server setting %d times, want 1", *reads)
			}

			press := pressNavIn(t, "sched:2026-11-05:user:sub", "user", "guild", publicMsg())
			check("page press", press[0].Response.Embeds[0])
			if *reads != 2 {
				t.Errorf("page press read the server setting %d times in total, want 2", *reads)
			}
		})
	}
}
