package animesubs

import (
	"context"
	"strings"
	"testing"

	"ReZeroTsu/internal/animeschedule"
	"ReZeroTsu/internal/database/anime_subs"
)

// fakeStore is an in-memory subsStore. It has no whole-document rewrite, so the commands cannot call one.
type fakeStore struct {
	docs  map[string][]*anime_subs.ShowSub
	calls []string
}

func newFakeStore() *fakeStore { return &fakeStore{docs: map[string][]*anime_subs.ShowSub{}} }

func (f *fakeStore) with(uid string, names ...string) *fakeStore {
	for _, n := range names {
		f.docs[uid] = append(f.docs[uid], &anime_subs.ShowSub{Show: n})
	}
	return f
}

func (f *fakeStore) names(uid string) []string {
	var out []string
	for _, s := range f.docs[uid] {
		out = append(out, s.Show)
	}
	return out
}

func (f *fakeStore) GetByID(_ context.Context, id string) (*anime_subs.AnimeSubs, error) {
	if _, ok := f.docs[id]; !ok {
		return nil, nil
	}
	return &anime_subs.AnimeSubs{ID: id, Shows: append([]*anime_subs.ShowSub(nil), f.docs[id]...)}, nil
}

func (f *fakeStore) AddShowIfAbsent(_ context.Context, id string, sub *anime_subs.ShowSub) (bool, error) {
	f.calls = append(f.calls, "add")
	for _, s := range f.docs[id] {
		if strings.EqualFold(s.Show, sub.Show) {
			return false, nil
		}
	}
	f.docs[id] = append(f.docs[id], sub)
	return true, nil
}

func (f *fakeStore) RemoveShows(_ context.Context, id string, names []string) error {
	f.calls = append(f.calls, "remove")
	var kept []*anime_subs.ShowSub
	for _, s := range f.docs[id] {
		drop := false
		for _, n := range names {
			if s.Show == n {
				drop = true
			}
		}
		if !drop {
			kept = append(kept, s)
		}
	}
	f.docs[id] = kept
	return nil
}

func (f *fakeStore) ClearShows(_ context.Context, id string) error {
	f.calls = append(f.calls, "clear")
	f.docs[id] = nil
	return nil
}

func withLookup(t *testing.T, titles ...string) {
	t.Helper()
	old := showLookup
	showLookup = func(name string) (animeschedule.ShowEntry, bool) {
		for _, ti := range titles {
			if strings.EqualFold(ti, strings.TrimSpace(name)) {
				return animeschedule.ShowEntry{Name: ti}, true
			}
		}
		return animeschedule.ShowEntry{}, false
	}
	t.Cleanup(func() { showLookup = old })
}

func TestSubCore(t *testing.T) {
	withLookup(t, "Frieren", "Dandadan")
	tests := []struct {
		name      string
		store     *fakeStore
		input     string
		wantMsg   string
		wantNames []string
	}{
		{"new subscription", newFakeStore(), "frieren", "Success!", []string{"Frieren"}},
		{"already subscribed case-insensitive", newFakeStore().with("u", "Frieren"), "FRIEREN", "already subscribed", []string{"Frieren"}},
		{"second show keeps first", newFakeStore().with("u", "Frieren"), "Dandadan", "Success!", []string{"Frieren", "Dandadan"}},
		{"unknown show", newFakeStore(), "nope", "not a valid", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, err := subCore(context.Background(), tt.store, "u", tt.input)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(msg, tt.wantMsg) {
				t.Errorf("msg = %q, want substring %q", msg, tt.wantMsg)
			}
			got := tt.store.names("u")
			if strings.Join(got, ",") != strings.Join(tt.wantNames, ",") {
				t.Errorf("names = %v, want %v", got, tt.wantNames)
			}
		})
	}
}

func TestSubCoreMaxSubscriptions(t *testing.T) {
	withLookup(t, "Frieren")
	st := newFakeStore()
	for i := 0; i < maxUserSubscriptions; i++ {
		st.with("u", "show"+strings.Repeat("x", i))
	}
	msg, err := subCore(context.Background(), st, "u", "Frieren")
	if err != nil || !strings.Contains(msg, "maximum") {
		t.Fatalf("msg=%q err=%v", msg, err)
	}
	if len(st.docs["u"]) != maxUserSubscriptions {
		t.Errorf("subscription added past the limit")
	}
}

func TestUnsubCore(t *testing.T) {
	tests := []struct {
		name      string
		store     *fakeStore
		input     string
		wantMsg   string
		wantNames []string
		wantCalls []string
	}{
		{"resolves stored title case-insensitively", newFakeStore().with("u", "Frieren", "Dandadan"), "frieren", "unsubscribed", []string{"Dandadan"}, []string{"remove"}},
		{"not subscribed", newFakeStore().with("u", "Dandadan"), "Frieren", "not subscribed", []string{"Dandadan"}, nil},
		{"no document", newFakeStore(), "Frieren", "not subscribed", nil, nil},
		{"empty input", newFakeStore(), "  ", "provide an anime", nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, err := unsubCore(context.Background(), tt.store, "u", tt.input)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(msg, tt.wantMsg) {
				t.Errorf("msg = %q, want substring %q", msg, tt.wantMsg)
			}
			if got := tt.store.names("u"); strings.Join(got, ",") != strings.Join(tt.wantNames, ",") {
				t.Errorf("names = %v, want %v", got, tt.wantNames)
			}
			if strings.Join(tt.store.calls, ",") != strings.Join(tt.wantCalls, ",") {
				t.Errorf("calls = %v, want %v", tt.store.calls, tt.wantCalls)
			}
		})
	}
}

func TestClearsubsCore(t *testing.T) {
	tests := []struct {
		name      string
		store     *fakeStore
		wantMsg   string
		wantCalls []string
	}{
		{"clears all", newFakeStore().with("u", "A", "B"), "cleared", []string{"clear"}},
		{"nothing to clear", newFakeStore(), "no active", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, err := clearsubsCore(context.Background(), tt.store, "u")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(msg, tt.wantMsg) {
				t.Errorf("msg = %q, want %q", msg, tt.wantMsg)
			}
			if len(tt.store.docs["u"]) != 0 {
				t.Errorf("shows remain: %v", tt.store.names("u"))
			}
			if strings.Join(tt.store.calls, ",") != strings.Join(tt.wantCalls, ",") {
				t.Errorf("calls = %v, want %v", tt.store.calls, tt.wantCalls)
			}
		})
	}
}

// A legacy stored entry with stray surrounding space is the same show to the notifier, so /sub must treat it as
// already subscribed instead of adding a duplicate.
func TestSubCoreLegacyTrimmedEntryIsAlreadySubscribed(t *testing.T) {
	withLookup(t, "Frieren")
	st := newFakeStore().with("u", "Frieren ")
	msg, err := subCore(context.Background(), st, "u", "Frieren")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "already subscribed") {
		t.Errorf("msg = %q, want already subscribed", msg)
	}
	if len(st.calls) != 0 || len(st.docs["u"]) != 1 {
		t.Errorf("calls=%v names=%v, want no add", st.calls, st.names("u"))
	}
}

// /unsub must be able to remove a legacy entry that the notifier still serves.
func TestUnsubCoreRemovesLegacyTrimmedEntry(t *testing.T) {
	st := newFakeStore().with("u", "Frieren ", "Dandadan")
	msg, err := unsubCore(context.Background(), st, "u", "frieren")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "unsubscribed") {
		t.Errorf("msg = %q, want unsubscribed", msg)
	}
	if got := st.names("u"); strings.Join(got, ",") != "Dandadan" {
		t.Errorf("names = %v, want [Dandadan]", got)
	}
}

func TestFindShowIn(t *testing.T) {
	shows := []animeschedule.ShowEntry{{Name: "Frieren "}, {Name: "Dandadan"}}
	tests := []struct {
		name, input, want string
		ok                bool
	}{
		{"schedule name with trailing space", "frieren", "Frieren ", true},
		{"input with surrounding space", "  DANDADAN ", "Dandadan", true},
		{"unknown", "nope", "", false},
		{"blank", "   ", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := findShowIn(shows, tt.input)
			if ok != tt.ok || got.Name != tt.want {
				t.Errorf("findShowIn(%q) = %q, %v; want %q, %v", tt.input, got.Name, ok, tt.want, tt.ok)
			}
		})
	}
}
