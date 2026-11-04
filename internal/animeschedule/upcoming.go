package animeschedule

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"ReZeroTsu/internal/httpclient"
	"ReZeroTsu/internal/logger"
)

// UpcomingShow is a show on AnimeSchedule whose first episode has not aired yet.
type UpcomingShow struct {
	Name    string
	Route   string
	Romaji  string
	English string
	Native  string
	// Premiere is the zero time when AnimeSchedule has no premiere date.
	Premiere time.Time
	// SubPremiere and DubPremiere are the zero time when AnimeSchedule has none. JPNTime, SubTime and DubTime are
	// the tracks' air clocks, the zero time when unset; only their UTC hour and minute mean anything.
	SubPremiere, DubPremiere  time.Time
	JPNTime, SubTime, DubTime time.Time
	MediaType                 MediaType
}

type upcomingItem struct {
	Title string `json:"title"`
	Route string `json:"route"`
	Names struct {
		Romaji  string `json:"romaji"`
		English string `json:"english"`
		Native  string `json:"native"`
	} `json:"names"`
	Premier    time.Time      `json:"premier"`
	SubPremier time.Time      `json:"subPremier"`
	DubPremier time.Time      `json:"dubPremier"`
	JPNTime    time.Time      `json:"jpnTime"`
	SubTime    time.Time      `json:"subTime"`
	DubTime    time.Time      `json:"dubTime"`
	MediaTypes []MediaTypeRef `json:"mediaTypes"`
}

type upcomingPage struct {
	Anime       []upcomingItem `json:"anime"`
	TotalAmount int            `json:"totalAmount"`
}

func (i upcomingItem) show() UpcomingShow {
	return UpcomingShow{
		Name:        strings.TrimSpace(i.Title),
		Route:       i.Route,
		Romaji:      strings.TrimSpace(i.Names.Romaji),
		English:     strings.TrimSpace(i.Names.English),
		Native:      strings.TrimSpace(i.Names.Native),
		Premiere:    i.Premier,
		SubPremiere: i.SubPremier,
		DubPremiere: i.DubPremier,
		JPNTime:     i.JPNTime,
		SubTime:     i.SubTime,
		DubTime:     i.DubTime,
		MediaType:   decodeMediaType(i.MediaTypes),
	}
}

var (
	upcomingURL = BaseURL + "/api/v3/anime"
	// upcomingPageGap spaces the page requests so the timetable refresh always has rate-limit room.
	upcomingPageGap = 3 * time.Second
	// upcomingStartDelay is the wait before the first load after startup; upcomingRefreshEvery the period after.
	upcomingStartDelay   = 30 * time.Second
	upcomingRefreshEvery = 24 * time.Hour
	upcomingSleep        = sleepCtx
)

const (
	upcomingMaxPages = 200
	// maxResetWait caps the wait for a rate-limit reset; fallbackResetWait replaces an unusable reset.
	maxResetWait      = 5 * time.Minute
	fallbackResetWait = time.Minute
	// upcomingRetryEvery is the wait before the next load attempt while the list has never loaded.
	upcomingRetryEvery = 15 * time.Minute
)

var (
	upcomingMu     sync.RWMutex
	upcomingList   []UpcomingShow
	upcomingLoaded bool
)

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// UpcomingShows returns the upcoming shows in AnimeSchedule's order. It is empty until the list has loaded.
func UpcomingShows() []UpcomingShow {
	upcomingMu.RLock()
	defer upcomingMu.RUnlock()
	return append([]UpcomingShow(nil), upcomingList...)
}

// UpcomingLoaded reports whether the upcoming list has loaded at least once.
func UpcomingLoaded() bool {
	upcomingMu.RLock()
	defer upcomingMu.RUnlock()
	return upcomingLoaded
}

// SeedUpcoming replaces the upcoming list and its loaded flag, and returns a function that restores the previous
// state. It exists so tests need no HTTP server.
func SeedUpcoming(shows []UpcomingShow, loaded bool) (restore func()) {
	upcomingMu.Lock()
	prev, prevLoaded := upcomingList, upcomingLoaded
	upcomingList, upcomingLoaded = shows, loaded
	upcomingMu.Unlock()
	return func() {
		upcomingMu.Lock()
		upcomingList, upcomingLoaded = prev, prevLoaded
		upcomingMu.Unlock()
	}
}

// rateLimitWait is how long to wait after a spent rate limit. A reset (Unix seconds in X-RateLimit-Reset) within
// maxResetWait is waited for. A reset already past waits one page gap. A missing, unparsable or distant reset
// waits fallbackResetWait.
func rateLimitWait(h http.Header) time.Duration {
	resetStr := h.Get("X-RateLimit-Reset")
	if resetStr == "" {
		return fallbackResetWait
	}
	resetUnix, err := strconv.ParseInt(resetStr, 10, 64)
	if err != nil {
		logger.For("animeschedule").Warn("X-RateLimit-Reset parse failed, using the fallback wait", "value", resetStr, "err", err)
		return fallbackResetWait
	}
	switch d := time.Until(time.Unix(resetUnix, 0)); {
	case d <= 0:
		return upcomingPageGap
	case d > maxResetWait:
		return fallbackResetWait
	default:
		return d
	}
}

// waitForReset sleeps for rateLimitWait of h.
func waitForReset(ctx context.Context, h http.Header) error {
	d := rateLimitWait(h)
	logger.For("animeschedule").Warn("rate limited; waiting", "wait", d.String())
	return upcomingSleep(ctx, d)
}

// fetchUpcomingPage reads one page. A 429 waits until the reset and asks once more. It returns the page and the
// response headers, so the caller can see a spent rate limit.
func fetchUpcomingPage(ctx context.Context, key string, page int) (upcomingPage, http.Header, error) {
	var zero upcomingPage
	u := upcomingURL + "?" + url.Values{"airing-statuses": {"upcoming"}, "page": {strconv.Itoa(page)}}.Encode()
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return zero, nil, err
		}
		req.Header.Set("User-Agent", httpclient.GetUserAgent())
		req.Header.Set("Authorization", "Bearer "+key)
		resp, err := newClient().Do(req)
		if err != nil {
			return zero, nil, err
		}
		if resp.StatusCode == http.StatusTooManyRequests && attempt == 0 {
			h := resp.Header
			resp.Body.Close()
			if err := waitForReset(ctx, h); err != nil {
				return zero, nil, err
			}
			continue
		}
		var p upcomingPage
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return zero, nil, fmt.Errorf("HTTP %d", resp.StatusCode)
		}
		err = json.NewDecoder(resp.Body).Decode(&p)
		resp.Body.Close()
		if err != nil {
			return zero, nil, err
		}
		return p, resp.Header, nil
	}
}

// RefreshUpcoming reads every page of AnimeSchedule's upcoming anime and replaces the list. Requests are spaced by
// upcomingPageGap, and a spent rate limit waits for its reset. Without a totalAmount it reads until an empty page.
// On any failure, including a read that hits the page cap or ends short of totalAmount, the last good list stays.
func RefreshUpcoming(ctx context.Context, key string) error {
	if key == "" {
		return nil
	}
	var all []UpcomingShow
	total, complete := 0, false
	for page := 1; page <= upcomingMaxPages; page++ {
		p, h, err := fetchUpcomingPage(ctx, key, page)
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				logger.For("animeschedule").Error("upcoming page failed", "page", page, "err", err)
			}
			return err
		}
		for _, it := range p.Anime {
			all = append(all, it.show())
		}
		total = p.TotalAmount
		if len(p.Anime) == 0 || (total > 0 && len(all) >= total) {
			complete = true
			break
		}
		if h.Get("X-RateLimit-Remaining") == "0" {
			err = waitForReset(ctx, h)
		} else {
			err = upcomingSleep(ctx, upcomingPageGap)
		}
		if err != nil {
			return err
		}
	}
	if !complete {
		logger.For("animeschedule").Warn("upcoming list hit the page cap; keeping the last list", "pages", upcomingMaxPages, "read", len(all))
		return fmt.Errorf("upcoming list not read within %d pages", upcomingMaxPages)
	}
	if total > 0 && len(all) < total {
		logger.For("animeschedule").Warn("upcoming list ended short of its total; keeping the last list", "read", len(all), "total", total)
		return fmt.Errorf("upcoming list has %d of %d shows", len(all), total)
	}
	upcomingMu.Lock()
	upcomingList, upcomingLoaded = all, true
	upcomingMu.Unlock()
	return nil
}

// RunUpcomingLoop loads the upcoming list shortly after startup, then once a day, until ctx is cancelled. While the
// list has never loaded, a failed load is retried every upcomingRetryEvery.
func RunUpcomingLoop(ctx context.Context, key string) {
	if key == "" {
		return
	}
	wait := upcomingStartDelay
	for {
		if err := upcomingSleep(ctx, wait); err != nil {
			return
		}
		err := RefreshUpcoming(ctx, key)
		if err != nil && !UpcomingLoaded() {
			wait = upcomingRetryEvery
		} else {
			wait = upcomingRefreshEvery
		}
	}
}
