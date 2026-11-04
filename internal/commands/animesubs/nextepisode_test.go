package animesubs

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"ReZeroTsu/internal/animeschedule"
	"ReZeroTsu/internal/database/anime_subs"
	"ReZeroTsu/internal/interaction"
)

const dmFooter = "Episode alerts arrive by DM. Keep DMs from ZeroTsu open."

var nextEpisodeNow = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

// seedEntries fixes the clock at nextEpisodeNow and loads the entries on their UTC dates.
func seedEntries(t *testing.T, entries ...animeschedule.ShowEntry) {
	t.Helper()
	t.Cleanup(animeschedule.SetClock(func() time.Time { return nextEpisodeNow }))
	days := map[string][]animeschedule.ShowEntry{}
	for _, e := range entries {
		k := animeschedule.DateKey(time.Unix(e.AirTimeUnix, 0))
		days[k] = append(days[k], e)
	}
	t.Cleanup(animeschedule.Seed(days))
}

func at(d time.Duration) int64 { return nextEpisodeNow.Add(d).Unix() }

func entry(name, route, ep string, air int64) animeschedule.ShowEntry {
	return animeschedule.ShowEntry{Name: name, Route: route, Episode: ep, AirTimeUnix: air}
}

func fullAndRelative(unix int64) string { return fmt.Sprintf("<t:%d:F> (<t:%d:R>)", unix, unix) }

func addReply(t *testing.T, title string) interaction.Response {
	t.Helper()
	st := newFakeStore()
	useStore(t, st)
	return reply(t, invoke(t, "add", animeOption(title)...))
}

func TestAddNamesTheEarliestEpisodeAtOrAfterNow(t *testing.T) {
	seedEntries(t,
		entry("Frieren", "frieren", "Ep 5", at(-2*time.Hour)),
		entry("Frieren", "frieren", "Ep 7", at(9*24*time.Hour)),
		entry("Frieren", "frieren", "Ep 6", at(2*24*time.Hour)))
	got := addReply(t, "Frieren").Embeds[0].Description
	want := "Subscribed to **Frieren**.\nNext episode: Ep 6, " + fullAndRelative(at(2*24*time.Hour)) + "."
	if got != want {
		t.Errorf("description = %q, want %q", got, want)
	}
}

func TestAddSaysTheLatestEpisodeAiredWhenNothingIsUpcoming(t *testing.T) {
	seedEntries(t,
		entry("Frieren", "frieren", "Ep 4", at(-9*24*time.Hour)),
		entry("Frieren", "frieren", "Ep 5", at(-2*time.Hour)))
	got := addReply(t, "Frieren").Embeds[0].Description
	want := fmt.Sprintf("Subscribed to **Frieren**.\nEp 5 aired <t:%d:R>. You'll get a DM when the next one airs.", at(-2*time.Hour))
	if got != want {
		t.Errorf("description = %q, want %q", got, want)
	}
}

func TestAddSaysWhenAMovieReleases(t *testing.T) {
	m := entry("Big Film", "big-film", "Ep 1", at(10*24*time.Hour))
	m.MediaType = animeschedule.MediaMovie
	seedEntries(t, m)
	got := addReply(t, "Big Film").Embeds[0].Description
	want := "Subscribed to **Big Film**.\nIt releases " + fullAndRelative(at(10*24*time.Hour)) + "."
	if got != want {
		t.Errorf("description = %q, want %q", got, want)
	}
}

func TestAddSaysWhenAChineseMovieReleases(t *testing.T) {
	m := entry("Big Film", "big-film", "Ep 1", at(10*24*time.Hour))
	m.MediaType, m.Donghua = animeschedule.MediaMovie, true
	seedEntries(t, m)
	got := addReply(t, "Big Film").Embeds[0].Description
	want := "Subscribed to **Big Film**.\nIt releases " + fullAndRelative(at(10*24*time.Hour)) + "."
	if got != want {
		t.Errorf("description = %q, want %q", got, want)
	}
}

func TestListLinesCarryTheTypeAndDonghuaLabels(t *testing.T) {
	cn := func(name, route string, mt animeschedule.MediaType) animeschedule.ShowEntry {
		e := entry(name, route, "Ep 1", at(10*24*time.Hour))
		e.MediaType, e.Donghua = mt, true
		return e
	}
	seedEntries(t,
		cn("Film", "film", animeschedule.MediaMovie),
		cn("Short", "short", animeschedule.MediaTVShort),
		cn("Video", "video", animeschedule.MediaOVA),
		cn("Extra", "extra", animeschedule.MediaSpecial),
		cn("Series", "series", animeschedule.MediaTV),
		cn("Web", "web", animeschedule.MediaONA))
	st := newFakeStore()
	st.docs["user"] = []*anime_subs.ShowSub{
		{Show: "Film", Route: "film"}, {Show: "Short", Route: "short"}, {Show: "Video", Route: "video"},
		{Show: "Extra", Route: "extra"}, {Show: "Series", Route: "series"}, {Show: "Web", Route: "web"},
	}
	useStore(t, st)
	got := reply(t, invoke(t, "list")).Embeds[0].Description
	for _, want := range []string{
		fmt.Sprintf("1. Film · Movie · Donghua · releases <t:%d:R>\n", at(10*24*time.Hour)),
		"2. Short · TV Short · Donghua · ",
		"3. Video · OVA · Donghua · ",
		"4. Extra · Special · Donghua · ",
		"5. Series · Donghua · ",
		"6. Web · ONA · Donghua · ",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("list %q lacks line %q", got, want)
		}
	}
}

func TestAddConfirmationAlwaysCarriesTheDMsFooter(t *testing.T) {
	seedEntries(t, entry("Frieren", "frieren", "Ep 6", at(time.Hour)))
	first := addReply(t, "Frieren")
	if first.Embeds[0].Footer == nil || first.Embeds[0].Footer.Text != dmFooter {
		t.Errorf("confirmation footer = %+v, want %q", first.Embeds[0].Footer, dmFooter)
	}
	st := newFakeStore().with("user", "Frieren")
	useStore(t, st)
	again := reply(t, invoke(t, "add", animeOption("Frieren")...))
	if !strings.Contains(text(again), "already subscribed") || again.Embeds[0].Footer == nil || again.Embeds[0].Footer.Text != dmFooter {
		t.Errorf("already-subscribed reply = %+v, want the footer %q", again.Embeds[0], dmFooter)
	}
}

func TestListLinesCarryLabelAndStatus(t *testing.T) {
	movie := entry("Big Film", "big-film", "Ep 1", at(10*24*time.Hour))
	movie.MediaType = animeschedule.MediaMovie
	seedEntries(t,
		entry("Renamed Frieren", "frieren", "Ep 6", at(2*24*time.Hour)),
		entry("Dandadan", "dandadan", "Ep 5", at(-24*time.Hour)),
		movie)
	st := newFakeStore()
	st.docs["user"] = []*anime_subs.ShowSub{
		{Show: "Old Frieren", Route: "frieren"},
		{Show: "Dandadan"},
		{Show: "Big Film", Route: "big-film"},
		{Show: "Mystery"},
	}
	useStore(t, st)
	got := reply(t, invoke(t, "list")).Embeds[0].Description
	for _, want := range []string{
		fmt.Sprintf("1. Old Frieren · Ep 6 <t:%d:R>\n", at(2*24*time.Hour)),
		fmt.Sprintf("2. Dandadan · Ep 5 aired <t:%d:R>\n", at(-24*time.Hour)),
		fmt.Sprintf("3. Big Film · Movie · releases <t:%d:R>\n", at(10*24*time.Hour)),
		"4. Mystery · no upcoming episode listed\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("list %q lacks line %q", got, want)
		}
	}
}

func TestListNumbersStillMatchThePickerOnLaterPages(t *testing.T) {
	seedEntries(t, entry("Show 25", "s25", "Ep 2", at(time.Hour)))
	st := newFakeStore().with("user", shows(30)...)
	useStore(t, st)
	page := RenderSubscriptionPage(t.Context(), nil, nil, nil, 1, 30, "user").Response
	got := page.Embeds[0].Description
	if !strings.Contains(got, fmt.Sprintf("26. Show 25 · Ep 2 <t:%d:R>\n", at(time.Hour))) || strings.Contains(got, "25. Show 24") {
		t.Errorf("page 2 = %q", got)
	}
	if opts := pickerOptions(page); opts[0].Value != "Show 25" {
		t.Errorf("picker starts at %q", opts[0].Value)
	}
}
