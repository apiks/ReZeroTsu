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
		if s.Matches(sub.Route, sub.Show) {
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

func (f *fakeStore) SetShowTracks(_ context.Context, id, showName, tracks string, marks []anime_subs.Mark) error {
	f.calls = append(f.calls, "settracks")
	for _, s := range f.docs[id] {
		if s.Show == showName {
			s.Tracks = tracks
			// Like Mongo, only a mark with an air time sets notified; a zero mark just clears the memory.
			for _, m := range marks {
				notified := s.Notified || m.AirUnix != 0
				s.Remember(m)
				s.Notified = notified
			}
		}
	}
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
	showLookup = func(name string, _ bool) (animeschedule.ShowEntry, bool) {
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
		{"new subscription", newFakeStore(), "frieren", "Subscribed to", []string{"Frieren"}},
		{"already subscribed case-insensitive", newFakeStore().with("u", "Frieren"), "FRIEREN", "already subscribed", []string{"Frieren"}},
		{"second show keeps first", newFakeStore().with("u", "Frieren"), "Dandadan", "Subscribed to", []string{"Frieren", "Dandadan"}},
		{"unknown show", newFakeStore(), "nope", "could not find", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, _, err := subCore(context.Background(), tt.store, "u", tt.input, animeschedule.SubscribeSub, false)
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
	msg, _, err := subCore(context.Background(), st, "u", "Frieren", animeschedule.SubscribeSub, false)
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

// A legacy stored entry with stray surrounding space is the same show to the notifier, so /sub must treat it as
// already subscribed instead of adding a duplicate.
func TestSubCoreLegacyTrimmedEntryIsAlreadySubscribed(t *testing.T) {
	withLookup(t, "Frieren")
	st := newFakeStore().with("u", "Frieren ")
	msg, _, err := subCore(context.Background(), st, "u", "Frieren", animeschedule.SubscribeSub, false)
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

// An autocomplete choice carries the title cut to 100 characters, so remove must resolve a stored title by its
// clipped form like the Remove picker does.
func TestUnsubCoreResolvesClippedLongTitle(t *testing.T) {
	long := strings.Repeat("A long title ", 10)
	st := newFakeStore().with("u", long, "Dandadan")
	msg, err := unsubCore(context.Background(), st, "u", clip(long))
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

func TestUnsubCoreAmbiguousClippedTitleRemovesNone(t *testing.T) {
	prefix := strings.Repeat("A long title ", 10)
	a, b := prefix+"Season 1", prefix+"Season 2"
	st := newFakeStore().with("u", a, b)
	msg, err := unsubCore(context.Background(), st, "u", clip(a))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "more than one") || !strings.Contains(msg, "Season 1") || !strings.Contains(msg, "Season 2") {
		t.Errorf("msg = %q, want the matching titles named", msg)
	}
	if len(st.calls) != 0 || len(st.docs["u"]) != 2 {
		t.Errorf("calls=%v names=%v, want nothing removed", st.calls, st.names("u"))
	}
}

func TestFindShowInResolvesAnyOfTheFourNamesAndPrefersTheTitle(t *testing.T) {
	shows := []animeschedule.ShowEntry{
		{Name: "Other", English: "Frieren"},
		{Name: "Frieren", Romaji: "Sousou no Frieren"},
		{Name: "Third", Native: "ダンダダン"},
	}
	for input, want := range map[string]string{"frieren": "Frieren", "SOUSOU NO FRIEREN": "Frieren", "ダンダダン": "Third"} {
		if got, ok := findShowIn(shows, input); !ok || got.Name != want {
			t.Errorf("findShowIn(%q) = %q, %v; want %q", input, got.Name, ok, want)
		}
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

// The already-aired check must use today's entry for the show, matched the way the notifier matches names, so a
// show listed on several weekdays or with a trim-variant spelling seeds the right air time.
func TestAiredTodayEntry(t *testing.T) {
	const now = 1000
	today := []animeschedule.ShowEntry{
		{Name: "Frieren", AirTimeUnix: 900},
		{Name: "Dandadan", AirTimeUnix: 1500},
	}
	tests := []struct {
		name     string
		show     string
		wantUnix int64
		ok       bool
	}{
		{"aired today", "Frieren", 900, true},
		{"lookup spelling differs by surrounding space", "Frieren ", 900, true},
		{"case differs", "frieren", 900, true},
		{"not aired yet", "Dandadan", 0, false},
		{"not on today's list", "Other", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, ok := airedTodayEntry(today, "", tt.show, now)
			if ok != tt.ok || (ok && e.AirTimeUnix != tt.wantUnix) {
				t.Errorf("airedTodayEntry(%q) = %d, %v; want %d, %v", tt.show, e.AirTimeUnix, ok, tt.wantUnix, tt.ok)
			}
		})
	}
}

func TestSubCoreStoresTheResolvedShowRoute(t *testing.T) {
	old := showLookup
	showLookup = func(string, bool) (animeschedule.ShowEntry, bool) {
		return animeschedule.ShowEntry{Name: "Frieren", Route: "sousou-no-frieren"}, true
	}
	t.Cleanup(func() { showLookup = old })
	st := newFakeStore()
	if _, _, err := subCore(context.Background(), st, "u", "frieren", animeschedule.SubscribeSub, false); err != nil {
		t.Fatal(err)
	}
	if got := st.docs["u"]; len(got) != 1 || got[0].Route != "sousou-no-frieren" {
		t.Fatalf("stored subscription = %+v, want route sousou-no-frieren", got)
	}
}
