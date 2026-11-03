package animeschedule

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
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
	Title             string         `json:"title"`
	Romaji            string         `json:"romaji"`
	English           string         `json:"english"`
	Native            string         `json:"native"`
	Route             string         `json:"route"`
	EpisodeDate       time.Time      `json:"episodeDate"`
	EpisodeNumber     int            `json:"episodeNumber"`
	Episodes          int            `json:"episodes"`
	DelayedText       string         `json:"delayedText"`
	DelayedFrom       time.Time      `json:"delayedFrom"`
	DelayedUntil      time.Time      `json:"delayedUntil"`
	Donghua           bool           `json:"donghua"`
	AirType           string         `json:"airType"`
	ImageVersionRoute string         `json:"imageVersionRoute"`
	MediaTypes        []MediaTypeRef `json:"mediaTypes"`
}

type ShowEntry struct {
	// Name is the display title; subscriptions are keyed by it.
	Name string
	// Romaji, English and Native are the show's other names; any may be empty.
	Romaji            string
	English           string
	Native            string
	Route             string
	AirTimeUnix       int64
	Episode           string
	Delayed           string
	Donghua           bool
	MediaType         MediaType
	AirType           string
	ImageVersionRoute string
	// Final marks the show's last episode.
	Final bool
}

// AllNames returns the display title, romaji, English and native names in that order, leaving out empty names and
// repeats (compared ignoring case and surrounding space).
func (e ShowEntry) AllNames() []string {
	var out []string
	seen := make(map[string]bool, 4)
	for _, n := range []string{e.Name, e.Romaji, e.English, e.Native} {
		n = strings.TrimSpace(n)
		k := strings.ToLower(n)
		if n == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, n)
	}
	return out
}

// ImageURL returns the full CDN URL for the show image, or "" if none.
func (e ShowEntry) ImageURL() string {
	if e.ImageVersionRoute == "" {
		return ""
	}
	return ImageBaseURL + e.ImageVersionRoute
}

// newClient builds the HTTP client for timetable fetches (overridable in tests).
var newClient = httpclient.Default

var (
	apiKeyMu       sync.RWMutex
	apiKey         string
	cacheMu        sync.RWMutex
	cache          = make(map[string][]ShowEntry) // UTC calendar date (DateKey) -> entries sorted by air time
	cachePopulated bool
	lastFetchErr   error
)

// clock returns the current time; replaceable through SetClock.
var (
	clockMu sync.RWMutex
	clock   = time.Now
)

// SetClock replaces the shared clock and returns a function that restores the previous one. Commands and
// delivery runs read "now" and "today" from Now and Today, so tests set the clock here.
func SetClock(now func() time.Time) (restore func()) {
	clockMu.Lock()
	prev := clock
	clock = now
	clockMu.Unlock()
	return func() {
		clockMu.Lock()
		clock = prev
		clockMu.Unlock()
	}
}

// Now returns the current time in UTC from the shared clock.
func Now() time.Time {
	clockMu.RLock()
	now := clock
	clockMu.RUnlock()
	return now().UTC()
}

// Today returns the current UTC date from the shared clock, as midnight UTC.
func Today() time.Time {
	n := Now()
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.UTC)
}

// DateKey is the timetable key of t's UTC calendar date ("2006-01-02").
func DateKey(t time.Time) string {
	return t.UTC().Format("2006-01-02")
}

// DateInWeek returns the date, as midnight UTC, of the given weekday in the current UTC ISO week (Monday to
// Sunday) of the shared clock.
func DateInWeek(weekday time.Weekday) time.Time {
	today := Today()
	monday := today.AddDate(0, 0, -((int(today.Weekday()) + 6) % 7))
	return monday.AddDate(0, 0, (int(weekday)+6)%7)
}

func SetAPIKey(key string) {
	apiKeyMu.Lock()
	defer apiKeyMu.Unlock()
	apiKey = key
}

// Seed replaces the cached timetable and marks it populated, keyed by UTC date (see DateKey), and returns a
// function that restores the previous state. It exists so tests need no HTTP server.
func Seed(days map[string][]ShowEntry) (restore func()) {
	cacheMu.Lock()
	prev, prevPopulated := cache, cachePopulated
	cache, cachePopulated = days, true
	cacheMu.Unlock()
	return func() {
		cacheMu.Lock()
		cache, cachePopulated = prev, prevPopulated
		cacheMu.Unlock()
	}
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

// loadedDates returns the cached dates in ascending order. The caller holds cacheMu.
func loadedDates() []string {
	dates := make([]string, 0, len(cache))
	for d := range cache {
		dates = append(dates, d)
	}
	sort.Strings(dates)
	return dates
}

// GetDayShows returns the shows of the UTC date of day, sorted by air time. Excludes donghua when false.
func GetDayShows(day time.Time, donghua bool) []ShowEntry {
	cacheMu.RLock()
	entries := cache[DateKey(day)]
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

// AiringShows returns the airing shows of the loaded dates, one entry per route (or per title when a route is
// missing), in date then air-time order. Donghua is included.
func AiringShows() []ShowEntry {
	cacheMu.RLock()
	defer cacheMu.RUnlock()
	var out []ShowEntry
	seen := make(map[string]bool)
	for _, date := range loadedDates() {
		for _, e := range cache[date] {
			key := e.Route
			if key == "" {
				key = strings.ToLower(e.Name)
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, e)
		}
	}
	return out
}

// GetAiringShowNames returns the set of airing show names (lowercased).
func GetAiringShowNames() map[string]struct{} {
	cacheMu.RLock()
	defer cacheMu.RUnlock()
	out := make(map[string]struct{})
	for _, entries := range cache {
		for _, e := range entries {
			out[strings.ToLower(e.Name)] = struct{}{}
		}
	}
	return out
}

// AllEntries returns every entry of the loaded dates in date then air-time order, donghua included.
func AllEntries() []ShowEntry {
	cacheMu.RLock()
	defer cacheMu.RUnlock()
	var out []ShowEntry
	for _, date := range loadedDates() {
		out = append(out, cache[date]...)
	}
	return out
}

// timetableWeek is one ISO week of the loaded range: its Monday (midnight UTC) and ISO year and number.
type timetableWeek struct {
	monday time.Time
	year   int
	week   int
}

// loadedWeeks returns last week, this week and next week by the shared UTC clock.
func loadedWeeks() []timetableWeek {
	thisMonday := DateInWeek(time.Monday)
	weeks := make([]timetableWeek, 0, 3)
	for _, offset := range []int{-7, 0, 7} {
		monday := thisMonday.AddDate(0, 0, offset)
		year, week := monday.ISOWeek()
		weeks = append(weeks, timetableWeek{monday: monday, year: year, week: week})
	}
	return weeks
}

// LoadedRange returns the first and last loaded UTC dates, as midnight UTC: last week's Monday and next week's
// Sunday by the shared clock.
func LoadedRange() (first, last time.Time) {
	weeks := loadedWeeks()
	return weeks[0].monday, weeks[len(weeks)-1].monday.AddDate(0, 0, 6)
}

// UpdateAnimeSchedule fetches last week, this week and next week from the timetables API (each by ISO year and
// week, as a UTC week) and updates the cache. A week that fails to fetch keeps its previous entries.
func UpdateAnimeSchedule(ctx context.Context, key string) {
	if key == "" {
		return
	}
	weeks := loadedWeeks()
	fetched := make(map[string][]ShowEntry, 21)
	ok := make(map[time.Time]bool, len(weeks))
	for _, w := range weeks {
		if ctx.Err() != nil {
			break
		}
		list, err := fetchWeek(ctx, key, w)
		if err != nil {
			setFetchErr(err)
			continue
		}
		ok[w.monday] = true
		for date, entries := range processTimetable(list) {
			// An entry outside the week it was requested for never lands on a loaded date.
			if t, perr := time.Parse("2006-01-02", date); perr == nil && !t.Before(w.monday) && t.Before(w.monday.AddDate(0, 0, 7)) {
				fetched[date] = entries
			}
		}
	}
	if len(ok) == 0 {
		return
	}

	first, last := LoadedRange()
	cacheMu.Lock()
	defer cacheMu.Unlock()
	newCache := make(map[string][]ShowEntry, len(cache))
	for date, entries := range cache {
		t, perr := time.Parse("2006-01-02", date)
		if perr != nil || t.Before(first) || t.After(last) {
			continue
		}
		monday := t.AddDate(0, 0, -((int(t.Weekday()) + 6) % 7))
		if ok[monday] {
			continue
		}
		newCache[date] = entries
	}
	for date, entries := range fetched {
		newCache[date] = entries
	}
	cache = newCache
	cachePopulated = true
	if len(ok) == len(weeks) {
		lastFetchErr = nil
	}
}

// fetchWeek requests one timetable week, waiting for the rate-limit reset and retrying once on a 429.
func fetchWeek(ctx context.Context, key string, w timetableWeek) ([]TimetableAnime, error) {
	q := url.Values{}
	q.Set("year", strconv.Itoa(w.year))
	q.Set("week", strconv.Itoa(w.week))
	q.Set("tz", "Etc/UTC")
	reqURL := apiURL + "?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		logger.For("animeschedule").Error("NewRequest failed", "err", err)
		return nil, err
	}
	req.Header.Set("User-Agent", httpclient.GetUserAgent())
	req.Header.Set("Authorization", "Bearer "+key)

	client := newClient()
	resp, err := client.Do(req)
	if err != nil {
		logger.For("animeschedule").Error("Do failed", "err", err)
		return nil, err
	}
	// Close whichever response is current when we return (the retry replaces resp).
	defer func() { resp.Body.Close() }()

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
						return nil, ctx.Err()
					case <-resetWait.C:
					}
				}
			}
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			// Retry once after reset
			resp.Body.Close()
			req2, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
			if err != nil {
				logger.For("animeschedule").Error("NewRequestWithContext retry failed", "err", err)
				return nil, err
			}
			req2.Header.Set("User-Agent", httpclient.GetUserAgent())
			req2.Header.Set("Authorization", "Bearer "+key)
			retryResp, err := client.Do(req2)
			if err != nil {
				logger.For("animeschedule").Error("Do retry failed", "err", err)
				return nil, err
			}
			resp = retryResp
		}
	}

	if resp.StatusCode != http.StatusOK {
		logger.For("animeschedule").Error("HTTP request failed", "status_code", resp.StatusCode, "year", w.year, "week", w.week)
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	var list []TimetableAnime
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		logger.For("animeschedule").Error("Decode failed", "err", err)
		return nil, err
	}
	return list, nil
}

func setFetchErr(err error) {
	cacheMu.Lock()
	lastFetchErr = err
	cacheMu.Unlock()
}

// processTimetable builds UTC date -> []ShowEntry from API; sub preferred, raw if no sub. Sorts each day by air time.
func processTimetable(list []TimetableAnime) map[string][]ShowEntry {
	subRoutes := make(map[string]bool)
	for _, a := range list {
		if a.AirType == "sub" {
			subRoutes[a.Route] = true
		}
	}

	byDate := make(map[string][]ShowEntry)
	for _, a := range list {
		if a.AirType == "dub" {
			continue
		}
		if a.AirType == "raw" && subRoutes[a.Route] {
			continue
		}

		date := DateKey(a.EpisodeDate)
		episodeStr := fmt.Sprintf("Ep %d", a.EpisodeNumber)
		final := false
		if a.Episodes > 0 && a.EpisodeNumber >= a.Episodes {
			episodeStr = fmt.Sprintf("Ep %dF", a.EpisodeNumber)
			final = true
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
		byDate[date] = append(byDate[date], ShowEntry{
			Name:              strings.TrimSpace(a.Title),
			Romaji:            strings.TrimSpace(a.Romaji),
			English:           strings.TrimSpace(a.English),
			Native:            strings.TrimSpace(a.Native),
			Route:             a.Route,
			AirTimeUnix:       a.EpisodeDate.UTC().Unix(),
			Episode:           episodeStr,
			Delayed:           delayed,
			Donghua:           a.Donghua,
			MediaType:         decodeMediaType(a.MediaTypes),
			AirType:           airType,
			ImageVersionRoute: a.ImageVersionRoute,
			Final:             final,
		})
	}

	for d := range byDate {
		sort.Slice(byDate[d], func(i, j int) bool {
			return byDate[d][i].AirTimeUnix < byDate[d][j].AirTimeUnix
		})
	}
	return byDate
}
