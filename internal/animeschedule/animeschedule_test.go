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

func TestProcessTimetableBucketsByUTCWeekday(t *testing.T) {
	tests := []struct {
		name        string
		episodeDate string
		wantDay     time.Weekday
	}{
		// 2025-01-06 is a Monday in UTC; 2025-01-06T05:00+09:00 = 2025-01-05T20:00Z (Sunday).
		{"positive offset previous UTC day", "2025-01-06T05:00:00+09:00", time.Sunday},
		// 2025-01-06T22:00-05:00 = 2025-01-07T03:00Z (Tuesday).
		{"negative offset next UTC day", "2025-01-06T22:00:00-05:00", time.Tuesday},
		{"UTC unchanged", "2025-01-06T12:00:00Z", time.Monday},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ts, err := time.Parse(time.RFC3339, tc.episodeDate)
			if err != nil {
				t.Fatal(err)
			}
			got := processTimetable([]TimetableAnime{{Title: "Show", Route: "show", AirType: "sub", EpisodeDate: ts}})
			if len(got[int(tc.wantDay)]) != 1 {
				t.Fatalf("expected entry under weekday %d, got %v", tc.wantDay, got)
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
	got := processTimetable(list)[int(time.Monday)]
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
	restore := Seed(map[int][]ShowEntry{
		1: {{Name: "A", Route: "a"}, {Name: "B", Route: "b"}},
		4: {{Name: "A", Route: "a"}},
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
