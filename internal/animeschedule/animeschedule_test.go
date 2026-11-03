package animeschedule

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestProcessTimetableBucketsByUTCDate(t *testing.T) {
	tests := []struct {
		name        string
		episodeDate string
		wantDate    string
	}{
		// 2025-01-06T05:00+09:00 = 2025-01-05T20:00Z.
		{"positive offset previous UTC day", "2025-01-06T05:00:00+09:00", "2025-01-05"},
		// 2025-01-06T22:00-05:00 = 2025-01-07T03:00Z.
		{"negative offset next UTC day", "2025-01-06T22:00:00-05:00", "2025-01-07"},
		{"UTC unchanged", "2025-01-06T12:00:00Z", "2025-01-06"},
		// The same weekday a week earlier is a different date, so it no longer shares a bucket.
		{"previous week same weekday", "2024-12-30T12:00:00Z", "2024-12-30"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ts, err := time.Parse(time.RFC3339, tc.episodeDate)
			if err != nil {
				t.Fatal(err)
			}
			got := processTimetable([]TimetableAnime{{Title: "Show", Route: "show", AirType: "sub", EpisodeDate: ts}})
			if len(got[tc.wantDate]) != 1 || len(got) != 1 {
				t.Fatalf("expected one entry under %s, got %v", tc.wantDate, got)
			}
		})
	}
}

func TestTimetableDecodesAllFourNames(t *testing.T) {
	body := `[{"title":"Sousou no Frieren","romaji":"Sousou no Frieren","english":"Frieren: Beyond Journey's End","native":"葬送のフリーレン","route":"frieren","airType":"sub","episodeDate":"2025-01-06T12:00:00Z"}]`
	var list []TimetableAnime
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatal(err)
	}
	got := processTimetable(list)["2025-01-06"]
	if len(got) != 1 {
		t.Fatalf("entries = %v", got)
	}
	e := got[0]
	if e.Name != "Sousou no Frieren" || e.Romaji != "Sousou no Frieren" || e.English != "Frieren: Beyond Journey's End" || e.Native != "葬送のフリーレン" {
		t.Errorf("entry = %+v", e)
	}
}

func TestAllNamesIgnoresEmptyAndRepeatedNames(t *testing.T) {
	e := ShowEntry{Name: "Show", Romaji: " ", English: "Show", Native: "ショー"}
	if got := strings.Join(e.AllNames(), "|"); got != "Show|ショー" {
		t.Errorf("AllNames = %q", got)
	}
}

func TestAiringShowsListsEachShowOnceAcrossTheWeek(t *testing.T) {
	restore := Seed(map[string][]ShowEntry{
		"2025-01-09": {{Name: "A", Route: "a"}},
		"2025-01-06": {{Name: "A", Route: "a"}, {Name: "B", Route: "b"}},
	})
	defer restore()
	var names []string
	for _, e := range AiringShows() {
		names = append(names, e.Name)
	}
	if strings.Join(names, ",") != "A,B" {
		t.Errorf("AiringShows = %v", names)
	}
}

func TestClockIsReplaceableAndReturnsUTC(t *testing.T) {
	zone := time.FixedZone("far", -10*3600)
	// 2026-11-03 23:30 in UTC-10 is 2026-11-04 09:30 UTC.
	restore := SetClock(func() time.Time { return time.Date(2026, 11, 3, 23, 30, 0, 0, zone) })
	defer restore()
	if got := Now(); got.Location() != time.UTC || got.Hour() != 9 {
		t.Errorf("Now = %v, want 09:30 UTC", got)
	}
	if got := Today(); !got.Equal(time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("Today = %v, want 2026-11-04 00:00 UTC", got)
	}
	restore()
	if got := Now(); time.Since(got) > time.Minute || time.Since(got) < -time.Minute {
		t.Errorf("restored clock = %v, want the system time", got)
	}
}

func TestGetDayShowsReadsOneUTCDateAndFiltersDonghua(t *testing.T) {
	defer Seed(map[string][]ShowEntry{
		"2026-11-03": {{Name: "Mon", Route: "mon"}, {Name: "Soul Land", Route: "sl", Donghua: true}},
		"2026-11-10": {{Name: "Next", Route: "next"}},
	})()
	// A local time on the other side of midnight still reads its UTC date.
	date := time.Date(2026, 11, 3, 23, 0, 0, 0, time.FixedZone("west", -5*3600)) // 2026-11-04 04:00 UTC
	if got := GetDayShows(date, true); len(got) != 0 {
		t.Errorf("2026-11-04 shows = %v, want none", got)
	}
	date = time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	if got := GetDayShows(date, true); len(got) != 2 {
		t.Errorf("with donghua = %v, want 2 shows", got)
	}
	if got := GetDayShows(date, false); len(got) != 1 || got[0].Name != "Mon" {
		t.Errorf("without donghua = %v, want only Mon", got)
	}
}

func TestDateInWeekIsTheCurrentISOWeeksDay(t *testing.T) {
	// 2026-11-04 is a Wednesday; its ISO week runs Monday 2026-11-02 to Sunday 2026-11-08.
	defer SetClock(func() time.Time { return time.Date(2026, 11, 4, 0, 30, 0, 0, time.UTC) })()
	tests := map[time.Weekday]string{time.Monday: "2026-11-02", time.Wednesday: "2026-11-04", time.Sunday: "2026-11-08"}
	for wd, want := range tests {
		if got := DateKey(DateInWeek(wd)); got != want {
			t.Errorf("DateInWeek(%v) = %s, want %s", wd, got, want)
		}
	}
}

type closeTracker struct {
	io.Reader
	closed *int32
}

func (c closeTracker) Close() error { atomic.AddInt32(c.closed, 1); return nil }

type rtFunc func(*http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestUpdateAnimeScheduleClosesBothBodiesOn429(t *testing.T) {
	var calls int32
	var bodies []*int32
	rt := rtFunc(func(r *http.Request) (*http.Response, error) {
		n := atomic.AddInt32(&calls, 1)
		closed := new(int32)
		bodies = append(bodies, closed)
		status := http.StatusTooManyRequests
		body := ""
		if n > 1 {
			status = http.StatusOK
			body = "[]"
		}
		return &http.Response{
			StatusCode: status,
			Header:     http.Header{},
			Body:       closeTracker{strings.NewReader(body), closed},
			Request:    r,
		}, nil
	})
	old := newClient
	newClient = func() *http.Client { return &http.Client{Transport: rt} }
	defer func() { newClient = old }()

	UpdateAnimeSchedule(context.Background(), "key")

	if calls != 2 {
		t.Fatalf("expected 2 requests, got %d", calls)
	}
	for i, c := range bodies {
		if atomic.LoadInt32(c) == 0 {
			t.Errorf("body %d was not closed", i)
		}
	}
}
