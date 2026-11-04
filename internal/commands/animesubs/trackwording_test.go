package animesubs

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"ReZeroTsu/internal/animeschedule"
	"ReZeroTsu/internal/database/anime_subs"
)

func trackRow(name, route, ep string, track animeschedule.Track, air int64) animeschedule.ShowEntry {
	e := entry(name, route, ep, air)
	e.Track = track
	return e
}

const (
	noDubSentence = "No dub is listed yet. You'll get a DM when a dubbed episode airs."
	noRawSentence = "No raw is listed yet. You'll get a DM when a raw episode airs."
	noSubSentence = "No sub is listed yet. You'll get a DM when a subbed episode airs."
)

func TestAddNamesTheEarliestSelectedRowAcrossTheSetsTracksAndLabelsItWithSeveralTracks(t *testing.T) {
	seedEntries(t,
		trackRow("Frieren", "frieren", "Ep 6", animeschedule.TrackSub, at(2*24*time.Hour)),
		trackRow("Frieren", "frieren", "Ep 2", animeschedule.TrackDub, at(24*time.Hour)))
	for _, tc := range []struct{ track, want string }{
		{"sub_dub", "Subscribed to **Frieren** (Sub and Dub).\nNext episode: Ep 2 (dub), " + fullAndRelative(at(24*time.Hour)) + "."},
		{"dub", "Subscribed to **Frieren** (Dub).\nNext episode: Ep 2, " + fullAndRelative(at(24*time.Hour)) + "."},
		{"sub", "Subscribed to **Frieren**.\nNext episode: Ep 6, " + fullAndRelative(at(2*24*time.Hour)) + "."},
	} {
		if got := addWithTrack(t, "Frieren", tc.track); got != tc.want {
			t.Errorf("track %q:\n got %q\nwant %q", tc.track, got, tc.want)
		}
	}
}

func TestAddNamesTheLatestAiredSelectedRowWithItsTrackWhenNothingIsUpcoming(t *testing.T) {
	seedEntries(t,
		trackRow("Frieren", "frieren", "Ep 5", animeschedule.TrackSub, at(-5*time.Hour)),
		trackRow("Frieren", "frieren", "Ep 2", animeschedule.TrackDub, at(-2*time.Hour)))
	want := fmt.Sprintf("Subscribed to **Frieren** (Sub and Dub).\nEp 2 (dub) aired <t:%d:R>. You'll get a DM when the next one airs.", at(-2*time.Hour))
	if got := addWithTrack(t, "Frieren", "sub_dub"); got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestAddLabelsAMoviesReleaseWithItsTrack(t *testing.T) {
	dub := trackRow("Big Film", "big-film", "Ep 1", animeschedule.TrackDub, at(10*24*time.Hour))
	sub := trackRow("Big Film", "big-film", "Ep 1", animeschedule.TrackSub, at(-24*time.Hour))
	dub.MediaType, sub.MediaType = animeschedule.MediaMovie, animeschedule.MediaMovie
	seedEntries(t, sub, dub)
	want := "Subscribed to **Big Film** (Sub and Dub).\nIts dub releases " + fullAndRelative(at(10*24*time.Hour)) + "."
	if got := addWithTrack(t, "Big Film", "sub_dub"); got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestAddSaysNoDubIsListedYetInPlaceOfTheNextEpisodeWhenNoTrackIsListed(t *testing.T) {
	seedEntries(t, trackRow("Frieren", "frieren", "Ep 6", animeschedule.TrackSub, at(24*time.Hour)))
	for _, tc := range []struct{ track, want string }{
		{"dub", "Subscribed to **Frieren** (Dub).\n" + noDubSentence},
		{"raw", "Subscribed to **Frieren** (Raw).\n" + noRawSentence},
	} {
		if got := addWithTrack(t, "Frieren", tc.track); got != tc.want {
			t.Errorf("track %q:\n got %q\nwant %q", tc.track, got, tc.want)
		}
	}
}

func TestAddNamesTheSubsNextEpisodeAndAddsTheDubSentenceWhenOnlyTheDubIsMissing(t *testing.T) {
	seedEntries(t, trackRow("Frieren", "frieren", "Ep 6", animeschedule.TrackSub, at(24*time.Hour)))
	want := "Subscribed to **Frieren** (Sub and Dub).\nNext episode: Ep 6 (sub), " + fullAndRelative(at(24*time.Hour)) + ".\n" + noDubSentence
	if got := addWithTrack(t, "Frieren", "sub_dub"); got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestAddCountsARawFallbackRowAsTheSubListedAndLabelsItRaw(t *testing.T) {
	seedEntries(t, trackRow("Frieren", "frieren", "Ep 5", animeschedule.TrackRaw, at(24*time.Hour)))
	// The raw fallback row is what Sub will DM, so Sub is listed; only the dub is missing.
	want := "Subscribed to **Frieren** (Sub and Dub).\nNext episode: Ep 5 (raw), " + fullAndRelative(at(24*time.Hour)) + ".\n" + noDubSentence
	if got := addWithTrack(t, "Frieren", "sub_dub"); got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
	// A plain Sub subscription reads as it always did.
	want = "Subscribed to **Frieren**.\nNext episode: Ep 5, " + fullAndRelative(at(24*time.Hour)) + "."
	if got := addWithTrack(t, "Frieren", "sub"); got != want {
		t.Errorf("plain Sub:\n got %q\nwant %q", got, want)
	}
}

func TestAddWithNoSubRowNoRawRowAndNoSubTrackStillSaysNoSubIsListedYet(t *testing.T) {
	dub := premiereIn(10 * 24 * time.Hour)
	seedUpcoming(t, []animeschedule.UpcomingShow{{Name: "Frieren", Route: "frieren", DubPremiere: dub}})
	want := "Subscribed to **Frieren** (Sub and Dub).\nIts dub premieres " + fullAndRelative(dub.Unix()) + ". You'll get a DM when episode 1 airs.\n" + noSubSentence
	if got := addWithTrack(t, "Frieren", "sub_dub"); got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestListLineNamesARawFallbackRowForTheSubWithItsRealTrack(t *testing.T) {
	seedEntries(t, trackRow("Frieren", "frieren", "Ep 5", animeschedule.TrackRaw, at(24*time.Hour)))
	st := newFakeStore()
	st.docs["user"] = []*anime_subs.ShowSub{{Show: "Frieren", Route: "frieren", Tracks: "sub_dub"}}
	useStore(t, st)
	want := fmt.Sprintf("1. Frieren · Sub and Dub · Ep 5 (raw) <t:%d:R>\n", at(24*time.Hour))
	if got := reply(t, invoke(t, "list")).Embeds[0].Description; !strings.Contains(got, want) {
		t.Errorf("list %q lacks %q", got, want)
	}
}

func TestAddEveryTrackNamesRawsPremiereAsRawAndDoesNotContradictItself(t *testing.T) {
	premiere := premiereIn(10 * 24 * time.Hour)
	noClock := time.Date(2027, 1, 10, 0, 0, 0, 0, time.UTC)
	seedUpcoming(t, []animeschedule.UpcomingShow{{
		Name: "Frieren", Route: "frieren", Premiere: premiere, JPNTime: premiere, SubTime: noClock, DubTime: noClock,
	}})
	got := addWithTrack(t, "Frieren", "every")
	if !strings.Contains(got, "Its raw premieres "+fullAndRelative(premiere.Unix())) {
		t.Errorf("reply %q does not name raw's premiere labelled raw", got)
	}
	if strings.Contains(got, "Its sub premieres") {
		t.Errorf("reply %q labels raw's premiere sub", got)
	}
}

func TestAddCountsAnUpcomingTrackThatExistsAsListedAndOneThatDoesNotAsMissing(t *testing.T) {
	seedUpcoming(t, []animeschedule.UpcomingShow{
		{Name: "Frieren", Route: "frieren", Premiere: premiereIn(10 * 24 * time.Hour), SubPremiere: premiereIn(12 * 24 * time.Hour)},
		{Name: "Dubbed", Route: "dubbed", Premiere: premiereIn(10 * 24 * time.Hour), DubPremiere: premiereIn(12 * 24 * time.Hour)},
	})
	want := "Subscribed to **Frieren** (Sub and Dub).\nIts sub premieres " + fullAndRelative(premiereIn(12*24*time.Hour).Unix()) + ". You'll get a DM when episode 1 airs.\n" + noDubSentence
	if got := addWithTrack(t, "Frieren", "sub_dub"); got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
	if got := addWithTrack(t, "Dubbed", "dub"); strings.Contains(got, "listed yet") {
		t.Errorf("a dub that exists reads %q", got)
	}
}

func TestListLinesInsertTheSetAndLabelTheNextEpisodeWithSeveralTracks(t *testing.T) {
	seedEntries(t,
		trackRow("Both", "both", "Ep 6", animeschedule.TrackSub, at(2*24*time.Hour)),
		trackRow("Both", "both", "Ep 2", animeschedule.TrackDub, at(24*time.Hour)),
		trackRow("Dubbed", "dubbed", "Ep 2", animeschedule.TrackDub, at(2*24*time.Hour)),
		trackRow("Subonly", "subonly", "Ep 6", animeschedule.TrackSub, at(3*24*time.Hour)),
		trackRow("Plain", "plain", "Ep 3", animeschedule.TrackSub, at(4*24*time.Hour)))
	st := newFakeStore()
	st.docs["user"] = []*anime_subs.ShowSub{
		{Show: "Dubbed", Route: "dubbed", Tracks: "dub"},
		{Show: "Both", Route: "both", Tracks: "sub_dub"},
		{Show: "Subonly", Route: "subonly", Tracks: "dub"},
		{Show: "Plain", Route: "plain"},
	}
	useStore(t, st)
	got := reply(t, invoke(t, "list")).Embeds[0].Description
	for _, want := range []string{
		fmt.Sprintf("1. Dubbed · Dub · Ep 2 <t:%d:R>\n", at(2*24*time.Hour)),
		fmt.Sprintf("2. Both · Sub and Dub · Ep 2 (dub) <t:%d:R>\n", at(24*time.Hour)),
		"3. Subonly · Dub · no dub listed yet\n",
		fmt.Sprintf("4. Plain · Ep 3 <t:%d:R>\n", at(4*24*time.Hour)),
	} {
		if !strings.Contains(got, want) {
			t.Errorf("list %q lacks line %q", got, want)
		}
	}
}

func TestListLineInsertsTheSetAfterTheMediaTypeLabel(t *testing.T) {
	film := trackRow("Big Film", "big-film", "Ep 1", animeschedule.TrackDub, at(24*time.Hour))
	film.MediaType = animeschedule.MediaMovie
	seedEntries(t, film)
	st := newFakeStore()
	st.docs["user"] = []*anime_subs.ShowSub{{Show: "Big Film", Route: "big-film", Tracks: "dub"}}
	useStore(t, st)
	want := fmt.Sprintf("1. Big Film · Movie · Dub · releases <t:%d:R>\n", at(24*time.Hour))
	if got := reply(t, invoke(t, "list")).Embeds[0].Description; !strings.Contains(got, want) {
		t.Errorf("list %q lacks %q", got, want)
	}
}

// The worst list line: a TV Short Donghua entry that already aired, on Every track, with a span label, under a title
// at the clip limit. Its status is " · TV Short · Donghua" (21) + " · Every track" (14) + " · Ep 100-101F (sub) aired
// <t:…:R>" (43), about 78 units. The footer is the longest one, from the last page of a full list.
func TestWorstCasePageOfListLinesKeepsItsFooter(t *testing.T) {
	var rows []animeschedule.ShowEntry
	var list []*anime_subs.ShowSub
	for i := range 25 {
		name := strings.Repeat("あ", 100) + fmt.Sprint(i)
		e := trackRow(name, fmt.Sprint("r", i), "Ep 100-101F", animeschedule.TrackSub, at(-time.Duration(i+1)*time.Hour))
		e.MediaType = animeschedule.MediaTVShort
		e.Donghua = true
		rows = append(rows, e)
		list = append(list, &anime_subs.ShowSub{Show: name, Route: fmt.Sprint("r", i), Tracks: "every"})
	}
	seedEntries(t, rows...)
	got := subsDescriptionForPage(list, 0, 0, 4, 100)
	if !strings.Contains(got, " · TV Short · Donghua · Every track · Ep 100-101F (sub) aired <t:") {
		t.Fatalf("description lacks the worst-case status: %q", got)
	}
	if n := utf16Len(got); n > 4096 {
		t.Errorf("description is %d UTF-16 units, want at most 4096", n)
	}
	if !strings.Contains(got, "Page 1 of 4. Showing 1–25 of 100.") {
		t.Errorf("description lost its footer")
	}
}

// TestEveryPageOfEveryStatusKeepsItsFooter builds a full first page and a full last page for every combination of
// media type, Donghua, track set and status, so a longer label or a new status form that no longer fits fails here
// the day it is added, not the day a member's list loses its footer.
func TestEveryPageOfEveryStatusKeepsItsFooter(t *testing.T) {
	types := []animeschedule.MediaType{animeschedule.MediaTV, animeschedule.MediaTVShort, animeschedule.MediaONA,
		animeschedule.MediaOVA, animeschedule.MediaSpecial, animeschedule.MediaMovie}
	// A set's stored form and the track of the rows it selects, labelled when the set has several tracks.
	sets := []struct {
		stored string
		track  animeschedule.Track
	}{
		{"", animeschedule.TrackSub}, {"dub", animeschedule.TrackDub}, {"raw", animeschedule.TrackRaw},
		{"sub_dub", animeschedule.TrackDub}, {"every", animeschedule.TrackDub},
	}
	states := []string{"next", "aired", "premiere", "premiered", "premiere date unknown", "nothing listed"}
	const total = 100
	checked := 0
	for _, mt := range types {
		for _, donghua := range []bool{false, true} {
			for _, set := range sets {
				for _, state := range states {
					var rows []animeschedule.ShowEntry
					var upcoming []animeschedule.UpcomingShow
					var list []*anime_subs.ShowSub
					for i := range total {
						route := fmt.Sprint("r", i)
						name := strings.Repeat("あ", 100) + fmt.Sprint(i)
						list = append(list, &anime_subs.ShowSub{Show: name, Route: route, Tracks: set.stored})
						switch state {
						case "next", "aired":
							d := time.Duration(i+1) * time.Hour
							if state == "aired" {
								d = -d
							}
							e := trackRow(name, route, "Ep 100-101F", set.track, at(d))
							e.MediaType, e.Donghua = mt, donghua
							rows = append(rows, e)
						case "premiere", "premiered", "premiere date unknown":
							var p time.Time
							switch state {
							case "premiere":
								p = nextEpisodeNow.Add(48 * time.Hour)
							case "premiered":
								p = nextEpisodeNow.Add(-48 * time.Hour)
							}
							upcoming = append(upcoming, animeschedule.UpcomingShow{Name: name, Route: route, MediaType: mt,
								Donghua: donghua, Premiere: p, SubPremiere: p, DubPremiere: p})
						}
					}
					seedEntries(t, rows...)
					t.Cleanup(animeschedule.SeedUpcoming(upcoming, true))
					for _, start := range []int{0, total - 25} {
						page := start / 25
						got := subsDescriptionForPage(list, start, page, total/25, total)
						footer := fmt.Sprintf("Page %d of %d. Showing %d–%d of %d.", page+1, total/25, start+1, start+25, total)
						if n := utf16Len(got); n > 4096 || !strings.Contains(got, footer) {
							t.Fatalf("%v donghua=%v set=%q %s page %d: %d UTF-16 units, footer kept = %v; line: %q",
								mt, donghua, set.stored, state, page+1, n, strings.Contains(got, footer), strings.SplitN(got, "\n", 2)[0])
						}
						checked++
					}
				}
			}
		}
	}
	if checked != len(types)*2*len(sets)*len(states)*2 {
		t.Fatalf("checked %d pages", checked)
	}
}

func TestPageOf25EveryTrackLinesStaysUnderTheEmbedLimit(t *testing.T) {
	var rows []animeschedule.ShowEntry
	var list []*anime_subs.ShowSub
	for i := range 25 {
		name := strings.Repeat("あ", 80) + fmt.Sprint(i)
		e := trackRow(name, fmt.Sprint("r", i), "Ep 1234-5678F", animeschedule.TrackSub, at(time.Duration(i+1)*time.Hour))
		e.MediaType = animeschedule.MediaTVShort
		rows = append(rows, e)
		list = append(list, &anime_subs.ShowSub{Show: name, Route: fmt.Sprint("r", i), Tracks: "every"})
	}
	seedEntries(t, rows...)
	got := subsDescriptionForPage(list, 0, 0, 2, 30)
	if n := len(utf16.Encode([]rune(got))); n > 4096 {
		t.Errorf("description is %d UTF-16 units, want at most 4096", n)
	}
	if !strings.Contains(got, "· TV Short · Every track · Ep 1234-5678F (sub) <t:") {
		t.Errorf("description lacks the set and label: %q", got)
	}
}
