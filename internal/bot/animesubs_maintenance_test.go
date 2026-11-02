package bot

import (
	"context"
	"fmt"
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
}

func (f *fakeUserSubs) CollapseDuplicateShows(_ context.Context, id string) error {
	f.collapsed = append(f.collapsed, id)
	f.ops = append(f.ops, "collapse:"+id)
	return nil
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

func TestCleanupCollapsesDuplicateAndNilEntries(t *testing.T) {
	airing := map[string]struct{}{"frieren": {}, "dandadan": {}}
	sub := func(name string) *anime_subs.ShowSub { return &anime_subs.ShowSub{Show: name} }
	tests := []struct {
		name        string
		shows       []*anime_subs.ShowSub
		wantRemoved []string
		wantOps     []string
		wantUpdated int
	}{
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
