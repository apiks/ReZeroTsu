package animesubs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"ReZeroTsu/internal/animeschedule"
	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/database/anime_subs"
)

// Timetable shows come before upcoming shows however the typed text matches them: ranking happens within each
// source, not across both.
func TestAddSuggestionsRankWithinEachSourceThenListTimetableShowsFirst(t *testing.T) {
	seedUpcoming(t, []animeschedule.UpcomingShow{{Name: "Kaiju No. 9", Route: "kaiju"}},
		entry("Shikanoko", "shikanoko", "Ep 2", at(time.Hour)))
	got := strings.Join(names(suggestions(t, newFakeStore(), "user", "ka")), "|")
	if want := "Shikanoko|Kaiju No. 9 · premiere date TBA"; got != want {
		t.Errorf("names = %q, want %q", got, want)
	}
}

// The 25-choice cap applies after the subscribed shows are moved last, so they cannot use up slots the unsubscribed
// shows after them need.
func TestAddSuggestionsCapAfterTheFinalOrdering(t *testing.T) {
	var entries []animeschedule.ShowEntry
	st := newFakeStore()
	for i := range 60 {
		name := fmt.Sprintf("Show %02d", i)
		entries = append(entries, entry(name, fmt.Sprintf("show-%02d", i), "Ep 2", at(time.Hour)))
		if i < 20 {
			st.docs["user"] = append(st.docs["user"], &anime_subs.ShowSub{Show: name, Route: fmt.Sprintf("show-%02d", i)})
		}
	}
	seedEntries(t, entries...)
	got := names(suggestions(t, st, "user", ""))
	if len(got) != 25 || got[0] != "Show 20" || got[24] != "Show 44" {
		t.Errorf("got %d choices from %q to %q, want 25 unsubscribed shows Show 20 to Show 44", len(got), got[0], got[len(got)-1])
	}
}

func utf16Len(s string) int { return len(utf16.Encode([]rune(s))) }

// A list page is capped in characters Discord counts (UTF-16 units), not bytes, and the page footer must survive.
func TestListPageOfLongTitlesFitsTheEmbedAndKeepsItsFooter(t *testing.T) {
	for name, glyph := range map[string]string{"Japanese": "あ", "astral": "😀"} {
		t.Run(name, func(t *testing.T) {
			var list []*anime_subs.ShowSub
			for i := range 30 {
				list = append(list, &anime_subs.ShowSub{Show: strings.Repeat(glyph, 150) + fmt.Sprint(i)})
			}
			got := subsDescriptionForPage(list, 0, 0, 2, len(list))
			if n := utf16Len(got); n > 4096 {
				t.Errorf("description is %d UTF-16 units, want at most 4096", n)
			}
			if !utf8.ValidString(got) || !strings.Contains(got, "Page 1 of 2. Showing 1–25 of 30.") {
				t.Errorf("description lost its footer or was cut mid-character: ...%q", got[max(len(got)-120, 0):])
			}
			if !strings.Contains(got, "25. ") {
				t.Errorf("description lacks line 25")
			}
		})
	}
}

func TestAddSaysAnUndatedMovieWillDMOnRelease(t *testing.T) {
	seedUpcoming(t, []animeschedule.UpcomingShow{{Name: "Undated Film", Route: "undated-film", MediaType: animeschedule.MediaMovie}})
	got := addReply(t, "Undated Film").Embeds[0].Description
	want := "Subscribed to **Undated Film**.\nIts release date isn't announced yet. You'll get a DM when it is released."
	if got != want {
		t.Errorf("description = %q, want %q", got, want)
	}
}

func TestAddSaysAnUpcomingShowAlreadyPremieredInThePastTense(t *testing.T) {
	seedUpcoming(t, []animeschedule.UpcomingShow{{Name: "Past Show", Route: "past-show", Premiere: premiereIn(-2 * time.Hour)}})
	got := addReply(t, "Past Show").Embeds[0].Description
	want := fmt.Sprintf("Subscribed to **Past Show**.\nIt premiered <t:%d:R>. You'll get a DM when the next episode airs.", premiereIn(-2*time.Hour).Unix())
	if got != want {
		t.Errorf("description = %q, want %q", got, want)
	}
	st := newFakeStore()
	st.docs["user"] = []*anime_subs.ShowSub{{Show: "Past Show", Route: "past-show"}}
	useStore(t, st)
	line := fmt.Sprintf("1. Past Show · premiered <t:%d:R>\n", premiereIn(-2*time.Hour).Unix())
	if list := reply(t, invoke(t, "list")).Embeds[0].Description; !strings.Contains(list, line) {
		t.Errorf("list %q lacks %q", list, line)
	}
}

func TestAddNotFoundReplyCoversUpcomingShows(t *testing.T) {
	seedTypes(t)
	useStore(t, newFakeStore())
	plain := text(reply(t, invoke(t, "add", animeOption("Nothing Like It")...)))
	if !strings.Contains(plain, "airing or upcoming") || strings.Contains(plain, "this week") || strings.Contains(plain, "type filter") {
		t.Errorf("reply %q", plain)
	}
}

type failingClear struct{ *fakeStore }

func (failingClear) ClearShows(context.Context, string) error { return errors.New("db down") }

// A clear that fails is logged as a clear, not as a picker failure.
func TestClearFailureIsLoggedUnderItsOwnMessage(t *testing.T) {
	id := clearButtonID(t, "Remove all")
	old := storeOf
	storeOf = func(_ *database.Client) subsStore { return failingClear{newFakeStore()} }
	t.Cleanup(func() { storeOf = old })
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	if err := handleClearButton(press("user", id), nil); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); strings.Contains(got, "picker") || !strings.Contains(got, "clear") {
		t.Errorf("log = %q, want a clear failure not reported as a picker failure", got)
	}
}
