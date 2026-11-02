package bot

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"ReZeroTsu/internal/database/anime_subs"
)

// fakeUserSubs is an in-memory userSubsStore (ticket #8). It has no whole-document rewrite.
type fakeUserSubs struct {
	docs    []*anime_subs.AnimeSubs
	removed map[string][]string
	airSet  map[string]int64 // "id|show" -> airUnix
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
	return &fakeUserSubs{docs: docs, removed: map[string][]string{}, airSet: map[string]int64{}}
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

func (f *fakeUserSubs) SetShowAirUnix(_ context.Context, id, show string, airUnix int64) error {
	f.airSet[id+"|"+show] = airUnix
	return nil
}

func TestCleanupFinishedShows(t *testing.T) {
	airing := map[string]struct{}{"frieren": {}}
	st := newFakeUserSubs(
		&anime_subs.AnimeSubs{ID: "a", Shows: []*anime_subs.ShowSub{{Show: "Frieren"}, {Show: "Old Show"}, {Show: "Gone"}}},
		&anime_subs.AnimeSubs{ID: "b", Shows: []*anime_subs.ShowSub{{Show: "Frieren"}}},
	)
	updated, err := cleanupFinishedShows(context.Background(), st, airing)
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
// schedule name. airing is keyed as animeschedule.GetAiringShowNames builds it: lowercased, not trimmed.
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
			if _, err := cleanupFinishedShows(context.Background(), st, airing); err != nil {
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
			updated, err := cleanupFinishedShows(context.Background(), st, airing)
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

func TestBackfillShows(t *testing.T) {
	aired := map[string]int64{"frieren": 9_000, "dandadan": 8_000}
	st := newFakeUserSubs(&anime_subs.AnimeSubs{ID: "u", Shows: []*anime_subs.ShowSub{
		{Show: "Frieren", Notified: true},
		{Show: "Dandadan", Notified: true, LastNotifiedAirUnix: 5},
		{Show: "Unnotified", Notified: false},
		{Show: "Unknown", Notified: true},
		{Show: "Guild", Notified: true, Guild: true},
	}})
	n, err := backfillUserSubs(context.Background(), st, aired)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("users updated = %d, want 1", n)
	}
	if len(st.airSet) != 1 || st.airSet["u|Frieren"] != 9_000 {
		t.Errorf("airSet = %v, want only u|Frieren=9000", st.airSet)
	}
}
