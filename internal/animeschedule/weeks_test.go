package animeschedule

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// timetableServer serves canned weeks keyed "year-week" (a body string, or an int status for a failure) and
// records each request's query. It points the client override at itself.
type timetableServer struct {
	mu    sync.Mutex
	weeks map[string]any
	reqs  []url.Values
}

func newTimetableServer(t *testing.T, weeks map[string]any) *timetableServer {
	t.Helper()
	ts := &timetableServer{weeks: weeks}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		ts.mu.Lock()
		ts.reqs = append(ts.reqs, q)
		resp, ok := ts.weeks[q.Get("year")+"-"+q.Get("week")]
		ts.mu.Unlock()
		if code, isCode := resp.(int); isCode {
			w.WriteHeader(code)
			return
		}
		if !ok {
			resp = "[]"
		}
		_, _ = io.WriteString(w, resp.(string))
	}))
	t.Cleanup(srv.Close)
	target, _ := url.Parse(srv.URL)
	old := newClient
	newClient = func() *http.Client {
		return &http.Client{Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
			r.URL.Scheme, r.URL.Host = target.Scheme, target.Host
			return http.DefaultTransport.RoundTrip(r)
		})}
	}
	t.Cleanup(func() { newClient = old })
	return ts
}

func (s *timetableServer) requested() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, q := range s.reqs {
		out = append(out, q.Get("year")+"-"+q.Get("week")+"-"+q.Get("tz"))
	}
	sort.Strings(out)
	return out
}

func entryJSON(route, at string) string {
	return `{"title":"` + route + `","route":"` + route + `","airType":"sub","episodeNumber":1,"episodeDate":"` + at + `"}`
}

func resetTimetable(t *testing.T) {
	t.Helper()
	t.Cleanup(Seed(map[string][]ShowEntry{}))
	timetableMu.Lock()
	timetablePopulated = false
	timetableMu.Unlock()
}

func at(t time.Time) func() {
	return SetClock(func() time.Time { return t })
}

func dayNames(date string) string {
	day, _ := time.Parse("2006-01-02", date)
	var out []string
	for _, e := range GetDayShows(day, true, PreferSub) {
		out = append(out, e.Name)
	}
	return strings.Join(out, ",")
}

func TestRefreshRequestsThreeISOWeeksInUTC(t *testing.T) {
	tests := []struct {
		name string
		now  time.Time
		want []string
	}{
		{"mid year", time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC),
			[]string{"2026-44-Etc/UTC", "2026-45-Etc/UTC", "2026-46-Etc/UTC"}},
		// 2026 has 53 ISO weeks; 2027-01-04 is the Monday of 2027 week 1.
		{"into next year", time.Date(2026, 12, 30, 12, 0, 0, 0, time.UTC),
			[]string{"2026-52-Etc/UTC", "2026-53-Etc/UTC", "2027-1-Etc/UTC"}},
		{"from new year", time.Date(2027, 1, 5, 12, 0, 0, 0, time.UTC),
			[]string{"2026-53-Etc/UTC", "2027-1-Etc/UTC", "2027-2-Etc/UTC"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resetTimetable(t)
			defer at(tc.now)()
			srv := newTimetableServer(t, nil)
			UpdateAnimeSchedule(context.Background(), "key")
			if got := strings.Join(srv.requested(), ","); got != strings.Join(tc.want, ",") {
				t.Errorf("requests = %s, want %v", got, tc.want)
			}
		})
	}
}

func TestRefreshKeepsLateSundayEntryOnItsSundayAndDropsStrays(t *testing.T) {
	resetTimetable(t)
	defer at(time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC))()
	// Week 45 runs 2026-11-02 to 2026-11-08. The first entry carries a +01:00 offset but is 23:30 UTC on Sunday;
	// the second is the previous Sunday and belongs to no loaded date.
	newTimetableServer(t, map[string]any{
		"2026-45": "[" + entryJSON("late", "2026-11-09T00:30:00+01:00") + "," + entryJSON("stray", "2026-11-01T23:30:00Z") + "]",
	})
	UpdateAnimeSchedule(context.Background(), "key")
	if got := dayNames("2026-11-08"); got != "late" {
		t.Errorf("Sunday = %q, want late", got)
	}
	if got := dayNames("2026-11-01"); got != "" {
		t.Errorf("previous Sunday = %q, want none", got)
	}
	if n := len(AllEntries()); n != 1 {
		t.Errorf("AllEntries = %d, want 1", n)
	}
}

func TestRefreshReadsAllThreeWeeksAndAppliesSubOverRawAndDubDropping(t *testing.T) {
	resetTimetable(t)
	defer at(time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC))()
	raw := `{"title":"dual","route":"dual","airType":"raw","episodeNumber":1,"episodeDate":"2026-11-10T10:00:00Z"}`
	dub := `{"title":"dubbed","route":"dubbed","airType":"dub","episodeNumber":1,"episodeDate":"2026-11-10T11:00:00Z"}`
	newTimetableServer(t, map[string]any{
		"2026-44": "[" + entryJSON("last", "2026-10-28T10:00:00Z") + "]",
		"2026-45": "[" + entryJSON("this", "2026-11-04T10:00:00Z") + "]",
		"2026-46": "[" + raw + "," + entryJSON("dual", "2026-11-10T10:00:00Z") + "," + dub + "]",
	})
	UpdateAnimeSchedule(context.Background(), "key")
	for date, want := range map[string]string{"2026-10-28": "last", "2026-11-04": "this", "2026-11-10": "dual"} {
		if got := dayNames(date); got != want {
			t.Errorf("%s = %q, want %q", date, got, want)
		}
	}
	day := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	if got := GetDayShows(day, true, PreferSub); got[0].Track != "sub" {
		t.Errorf("2026-11-10 = %+v, want the sub", got[0])
	}
}

func TestFailedWeekKeepsItsPreviousEntries(t *testing.T) {
	resetTimetable(t)
	defer at(time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC))()
	srv := newTimetableServer(t, map[string]any{
		"2026-44": "[" + entryJSON("last", "2026-10-28T10:00:00Z") + "]",
		"2026-45": "[" + entryJSON("this", "2026-11-04T10:00:00Z") + "]",
		"2026-46": "[" + entryJSON("next", "2026-11-10T10:00:00Z") + "]",
	})
	UpdateAnimeSchedule(context.Background(), "key")

	srv.mu.Lock()
	srv.weeks["2026-44"] = http.StatusInternalServerError
	srv.weeks["2026-45"] = "[" + entryJSON("this2", "2026-11-05T10:00:00Z") + "]"
	srv.weeks["2026-46"] = "not json"
	srv.mu.Unlock()
	UpdateAnimeSchedule(context.Background(), "key")

	if got := dayNames("2026-10-28"); got != "last" {
		t.Errorf("non-200 last week = %q, want last", got)
	}
	if got := dayNames("2026-11-10"); got != "next" {
		t.Errorf("undecodable next week = %q, want next", got)
	}
	if dayNames("2026-11-04") != "" || dayNames("2026-11-05") != "this2" {
		t.Errorf("this week = %q / %q, want the refreshed entries", dayNames("2026-11-04"), dayNames("2026-11-05"))
	}
}

func TestSnapshotWeeksCountsAWeekLoadedOnlyAfterItsOwnFetch(t *testing.T) {
	resetTimetable(t)
	restore := at(time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC))
	defer func() { restore() }()
	srv := newTimetableServer(t, map[string]any{
		"2026-45": "[" + entryJSON("this", "2026-11-04T10:00:00Z") + "]",
		"2026-46": "[" + entryJSON("next", "2026-11-10T10:00:00Z") + "]",
	})
	UpdateAnimeSchedule(context.Background(), "key")
	if s := SnapshotWeeks(); !s.ThisLoaded || !s.NextLoaded {
		t.Fatalf("after a full fetch: this=%v next=%v, want both loaded", s.ThisLoaded, s.NextLoaded)
	}

	// Two weeks later every fetch fails: the old entries stay, but the new weeks were never fetched.
	restore()
	restore = at(time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC))
	srv.mu.Lock()
	srv.weeks["2026-47"] = http.StatusInternalServerError
	srv.weeks["2026-48"] = http.StatusInternalServerError
	srv.weeks["2026-49"] = http.StatusInternalServerError
	srv.mu.Unlock()
	UpdateAnimeSchedule(context.Background(), "key")
	if s := SnapshotWeeks(); s.ThisLoaded || s.NextLoaded {
		t.Errorf("after failed fetches: this=%v next=%v, want neither loaded", s.ThisLoaded, s.NextLoaded)
	}
}

func TestLoadedRangeRunsLastMondayToNextSunday(t *testing.T) {
	defer at(time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC))()
	first, last := LoadedRange()
	if DateKey(first) != "2026-10-26" || DateKey(last) != "2026-11-15" {
		t.Errorf("range = %s..%s, want 2026-10-26..2026-11-15", DateKey(first), DateKey(last))
	}
}

func TestRateLimitedRequestWaitsForResetAndRetriesOnce(t *testing.T) {
	resetTimetable(t)
	defer at(time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC))()
	var calls int32
	old := newClient
	newClient = func() *http.Client {
		return &http.Client{Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
			status, body := http.StatusOK, "[]"
			h := http.Header{}
			if r.URL.Query().Get("week") == "45" && atomic.AddInt32(&calls, 1) == 1 {
				status, body = http.StatusTooManyRequests, ""
				h.Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(time.Second).Unix(), 10))
			}
			return &http.Response{StatusCode: status, Header: h, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
		})}
	}
	defer func() { newClient = old }()
	UpdateAnimeSchedule(context.Background(), "key")
	if calls != 2 {
		t.Errorf("week 45 requests = %d, want 2 (429 then one retry)", calls)
	}
}
