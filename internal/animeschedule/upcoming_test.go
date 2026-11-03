package animeschedule

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
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
	wantTypes := []MediaType{MediaTV, MediaDonghua, MediaMovie, MediaTVShort, MediaTV}
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
