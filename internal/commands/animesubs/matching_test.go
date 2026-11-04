package animesubs

import (
	"context"
	"strings"
	"testing"
	"time"

	"ReZeroTsu/internal/animeschedule"
	"ReZeroTsu/internal/database/anime_subs"
)

// lookupAs makes the typed lookup resolve every input to show.
func lookupAs(t *testing.T, show animeschedule.ShowEntry) {
	t.Helper()
	old := showLookup
	showLookup = func(string, bool) (animeschedule.ShowEntry, bool) { return show, true }
	t.Cleanup(func() { showLookup = old })
}

func finalEpisode(e animeschedule.ShowEntry) animeschedule.ShowEntry {
	e.Final = true
	return e
}

// ADR-0002: a subscription that has a route matches by route only, so a second show with the same title is a
// different show and can be subscribed to.
func TestAddSubscribesToASecondShowThatSharesATitleWithASubscribedOne(t *testing.T) {
	lookupAs(t, animeschedule.ShowEntry{Name: "Foo", Route: "foo-b"})
	st := newFakeStore()
	st.docs["u"] = []*anime_subs.ShowSub{{Show: "Foo", Route: "foo-a"}}
	msg, _, err := subCore(context.Background(), st, "u", "foo-b", animeschedule.SubscribeSub, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "Subscribed to") || len(st.docs["u"]) != 2 {
		t.Errorf("msg = %q, subscriptions = %+v, want the second show added", msg, st.docs["u"])
	}
}

// A renamed show keeps its route, so the already-subscribed check must see it even though the title changed.
func TestAddRecognisesARenamedShowByItsRoute(t *testing.T) {
	lookupAs(t, animeschedule.ShowEntry{Name: "Foo Season 2", Route: "x"})
	st := newFakeStore()
	st.docs["u"] = []*anime_subs.ShowSub{{Show: "Foo", Route: "x"}}
	msg, _, err := subCore(context.Background(), st, "u", "x", animeschedule.SubscribeSub, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "already subscribed") || len(st.docs["u"]) != 1 {
		t.Errorf("msg = %q, subscriptions = %+v, want no second subscription", msg, st.docs["u"])
	}
}

// A legacy subscription has no route, so it matches the show by title.
func TestAddRecognisesALegacyRoutelessSubscriptionByTitle(t *testing.T) {
	lookupAs(t, animeschedule.ShowEntry{Name: "Foo", Route: "x"})
	st := newFakeStore().with("u", "foo ")
	msg, _, err := subCore(context.Background(), st, "u", "x", animeschedule.SubscribeSub, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "already subscribed") || len(st.docs["u"]) != 1 {
		t.Errorf("msg = %q, subscriptions = %+v, want the legacy subscription to count", msg, st.docs["u"])
	}
}

// Seeding marks an episode as notified only for the show's own aired entry, not for another show's of the same title.
func TestAddSeedsOnlyFromTheSameShowsAiredEntry(t *testing.T) {
	seedEntries(t, entry("Foo", "foo-a", "Ep 3", at(-time.Hour)))
	lookupAs(t, animeschedule.ShowEntry{Name: "Foo", Route: "foo-b"})
	st := newFakeStore()
	if _, _, err := subCore(context.Background(), st, "u", "foo-b", animeschedule.SubscribeSub, false); err != nil {
		t.Fatal(err)
	}
	if got := st.docs["u"]; len(got) != 1 || got[0].Notified || got[0].LastNotifiedAirUnix != 0 {
		t.Errorf("subscription = %+v, want it unseeded by the other show's episode", got)
	}
}

func TestAiredTodayEntryMatchesByRouteThenTitle(t *testing.T) {
	today := []animeschedule.ShowEntry{{Name: "Foo", Route: "foo-a", AirTimeUnix: 900}}
	if _, ok := airedTodayEntry(today, "foo-b", "Foo", 1000); ok {
		t.Error("a different route with the same title was taken as the aired entry")
	}
	if _, ok := airedTodayEntry(today, "foo-a", "Renamed", 1000); !ok {
		t.Error("the same route under another title was not taken as the aired entry")
	}
	if _, ok := airedTodayEntry(today, "", "foo", 1000); !ok {
		t.Error("a route-less show was not matched by title")
	}
}

// ADR-0002: the notifier never title-matches a subscription that has a route, so neither may the labels.
func TestSubscriptionWithARouteIsNotMatchedByTitleToARoutelessEntry(t *testing.T) {
	seedEntries(t, entry("Twin", "", "Ep 1", at(time.Hour)))
	st := newFakeStore()
	st.docs["user"] = []*anime_subs.ShowSub{{Show: "Twin", Route: "twin-a"}}
	if got := strings.Join(names(suggestions(t, st, "user", "")), "|"); got != "Twin" {
		t.Errorf("suggestions = %q, want Twin unlabelled", got)
	}
	useStore(t, st)
	if got := reply(t, invoke(t, "list")).Embeds[0].Description; !strings.Contains(got, "1. Twin · no upcoming episode listed\n") {
		t.Errorf("list = %q, want no episode taken from the route-less entry", got)
	}
}

func TestSuggestionsAndLookupCoverLastWeeksShowsOnABreak(t *testing.T) {
	seedEntries(t,
		entry("On Break", "on-break", "Ep 5", at(-5*24*time.Hour)),
		finalEpisode(entry("Ended", "ended", "Ep 12", at(-5*24*time.Hour))),
		finalEpisode(entry("Returning", "returning", "Ep 12", at(-5*24*time.Hour))),
		entry("Returning", "returning", "Ep 1", at(24*time.Hour)))
	got := strings.Join(names(suggestions(t, newFakeStore(), "user", "")), "|")
	if want := "Returning|On Break"; got != want {
		t.Errorf("suggestions = %q, want %q (a show whose final episode aired last week is dropped)", got, want)
	}
	st := newFakeStore()
	useStore(t, st)
	if msg := text(reply(t, invoke(t, "add", animeOption("on-break")...))); !strings.Contains(msg, "Subscribed to **On Break**") {
		t.Errorf("reply %q, want the show on a break to resolve", msg)
	}
	st = newFakeStore()
	useStore(t, st)
	if msg := text(reply(t, invoke(t, "add", animeOption("ended")...))); !strings.Contains(msg, "could not find") {
		t.Errorf("reply %q, want a show that ended last week not to resolve", msg)
	}
}
