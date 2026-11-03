package animeschedule

import (
	"context"
	"encoding/json"
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

// MediaType is the kind of release a show is. The set is closed; an unknown AnimeSchedule type is TV.
type MediaType string

const (
	MediaTypeTV      MediaType = "TV"
	MediaTypeTVShort MediaType = "TV Short"
	MediaTypeONA     MediaType = "ONA"
	MediaTypeDonghua MediaType = "Donghua" // AnimeSchedule's "ONA (Chinese)"
	MediaTypeOVA     MediaType = "OVA"
	MediaTypeSpecial MediaType = "Special"
	MediaTypeMovie   MediaType = "Movie"
)

// mediaTypeFromRoute maps an AnimeSchedule media-type route to a MediaType.
func mediaTypeFromRoute(route string) MediaType {
	switch strings.ToLower(strings.TrimSpace(route)) {
	case "tv-short":
		return MediaTypeTVShort
	case "ona":
		return MediaTypeONA
	case "ona-chinese":
		return MediaTypeDonghua
	case "ova":
		return MediaTypeOVA
	case "special":
		return MediaTypeSpecial
	case "movie":
		return MediaTypeMovie
	default:
		return MediaTypeTV
	}
}

// UpcomingShow is a show on AnimeSchedule whose first episode has not aired yet.
type UpcomingShow struct {
	Name    string
	Route   string
	Romaji  string
	English string
	Native  string
	// Premiere is the zero time when AnimeSchedule has no premiere date.
	Premiere  time.Time
	MediaType MediaType
}

type upcomingItem struct {
	Title string `json:"title"`
	Route string `json:"route"`
	Names struct {
		Romaji  string `json:"romaji"`
		English string `json:"english"`
		Native  string `json:"native"`
	} `json:"names"`
	Premier    time.Time `json:"premier"`
	MediaTypes []struct {
		Route string `json:"route"`
	} `json:"mediaTypes"`
}

type upcomingPage struct {
	Anime       []upcomingItem `json:"anime"`
	TotalAmount int            `json:"totalAmount"`
}

func (i upcomingItem) show() UpcomingShow {
	mt := MediaTypeTV
	if len(i.MediaTypes) > 0 {
		mt = mediaTypeFromRoute(i.MediaTypes[0].Route)
	}
	return UpcomingShow{
		Name:      strings.TrimSpace(i.Title),
		Route:     i.Route,
		Romaji:    strings.TrimSpace(i.Names.Romaji),
		English:   strings.TrimSpace(i.Names.English),
		Native:    strings.TrimSpace(i.Names.Native),
		Premiere:  i.Premier,
		MediaType: mt,
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
	maxResetWait     = 5 * time.Minute
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

// waitForReset sleeps until the X-RateLimit-Reset time (Unix seconds) of h, when it parses and lies within
// maxResetWait.
func waitForReset(ctx context.Context, h http.Header) error {
	resetStr := h.Get("X-RateLimit-Reset")
	if resetStr == "" {
		return nil
	}
	resetUnix, err := strconv.ParseInt(resetStr, 10, 64)
	if err != nil {
		logger.For("animeschedule").Warn("X-RateLimit-Reset parse failed, skipping wait", "value", resetStr, "err", err)
		return nil
	}
	resetAt := time.Unix(resetUnix, 0)
	if d := time.Until(resetAt); d > 0 && d < maxResetWait {
		logger.For("animeschedule").Warn("rate limited; waiting until reset", "reset_at", resetAt.UTC().Format(time.RFC3339))
		return upcomingSleep(ctx, d)
	}
	return nil
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
// upcomingPageGap, and a spent rate limit waits until X-RateLimit-Reset. On any failure the last good list stays.
func RefreshUpcoming(ctx context.Context, key string) error {
	if key == "" {
		return nil
	}
	var all []UpcomingShow
	for page := 1; page <= upcomingMaxPages; page++ {
		p, h, err := fetchUpcomingPage(ctx, key, page)
		if err != nil {
			logger.For("animeschedule").Error("upcoming page failed", "page", page, "err", err)
			return err
		}
		for _, it := range p.Anime {
			all = append(all, it.show())
		}
		if len(p.Anime) == 0 || len(all) >= p.TotalAmount {
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
	upcomingMu.Lock()
	upcomingList, upcomingLoaded = all, true
	upcomingMu.Unlock()
	return nil
}

// RunUpcomingLoop loads the upcoming list shortly after startup, then once a day, until ctx is cancelled.
func RunUpcomingLoop(ctx context.Context, key string) {
	if key == "" {
		return
	}
	if err := sleepCtx(ctx, upcomingStartDelay); err != nil {
		return
	}
	ticker := time.NewTicker(upcomingRefreshEvery)
	defer ticker.Stop()
	for {
		_ = RefreshUpcoming(ctx, key)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
