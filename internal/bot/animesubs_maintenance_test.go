package bot

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"ReZeroTsu/internal/animeschedule"
	"ReZeroTsu/internal/database/anime_subs"
)

// rosterFixture seeds three weeks and the upcoming list around Wednesday 2026-07-15 and returns the roster.
func rosterFixture(t *testing.T, upcomingLoaded bool, lastWeek, thisWeek, nextWeek []animeschedule.ShowEntry, upcoming []animeschedule.UpcomingShow) (keepRoster, bool) {
	t.Helper()
	t.Cleanup(animeschedule.SetClock(func() time.Time { return time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC) }))
	monday := animeschedule.DateInWeek(time.Monday)
	// An unrelated show in each of this week and next week keeps both weeks loaded, which the cleanup requires.
	filler := animeschedule.ShowEntry{Name: "Filler", Route: "filler"}
	thisWeek = append(thisWeek[:len(thisWeek):len(thisWeek)], filler)
	nextWeek = append(nextWeek[:len(nextWeek):len(nextWeek)], filler)
	t.Cleanup(animeschedule.Seed(map[string][]animeschedule.ShowEntry{
		animeschedule.DateKey(monday.AddDate(0, 0, -7)): lastWeek,
		animeschedule.DateKey(monday):                   thisWeek,
		animeschedule.DateKey(monday.AddDate(0, 0, 7)):  nextWeek,
	}))
	t.Cleanup(animeschedule.SeedUpcoming(upcoming, upcomingLoaded))
	return buildKeepRoster()
}

func TestKeepRosterCleanupRule(t *testing.T) {
	show := func(name, route string, final bool) animeschedule.ShowEntry {
		return animeschedule.ShowEntry{Name: name, Route: route, Final: final}
	}
	one := func(e animeschedule.ShowEntry) []animeschedule.ShowEntry { return []animeschedule.ShowEntry{e} }
	tests := []struct {
		name     string
		sub      anime_subs.ShowSub
		last     []animeschedule.ShowEntry
		this     []animeschedule.ShowEntry
		next     []animeschedule.ShowEntry
		upcoming []animeschedule.UpcomingShow
		want     bool
	}{
		{"on next week only", anime_subs.ShowSub{Show: "A"}, nil, nil, one(show("A", "a", false)), nil, true},
		{"two-week break after a non-final episode", anime_subs.ShowSub{Show: "A"}, one(show("A", "a", false)), nil, nil, nil, true},
		{"last week's final episode", anime_subs.ShowSub{Show: "A"}, one(show("A", "a", true)), nil, nil, nil, false},
		{"on the upcoming list", anime_subs.ShowSub{Show: "A"}, nil, nil, nil, []animeschedule.UpcomingShow{{Name: "A", Route: "a"}}, true},
		{"on none of them", anime_subs.ShowSub{Show: "A"}, nil, nil, nil, nil, false},
		{"route match survives a rename", anime_subs.ShowSub{Show: "Old", Route: "a"}, nil, one(show("New", "a", false)), nil, nil, true},
		{"title fallback without a route", anime_subs.ShowSub{Show: " a "}, nil, one(show("A", "x", false)), nil, nil, true},
		{"a route that is absent is not rescued by the title", anime_subs.ShowSub{Show: "A", Route: "gone"}, nil, one(show("A", "a", false)), nil, nil, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r, ok := rosterFixture(t, true, tc.last, tc.this, tc.next, tc.upcoming)
			if !ok {
				t.Fatal("roster not ready")
			}
			if got := r.keeps(&tc.sub); got != tc.want {
				t.Errorf("keeps = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestKeepRosterNotReadyUntilUpcomingLoads(t *testing.T) {
	_, ok := rosterFixture(t, false, nil, []animeschedule.ShowEntry{{Name: "A", Route: "a"}}, nil, nil)
	if ok {
		t.Error("roster ready before the upcoming list loaded")
	}
}

func TestKeepRosterNotReadyUnlessThisAndNextWeekAreLoaded(t *testing.T) {
	entry := []animeschedule.ShowEntry{{Name: "A", Route: "a"}}
	upcoming := []animeschedule.UpcomingShow{{Name: "U", Route: "u"}}
	tests := []struct {
		name             string
		last, this, next []animeschedule.ShowEntry
		wantReady        bool
	}{
		{"only last week loaded", entry, nil, nil, false},
		{"this week loaded, next week missing", nil, entry, nil, false},
		{"next week loaded, this week missing", nil, nil, entry, false},
		{"this and next week loaded", nil, entry, entry, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Cleanup(animeschedule.SetClock(func() time.Time { return time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC) }))
			monday := animeschedule.DateInWeek(time.Monday)
			t.Cleanup(animeschedule.Seed(map[string][]animeschedule.ShowEntry{
				animeschedule.DateKey(monday.AddDate(0, 0, -7)): tc.last,
				animeschedule.DateKey(monday):                   tc.this,
				animeschedule.DateKey(monday.AddDate(0, 0, 7)):  tc.next,
			}))
			t.Cleanup(animeschedule.SeedUpcoming(upcoming, true))
			if _, ok := buildKeepRoster(); ok != tc.wantReady {
				t.Errorf("roster ready = %v, want %v", ok, tc.wantReady)
			}
		})
	}
}

// namesRoster keeps the shows with these names (by match key), for the tests of the removal mechanics.
func namesRoster(airing map[string]struct{}) keepRoster {
	r := keepRoster{chain: newShowSet()}
	for name := range airing {
		r.chain.keys[anime_subs.MatchKey(name)] = struct{}{}
	}
	return r
}

// fakeUserSubs is an in-memory userSubsStore (ticket #8). It has no whole-document rewrite.
type fakeUserSubs struct {
	docs    []*anime_subs.AnimeSubs
	removed map[string][]string
	// collapsed lists the IDs CollapseDuplicateShows was called for; ops is "remove:id" / "collapse:id" in order.
	collapsed []string
	ops       []string
	// collapseNoop makes CollapseDuplicateShows report that it modified nothing.
	collapseNoop bool
}

func (f *fakeUserSubs) CollapseDuplicateShows(_ context.Context, id string) (bool, error) {
	f.collapsed = append(f.collapsed, id)
	f.ops = append(f.ops, "collapse:"+id)
	return !f.collapseNoop, nil
}

func newFakeUserSubs(docs ...*anime_subs.AnimeSubs) *fakeUserSubs {
	return &fakeUserSubs{docs: docs, removed: map[string][]string{}}
}

func (f *fakeUserSubs) IterateUserSubs(_ context.Context, fn func(*anime_subs.AnimeSubs) error) error {
	for _, d := range f.docs {
		if err := fn(d); err != nil {
			return err
		}
	}
	return nil
}

func (f *fakeUserSubs) RemoveShows(_ context.Context, id string, names []string) error {
	f.removed[id] = append(f.removed[id], names...)
	f.ops = append(f.ops, "remove:"+id)
	return nil
}

func TestCleanupFinishedShows(t *testing.T) {
	airing := map[string]struct{}{"frieren": {}}
	st := newFakeUserSubs(
		&anime_subs.AnimeSubs{ID: "a", Shows: []*anime_subs.ShowSub{{Show: "Frieren"}, {Show: "Old Show"}, {Show: "Gone"}}},
		&anime_subs.AnimeSubs{ID: "b", Shows: []*anime_subs.ShowSub{{Show: "Frieren"}}},
	)
	updated, err := cleanupFinishedShows(context.Background(), st, namesRoster(airing))
	if err != nil {
		t.Fatal(err)
	}
	if updated != 1 {
		t.Errorf("updated = %d, want 1", updated)
	}
	got := st.removed["a"]
	if len(got) != 2 || got[0] != "Old Show" || got[1] != "Gone" {
		t.Errorf("removed for a = %v, want [Old Show Gone]", got)
	}
	if _, ok := st.removed["b"]; ok {
		t.Errorf("document b with only airing shows was touched")
	}
}

// Cleanup must call a show finished exactly when the notifier (anime_subs.MatchKey) cannot match it to an airing
// schedule name. airing is keyed by lowercased, untrimmed names.
func TestCleanupFinishedShowsMatchesLikeTheNotifier(t *testing.T) {
	tests := []struct {
		name        string
		airingName  string
		subName     string
		wantRemoved []string
	}{
		{"schedule name with a trailing space is still airing", "Frieren ", "Frieren ", nil},
		{"subscription matching only after a trim is still airing", "Frieren ", "frieren", nil},
		{"subscription ending in U+3000 never matches, so it is finished", "Frieren", "Frieren　", []string{"Frieren　"}},
		{"subscription ending in U+0085 never matches, so it is finished", "Frieren", "Frieren\u0085", []string{"Frieren\u0085"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			airing := map[string]struct{}{strings.ToLower(tc.airingName): {}}
			st := newFakeUserSubs(&anime_subs.AnimeSubs{ID: "u", Shows: []*anime_subs.ShowSub{{Show: tc.subName}}})
			if _, err := cleanupFinishedShows(context.Background(), st, namesRoster(airing)); err != nil {
				t.Fatal(err)
			}
			if fmt.Sprint(st.removed["u"]) != fmt.Sprint(tc.wantRemoved) {
				t.Errorf("removed = %q, want %q", st.removed["u"], tc.wantRemoved)
			}
		})
	}
}

func TestCleanupCollapsesDuplicateAndNilEntries(t *testing.T) {
	airing := map[string]struct{}{"frieren": {}, "dandadan": {}, "äbc": {}, "　frieren": {}}
	sub := func(name string) *anime_subs.ShowSub { return &anime_subs.ShowSub{Show: name} }
	tests := []struct {
		name         string
		shows        []*anime_subs.ShowSub
		collapseNoop bool
		wantRemoved  []string
		wantOps      []string
		wantUpdated  int
	}{
		{name: "entry without a show name is pruned", shows: []*anime_subs.ShowSub{sub("Frieren"), sub("")}, wantOps: []string{"collapse:u"}, wantUpdated: 1},
		{name: "whitespace-only show name is pruned", shows: []*anime_subs.ShowSub{sub("Frieren"), sub("  ")}, wantOps: []string{"collapse:u"}, wantUpdated: 1},
		// Mongo's $toLower is ASCII-only and $trim keeps U+3000, so these are not duplicates to the collapse.
		{name: "names differing in non-ASCII case are not flagged", shows: []*anime_subs.ShowSub{sub("ÄBC"), sub("äbc")}, wantUpdated: 0},
		{name: "names differing in a space $trim keeps are not flagged", shows: []*anime_subs.ShowSub{sub("Frieren"), sub("　Frieren")}, wantUpdated: 0},
		{
			name: "a collapse that modifies nothing is not counted", shows: []*anime_subs.ShowSub{sub("Frieren"), sub("frieren")},
			collapseNoop: true, wantOps: []string{"collapse:u"}, wantUpdated: 0,
		},
		{name: "clean document is untouched", shows: []*anime_subs.ShowSub{sub("Frieren"), sub("Dandadan")}, wantUpdated: 0},
		{name: "case-insensitive duplicate is collapsed", shows: []*anime_subs.ShowSub{sub("Frieren"), sub("frieren")}, wantOps: []string{"collapse:u"}, wantUpdated: 1},
		{name: "duplicate differing in surrounding spaces is collapsed", shows: []*anime_subs.ShowSub{sub("Frieren"), sub(" FRIEREN ")}, wantOps: []string{"collapse:u"}, wantUpdated: 1},
		{name: "nil entry is pruned", shows: []*anime_subs.ShowSub{sub("Frieren"), nil}, wantOps: []string{"collapse:u"}, wantUpdated: 1},
		{
			name:        "duplicates among finished shows only need the removal",
			shows:       []*anime_subs.ShowSub{sub("Frieren"), sub("Old"), sub("old")},
			wantRemoved: []string{"Old", "old"}, wantOps: []string{"remove:u"}, wantUpdated: 1,
		},
		{
			name:        "finished show and airing duplicate: removal then collapse, counted once",
			shows:       []*anime_subs.ShowSub{sub("Old"), sub("Dandadan"), sub("DANDADAN"), nil},
			wantRemoved: []string{"Old"}, wantOps: []string{"remove:u", "collapse:u"}, wantUpdated: 1,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := newFakeUserSubs(&anime_subs.AnimeSubs{ID: "u", Shows: tc.shows})
			st.collapseNoop = tc.collapseNoop
			updated, err := cleanupFinishedShows(context.Background(), st, namesRoster(airing))
			if err != nil {
				t.Fatal(err)
			}
			if updated != tc.wantUpdated {
				t.Errorf("updated = %d, want %d", updated, tc.wantUpdated)
			}
			if fmt.Sprint(st.removed["u"]) != fmt.Sprint(tc.wantRemoved) {
				t.Errorf("removed = %v, want %v", st.removed["u"], tc.wantRemoved)
			}
			if fmt.Sprint(st.ops) != fmt.Sprint(tc.wantOps) {
				t.Errorf("ops = %v, want %v", st.ops, tc.wantOps)
			}
		})
	}
}
