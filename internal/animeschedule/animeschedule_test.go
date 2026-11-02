package animeschedule

import (
	"context"
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
