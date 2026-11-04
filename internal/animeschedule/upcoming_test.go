package animeschedule

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// upcomingFixture serves AnimeSchedule's anime list from canned pages and records what it was asked.
type upcomingFixture struct {
	mu       sync.Mutex
	pages    []string // JSON array body of "anime" per page
	total    int
	headers  map[int]http.Header // extra headers per 1-based page
	statuses map[int][]int       // status codes to serve per 1-based page, consumed in order
	requests []*http.Request
}

func (f *upcomingFixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, r)
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	for k, v := range f.headers[page] {
		w.Header()[k] = v
	}
	if q := f.statuses[page]; len(q) > 0 {
		f.statuses[page] = q[1:]
		if q[0] != http.StatusOK {
			w.WriteHeader(q[0])
			return
		}
	}
	if page < 1 || page > len(f.pages) {
		fmt.Fprintf(w, `{"anime":[],"page":%d,"totalAmount":%d}`, page, f.total)
		return
	}
	fmt.Fprintf(w, `{"anime":%s,"page":%d,"totalAmount":%d}`, f.pages[page-1], page, f.total)
}

// useUpcomingServer points the upcoming fetch at f, records waits instead of sleeping, and restores everything.
func useUpcomingServer(t *testing.T, f *upcomingFixture) (waits *[]time.Duration) {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	oldURL, oldGap, oldSleep := upcomingURL, upcomingPageGap, upcomingSleep
	t.Cleanup(func() { upcomingURL, upcomingPageGap, upcomingSleep = oldURL, oldGap, oldSleep })
	upcomingURL = srv.URL
	upcomingPageGap = 3 * time.Second
	waits = new([]time.Duration)
	upcomingSleep = func(ctx context.Context, d time.Duration) error {
		*waits = append(*waits, d)
		return ctx.Err()
	}
	t.Cleanup(Seed(map[string][]ShowEntry{}))
	t.Cleanup(SeedUpcoming(nil, false))
	return waits
}

func item(title, route, premier, mediaRoute string) string {
	return fmt.Sprintf(`{"title":%q,"route":%q,"names":{"romaji":%q,"english":%q,"native":"N-%s"},"premier":%q,"mediaTypes":[{"name":"x","route":%q}]}`,
		title, route, "R-"+title, "E-"+title, title, premier, mediaRoute)
}

const zeroTime = "0001-01-01T00:00:00Z"

func TestRefreshUpcomingReadsEveryPageIntoShows(t *testing.T) {
	f := &upcomingFixture{total: 5, pages: []string{
		"[" + item("A", "a", "2027-01-10T15:00:00Z", "tv") + "," + item("B", "b", zeroTime, "ona-chinese") + "]",
		"[" + item("C", "c", zeroTime, "movie") + "," + item("D", "d", zeroTime, "tv-short") + "]",
		"[" + item("E", "e", zeroTime, "mystery") + "]",
	}}
	waits := useUpcomingServer(t, f)

	if UpcomingLoaded() {
		t.Fatal("list reports loaded before any refresh")
	}
	if err := RefreshUpcoming(context.Background(), "key"); err != nil {
		t.Fatal(err)
	}
	if !UpcomingLoaded() {
		t.Fatal("list not loaded after a successful refresh")
	}
	got := UpcomingShows()
	if len(got) != 5 {
		t.Fatalf("shows = %d, want 5", len(got))
	}
	a := got[0]
	want := time.Date(2027, 1, 10, 15, 0, 0, 0, time.UTC)
	if a.Name != "A" || a.Route != "a" || a.Romaji != "R-A" || a.English != "E-A" || a.Native != "N-A" || !a.Premiere.Equal(want) || a.MediaType != MediaTV {
		t.Errorf("first show = %+v", a)
	}
	wantTypes := []MediaType{MediaTV, MediaONA, MediaMovie, MediaTVShort, MediaTV}
	for i, w := range wantTypes {
		if got[i].MediaType != w {
			t.Errorf("show %d media type = %v, want %v", i, got[i].MediaType, w)
		}
	}
	if !got[1].Premiere.IsZero() {
		t.Errorf("zero premiere read as %v, want empty", got[1].Premiere)
	}
	if len(f.requests) != 3 {
		t.Fatalf("requests = %d, want 3 pages", len(f.requests))
	}
	r := f.requests[0]
	if r.URL.Query().Get("airing-statuses") != "upcoming" || r.Header.Get("Authorization") != "Bearer key" {
		t.Errorf("request = %s auth %q", r.URL, r.Header.Get("Authorization"))
	}
	if len(*waits) != 2 || (*waits)[0] != 3*time.Second {
		t.Errorf("waits between pages = %v, want two page gaps", *waits)
	}
}

func TestRefreshUpcomingWaitsForResetWhenNoRequestsRemain(t *testing.T) {
	reset := time.Now().Add(40 * time.Second).Unix()
	f := &upcomingFixture{total: 2, pages: []string{
		"[" + item("A", "a", zeroTime, "tv") + "]",
		"[" + item("B", "b", zeroTime, "tv") + "]",
	}, headers: map[int]http.Header{1: {
		"X-Ratelimit-Remaining": {"0"}, "X-Ratelimit-Reset": {strconv.FormatInt(reset, 10)},
	}}}
	waits := useUpcomingServer(t, f)

	if err := RefreshUpcoming(context.Background(), "key"); err != nil {
		t.Fatal(err)
	}
	if len(*waits) != 1 || (*waits)[0] < 30*time.Second || (*waits)[0] > 41*time.Second {
		t.Errorf("waits = %v, want one wait until the reset about 40s away", *waits)
	}
	if len(UpcomingShows()) != 2 {
		t.Errorf("shows = %v", UpcomingShows())
	}
}

func TestRefreshUpcomingRetriesAPageAfter429(t *testing.T) {
	reset := time.Now().Add(20 * time.Second).Unix()
	f := &upcomingFixture{total: 1, pages: []string{"[" + item("A", "a", zeroTime, "tv") + "]"},
		statuses: map[int][]int{1: {http.StatusTooManyRequests, http.StatusOK}},
		headers:  map[int]http.Header{1: {"X-Ratelimit-Reset": {strconv.FormatInt(reset, 10)}}}}
	waits := useUpcomingServer(t, f)

	if err := RefreshUpcoming(context.Background(), "key"); err != nil {
		t.Fatal(err)
	}
	if len(f.requests) != 2 {
		t.Errorf("requests = %d, want the page asked twice", len(f.requests))
	}
	if len(*waits) != 1 || (*waits)[0] < 10*time.Second {
		t.Errorf("waits = %v, want a wait until reset before the retry", *waits)
	}
	if len(UpcomingShows()) != 1 {
		t.Errorf("shows = %v", UpcomingShows())
	}
}

func TestFailedRefreshKeepsTheLastGoodList(t *testing.T) {
	f := &upcomingFixture{total: 2, pages: []string{
		"[" + item("A", "a", zeroTime, "tv") + "]",
		"[" + item("B", "b", zeroTime, "tv") + "]",
	}}
	useUpcomingServer(t, f)
	if err := RefreshUpcoming(context.Background(), "key"); err != nil {
		t.Fatal(err)
	}

	// The next refresh fails on its second page.
	f.mu.Lock()
	f.statuses = map[int][]int{2: {http.StatusInternalServerError}}
	f.mu.Unlock()
	if err := RefreshUpcoming(context.Background(), "key"); err == nil {
		t.Fatal("expected an error from the failed refresh")
	}
	if !UpcomingLoaded() || len(UpcomingShows()) != 2 {
		t.Errorf("after failed refresh: loaded=%v shows=%v, want the last good list", UpcomingLoaded(), UpcomingShows())
	}
}

func TestUpcomingNotLoadedUntilFirstSuccess(t *testing.T) {
	f := &upcomingFixture{total: 1, pages: []string{"[" + item("A", "a", zeroTime, "tv") + "]"},
		statuses: map[int][]int{1: {http.StatusInternalServerError}}}
	useUpcomingServer(t, f)
	if err := RefreshUpcoming(context.Background(), "key"); err == nil {
		t.Fatal("expected an error")
	}
	if UpcomingLoaded() || len(UpcomingShows()) != 0 {
		t.Errorf("loaded=%v shows=%v, want not loaded and empty", UpcomingLoaded(), UpcomingShows())
	}
}

func TestRefreshUpcomingWithoutATotalReadsUntilAnEmptyPage(t *testing.T) {
	f := &upcomingFixture{total: 0, pages: []string{
		"[" + item("A", "a", zeroTime, "tv") + "]",
		"[" + item("B", "b", zeroTime, "tv") + "]",
	}}
	useUpcomingServer(t, f)
	if err := RefreshUpcoming(context.Background(), "key"); err != nil {
		t.Fatal(err)
	}
	if got := len(UpcomingShows()); got != 2 {
		t.Errorf("shows = %d, want both pages read despite the missing total", got)
	}
}

func TestRefreshUpcomingShortOfTheTotalFailsAndKeepsTheLastGoodList(t *testing.T) {
	f := &upcomingFixture{total: 1, pages: []string{"[" + item("A", "a", zeroTime, "tv") + "]"}}
	useUpcomingServer(t, f)
	if err := RefreshUpcoming(context.Background(), "key"); err != nil {
		t.Fatal(err)
	}
	// The next refresh ends on an empty page with 1 of 3 shows read.
	f.mu.Lock()
	f.total = 3
	f.mu.Unlock()
	if err := RefreshUpcoming(context.Background(), "key"); err == nil {
		t.Fatal("expected an error for a list shorter than totalAmount")
	}
	if got := UpcomingShows(); len(got) != 1 {
		t.Errorf("shows = %v, want the last good list", got)
	}
}

func TestRefreshUpcomingHittingThePageCapFails(t *testing.T) {
	f := &upcomingFixture{total: 0}
	for range upcomingMaxPages + 1 {
		f.pages = append(f.pages, "["+item("A", "a", zeroTime, "tv")+"]")
	}
	useUpcomingServer(t, f)
	if err := RefreshUpcoming(context.Background(), "key"); err == nil {
		t.Error("expected an error when the page cap ends the read")
	}
	if UpcomingLoaded() {
		t.Error("a partial list was stored as loaded")
	}
}

func TestRefreshUpcomingWaitsWhenNoRequestsRemainAndTheResetIsUnusable(t *testing.T) {
	tests := []struct {
		name   string
		header http.Header
		want   time.Duration
	}{
		{"missing reset", http.Header{"X-Ratelimit-Remaining": {"0"}}, time.Minute},
		{"unparsable reset", http.Header{"X-Ratelimit-Remaining": {"0"}, "X-Ratelimit-Reset": {"soon"}}, time.Minute},
		{"reset too far away", http.Header{"X-Ratelimit-Remaining": {"0"}, "X-Ratelimit-Reset": {strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)}}, time.Minute},
		{"reset already passed", http.Header{"X-Ratelimit-Remaining": {"0"}, "X-Ratelimit-Reset": {strconv.FormatInt(time.Now().Add(-time.Hour).Unix(), 10)}}, 3 * time.Second},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := &upcomingFixture{total: 2, headers: map[int]http.Header{1: tc.header}, pages: []string{
				"[" + item("A", "a", zeroTime, "tv") + "]",
				"[" + item("B", "b", zeroTime, "tv") + "]",
			}}
			waits := useUpcomingServer(t, f)
			if err := RefreshUpcoming(context.Background(), "key"); err != nil {
				t.Fatal(err)
			}
			if len(*waits) != 1 || (*waits)[0] != tc.want {
				t.Errorf("waits = %v, want one wait of %v", *waits, tc.want)
			}
		})
	}
}

func TestUpcomingLoopRetriesSoonAfterAFailedLoadThenGoesDaily(t *testing.T) {
	f := &upcomingFixture{total: 1, pages: []string{"[" + item("A", "a", zeroTime, "tv") + "]"},
		statuses: map[int][]int{1: {http.StatusInternalServerError}}}
	waits := useUpcomingServer(t, f)
	// The timeout only bounds a loop that ignores upcomingSleep.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	upcomingSleep = func(ctx context.Context, d time.Duration) error {
		*waits = append(*waits, d)
		if len(*waits) == 3 {
			cancel()
		}
		return ctx.Err()
	}
	RunUpcomingLoop(ctx, "key")
	want := []time.Duration{upcomingStartDelay, 15 * time.Minute, upcomingRefreshEvery}
	if fmt.Sprint(*waits) != fmt.Sprint(want) {
		t.Errorf("waits = %v, want %v", *waits, want)
	}
	if !UpcomingLoaded() {
		t.Error("list not loaded after the retry")
	}
}

func TestSeedUpcomingSeedsAndRestores(t *testing.T) {
	restore := SeedUpcoming([]UpcomingShow{{Name: "Soon", Route: "soon"}}, true)
	if !UpcomingLoaded() || len(UpcomingShows()) != 1 || UpcomingShows()[0].Route != "soon" {
		t.Errorf("seeded: loaded=%v shows=%v", UpcomingLoaded(), UpcomingShows())
	}
	restore()
	if UpcomingLoaded() || len(UpcomingShows()) != 0 {
		t.Errorf("restored: loaded=%v shows=%v", UpcomingLoaded(), UpcomingShows())
	}
}

func TestUpcomingLoopLoadsAfterStartupThenDailyAndStops(t *testing.T) {
	f := &upcomingFixture{total: 1, pages: []string{"[" + item("A", "a", zeroTime, "tv") + "]"}}
	useUpcomingServer(t, f)
	upcomingSleep = sleepCtx
	oldDelay, oldEvery := upcomingStartDelay, upcomingRefreshEvery
	defer func() { upcomingStartDelay, upcomingRefreshEvery = oldDelay, oldEvery }()
	upcomingStartDelay, upcomingRefreshEvery = 10*time.Millisecond, 40*time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { RunUpcomingLoop(ctx, "key"); close(done) }()

	count := func() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.requests) }
	deadline := time.Now().Add(2 * time.Second)
	for count() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if count() < 2 || !UpcomingLoaded() {
		t.Fatalf("requests=%d loaded=%v, want a startup load and a repeat", count(), UpcomingLoaded())
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("loop did not stop on shutdown")
	}
}

func TestUpcomingShowsDecodeShape(t *testing.T) {
	var p upcomingPage
	if err := json.Unmarshal([]byte(`{"anime":[`+item("A", "a", zeroTime, "tv")+`],"page":1,"totalAmount":1}`), &p); err != nil || len(p.Anime) != 1 {
		t.Fatalf("decode = %+v, %v", p, err)
	}
}

// itemWithRoutes is an upcoming item whose mediaTypes list holds the given routes in order; none gives no list.
func itemWithRoutes(title string, routes ...string) string {
	refs := make([]string, len(routes))
	for i, r := range routes {
		refs[i] = fmt.Sprintf(`{"name":"x","route":%q}`, r)
	}
	return fmt.Sprintf(`{"title":%q,"route":%q,"premier":%q,"mediaTypes":[%s]}`, title, title, zeroTime, strings.Join(refs, ","))
}

func TestRefreshUpcomingRecordsWhichShowsAreDonghuaFromAllTheirMediaTypeRoutes(t *testing.T) {
	cases := []struct {
		name   string
		routes []string
		want   bool
	}{
		{"tv-chinese", []string{"tv-chinese"}, true},
		{"tv-chinese-short", []string{"tv-chinese-short"}, true},
		{"ona-chinese", []string{"ona-chinese"}, true},
		{"ova-chinese", []string{"ova-chinese"}, true},
		{"special-chinese", []string{"special-chinese"}, true},
		{"movie-chinese", []string{"movie-chinese"}, true},
		{"only a later route is a donghua route", []string{"ona", "tv", "movie-chinese"}, true},
		{"a route in another case", []string{" TV-Chinese "}, true},
		{"non-donghua show", []string{"tv", "ona"}, false},
		{"no media types", nil, false},
	}
	var items []string
	for _, c := range cases {
		items = append(items, itemWithRoutes(c.name, c.routes...))
	}
	useUpcomingServer(t, &upcomingFixture{total: len(items), pages: []string{"[" + strings.Join(items, ",") + "]"}})
	if err := RefreshUpcoming(context.Background(), "key"); err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, u := range UpcomingShows() {
		got[u.Name] = u.Donghua
	}
	for _, c := range cases {
		d, ok := got[c.name]
		if !ok {
			t.Errorf("%s: show missing from the upcoming list", c.name)
		} else if d != c.want {
			t.Errorf("%s: Donghua = %v, want %v", c.name, d, c.want)
		}
	}
}

func TestRefreshUpcomingDecodesDonghuaRoutesToTheirBaseType(t *testing.T) {
	cases := []struct {
		name   string
		routes []string
		want   MediaType
	}{
		{"tv-chinese", []string{"tv-chinese"}, MediaTV},
		{"tv-chinese-short", []string{"tv-chinese-short"}, MediaTVShort},
		{"ona-chinese", []string{"ona-chinese"}, MediaONA},
		{"ova-chinese", []string{"ova-chinese"}, MediaOVA},
		{"special-chinese", []string{"special-chinese"}, MediaSpecial},
		{"movie-chinese", []string{"movie-chinese"}, MediaMovie},
		{"first recognised wins", []string{"weird", "ova-chinese", "movie"}, MediaOVA},
		{"japanese movie", []string{"movie"}, MediaMovie},
		{"no media types", nil, MediaTV},
	}
	var items []string
	for _, c := range cases {
		items = append(items, itemWithRoutes(c.name, c.routes...))
	}
	useUpcomingServer(t, &upcomingFixture{total: len(items), pages: []string{"[" + strings.Join(items, ",") + "]"}})
	if err := RefreshUpcoming(context.Background(), "key"); err != nil {
		t.Fatal(err)
	}
	got := map[string]MediaType{}
	for _, u := range UpcomingShows() {
		got[u.Name] = u.MediaType
	}
	for _, c := range cases {
		mt, ok := got[c.name]
		if !ok {
			t.Errorf("%s: show missing from the upcoming list", c.name)
		} else if mt != c.want {
			t.Errorf("%s: media type = %v, want %v", c.name, mt, c.want)
		}
	}
}
