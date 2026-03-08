package animeschedule

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"ReZeroTsu/internal/httpclient"
	"ReZeroTsu/internal/logger"
)

const (
	BaseURL      = "https://animeschedule.net"
	ImageBaseURL = "https://img.animeschedule.net/production/assets/public/img/"
	apiURL       = BaseURL + "/api/v3/timetables"
)

// TimetableAnime is one entry from the AnimeSchedule API timetables.
type TimetableAnime struct {
	Title             string    `json:"title"`
	Route             string    `json:"route"`
	EpisodeDate       time.Time `json:"episodeDate"`
	EpisodeNumber     int       `json:"episodeNumber"`
	Episodes          int       `json:"episodes"`
	DelayedText       string    `json:"delayedText"`
	DelayedFrom       time.Time `json:"delayedFrom"`
	DelayedUntil      time.Time `json:"delayedUntil"`
	Donghua           bool      `json:"donghua"`
	AirType           string    `json:"airType"`
	ImageVersionRoute string    `json:"imageVersionRoute"`
}

type ShowEntry struct {
	Name              string
	Route             string
	AirTimeUnix       int64
	Episode           string
	Delayed           string
	Donghua           bool
	AirType           string
	ImageVersionRoute string
}

// ImageURL returns the full CDN URL for the show image, or "" if none.
func (e ShowEntry) ImageURL() string {
	if e.ImageVersionRoute == "" {
		return ""
	}
	return ImageBaseURL + e.ImageVersionRoute
}

var (
	apiKeyMu       sync.RWMutex
	apiKey         string
	cacheMu        sync.RWMutex
	cache          = make(map[int][]ShowEntry) // weekday 0=Sunday .. 6=Saturday
	cachePopulated bool
	lastFetchErr   error
)

func SetAPIKey(key string) {
	apiKeyMu.Lock()
	defer apiKeyMu.Unlock()
	apiKey = key
}

// APIKeyConfigured reports whether an API key is set.
func APIKeyConfigured() bool {
	apiKeyMu.RLock()
	defer apiKeyMu.RUnlock()
	return apiKey != ""
}

// HasData reports whether the cache has been populated.
func HasData() bool {
	cacheMu.RLock()
	defer cacheMu.RUnlock()
	return cachePopulated
}

// GetDayShows returns shows for weekday (0=Sunday..6=Saturday), sorted by air time. Excludes donghua when false.
func GetDayShows(weekday int, donghua bool) []ShowEntry {
	cacheMu.RLock()
	entries := cache[weekday]
	cacheMu.RUnlock()
	if len(entries) == 0 {
		return nil
	}
	out := make([]ShowEntry, 0, len(entries))
	for _, e := range entries {
		if !donghua && e.Donghua {
			continue
		}
		out = append(out, e)
	}
	return out
}

// GetAiringShowNames returns the set of airing show names (lowercased).
func GetAiringShowNames() map[string]struct{} {
	cacheMu.RLock()
	defer cacheMu.RUnlock()
	out := make(map[string]struct{})
	for weekday := 0; weekday <= 6; weekday++ {
		for _, e := range cache[weekday] {
			out[strings.ToLower(e.Name)] = struct{}{}
		}
	}
	return out
}

// UpdateAnimeSchedule fetches timetables API, handles rate limits, updates cache.
func UpdateAnimeSchedule(ctx context.Context, key string) {
	if key == "" {
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		setFetchErr(err)
		logger.For("animeschedule").Error("NewRequest failed", "err", err)
		return
	}
	req.Header.Set("User-Agent", httpclient.GetUserAgent())
	req.Header.Set("Authorization", "Bearer "+key)

	client := httpclient.Default()
	resp, err := client.Do(req)
	if err != nil {
		setFetchErr(err)
		logger.For("animeschedule").Error("Do failed", "err", err)
		return
	}
	defer resp.Body.Close()

	// Rate limit: if 429 or remaining 0, wait until reset then retry once
	if resp.StatusCode == http.StatusTooManyRequests || resp.Header.Get("X-RateLimit-Remaining") == "0" {
		resetStr := resp.Header.Get("X-RateLimit-Reset")
		if resetStr != "" {
			resetUnix, parseErr := strconv.ParseInt(resetStr, 10, 64)
			if parseErr != nil {
				logger.For("animeschedule").Warn("X-RateLimit-Reset parse failed, skipping wait", "value", resetStr, "err", parseErr)
			} else {
				resetAt := time.Unix(resetUnix, 0)
				if d := time.Until(resetAt); d > 0 && d < 5*time.Minute {
					logger.For("animeschedule").Warn("rate limited; waiting until reset", "reset_at", resetAt.UTC().Format(time.RFC3339))
					resetWait := time.NewTimer(d)
					defer resetWait.Stop()
					select {
					case <-ctx.Done():
						return
					case <-resetWait.C:
					}
				}
			}
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			// Retry once after reset
			resp.Body.Close()
			req2, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
			if err != nil {
				setFetchErr(err)
				logger.For("animeschedule").Error("NewRequestWithContext retry failed", "err", err)
				return
			}
			req2.Header.Set("User-Agent", httpclient.GetUserAgent())
			req2.Header.Set("Authorization", "Bearer "+key)
			resp, err = client.Do(req2)
			if err != nil {
				setFetchErr(err)
				logger.For("animeschedule").Error("Do retry failed", "err", err)
				return
			}
		}
	}

	if resp.StatusCode != http.StatusOK {
		setFetchErr(fmt.Errorf("HTTP %d", resp.StatusCode))
		logger.For("animeschedule").Error("HTTP request failed", "status_code", resp.StatusCode)
		return
	}

	var list []TimetableAnime
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		setFetchErr(err)
		logger.For("animeschedule").Error("Decode failed", "err", err)
		return
	}

	newCache := processTimetable(list)
	cacheMu.Lock()
	cache = newCache
	cachePopulated = true
	lastFetchErr = nil
	cacheMu.Unlock()
}

func setFetchErr(err error) {
	cacheMu.Lock()
	lastFetchErr = err
	cacheMu.Unlock()
}

// processTimetable builds weekday -> []ShowEntry from API; sub preferred, raw if no sub. Sorts each day by air time.
func processTimetable(list []TimetableAnime) map[int][]ShowEntry {
	subRoutes := make(map[string]bool)
	for _, a := range list {
		if a.AirType == "sub" {
			subRoutes[a.Route] = true
		}
	}

	byWeekday := make(map[int][]ShowEntry)
	for _, a := range list {
		if a.AirType == "dub" {
			continue
		}
		if a.AirType == "raw" && subRoutes[a.Route] {
			continue
		}

		weekday := int(a.EpisodeDate.Weekday())
		episodeStr := fmt.Sprintf("Ep %d", a.EpisodeNumber)
		if a.Episodes > 0 && a.EpisodeNumber >= a.Episodes {
			episodeStr = fmt.Sprintf("Ep %dF", a.EpisodeNumber)
		}
		delayed := ""
		if !a.DelayedFrom.IsZero() || !a.DelayedUntil.IsZero() {
			if a.DelayedUntil.IsZero() {
				if !a.EpisodeDate.Before(a.DelayedFrom) {
					delayed = "Delayed"
				}
			} else if a.DelayedFrom.IsZero() {
				if !a.EpisodeDate.After(a.DelayedUntil) {
					delayed = "Delayed"
				}
			} else if a.EpisodeDate.After(a.DelayedFrom) && a.EpisodeDate.Before(a.DelayedUntil) {
				delayed = "Delayed"
			}
		}
		if a.DelayedText != "" {
			delayed = a.DelayedText
		}

		airType := a.AirType
		if airType != "sub" && airType != "raw" {
			airType = "raw"
		}
		byWeekday[weekday] = append(byWeekday[weekday], ShowEntry{
			Name:              strings.TrimSpace(a.Title),
			Route:             a.Route,
			AirTimeUnix:       a.EpisodeDate.UTC().Unix(),
			Episode:           episodeStr,
			Delayed:           delayed,
			Donghua:           a.Donghua,
			AirType:           airType,
			ImageVersionRoute: a.ImageVersionRoute,
		})
	}

	for w := range byWeekday {
		sort.Slice(byWeekday[w], func(i, j int) bool {
			return byWeekday[w][i].AirTimeUnix < byWeekday[w][j].AirTimeUnix
		})
	}
	return byWeekday
}
