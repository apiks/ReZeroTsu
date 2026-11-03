// Package animeschedule holds the timetable (three UTC weeks keyed by date) and the upcoming-show list, both read
// from AnimeSchedule.net. Read docs/animeschedule-api.md before changing a request or a response shape: week
// boundaries follow the tz parameter, the rate limit is 120 requests per minute, and the docs are split across pages.
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

	"ReZeroTsu/internal/database/anime_subs"
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
	Title         string    `json:"title"`
	Romaji        string    `json:"romaji"`
	English       string    `json:"english"`
	Native        string    `json:"native"`
	Route         string    `json:"route"`
	EpisodeDate   time.Time `json:"episodeDate"`
	EpisodeNumber int       `json:"episodeNumber"`
	// SubtractedEpisodeNumber is the lowest episode of several that air together; 0 when unset.
	SubtractedEpisodeNumber int            `json:"subtractedEpisodeNumber"`
	Episodes                int            `json:"episodes"`
	DelayedText             string         `json:"delayedText"`
	DelayedFrom             time.Time      `json:"delayedFrom"`
	DelayedUntil            time.Time      `json:"delayedUntil"`
	Donghua                 bool           `json:"donghua"`
	AirType                 string         `json:"airType"`
	ImageVersionRoute       string         `json:"imageVersionRoute"`
	MediaTypes              []MediaTypeRef `json:"mediaTypes"`
}

type ShowEntry struct {
	// Name is the display title; subscriptions are keyed by it.
	Name string
	// Romaji, English and Native are the show's other names; any may be empty.
	Romaji      string
	English     string
	Native      string
	Route       string
	AirTimeUnix int64
	Episode     string
	Delayed     string
	Donghua     bool
	MediaType   MediaType
	// Track is the entry's track.
	Track             Track
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
	apiKeyMu           sync.RWMutex
	apiKey             string
	timetableMu        sync.RWMutex
	timetable          = make(map[string][]ShowEntry) // UTC calendar date (DateKey) -> entries sorted by air time
	timetablePopulated bool
	// fetchedWeeks holds the Monday (DateKey) of each week fetched successfully since startup.
	fetchedWeeks = make(map[string]bool)
	lastFetchErr error
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

// DayOf returns the UTC date of t, as midnight UTC.
func DayOf(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// Today returns the current UTC date from the shared clock, as midnight UTC.
func Today() time.Time {
	return DayOf(Now())
}

const dateKeyLayout = "2006-01-02"

// DateKey is the timetable key of t's UTC calendar date ("2006-01-02").
func DateKey(t time.Time) string {
	return t.UTC().Format(dateKeyLayout)
}

// ParseDateKey reads a timetable key ("2006-01-02") as that UTC date, at midnight UTC.
func ParseDateKey(key string) (time.Time, error) {
	return time.Parse(dateKeyLayout, key)
}

// mondayOf returns the Monday, as midnight UTC, of the UTC ISO week that holds t.
func mondayOf(t time.Time) time.Time {
	d := DayOf(t)
	return d.AddDate(0, 0, -((int(d.Weekday()) + 6) % 7))
}

// DateInWeek returns the date, as midnight UTC, of the given weekday in the current UTC ISO week (Monday to
// Sunday) of the shared clock.
func DateInWeek(weekday time.Weekday) time.Time {
	return mondayOf(Today()).AddDate(0, 0, (int(weekday)+6)%7)
}

func SetAPIKey(key string) {
	apiKeyMu.Lock()
	defer apiKeyMu.Unlock()
	apiKey = key
}

// Seed replaces the loaded timetable and marks it populated, keyed by UTC date (see DateKey), and returns a
// function that restores the previous state. Every week that holds a date counts as fetched. It exists so tests
// need no HTTP server.
func Seed(days map[string][]ShowEntry) (restore func()) {
	weeks := make(map[string]bool)
	for date := range days {
		if t, err := time.Parse(dateKeyLayout, date); err == nil {
			weeks[DateKey(mondayOf(t))] = true
		}
	}
	normalized := make(map[string][]ShowEntry, len(days))
	for date, rows := range days {
		normalized[date] = normalizeRows(rows)
	}
	timetableMu.Lock()
	prev, prevPopulated, prevWeeks := timetable, timetablePopulated, fetchedWeeks
	timetable, timetablePopulated, fetchedWeeks = normalized, true, weeks
	timetableMu.Unlock()
	return func() {
		timetableMu.Lock()
		timetable, timetablePopulated, fetchedWeeks = prev, prevPopulated, prevWeeks
		timetableMu.Unlock()
	}
}

// APIKeyConfigured reports whether an API key is set.
func APIKeyConfigured() bool {
	apiKeyMu.RLock()
	defer apiKeyMu.RUnlock()
	return apiKey != ""
}

// HasData reports whether the timetable has been populated.
func HasData() bool {
	timetableMu.RLock()
	defer timetableMu.RUnlock()
	return timetablePopulated
}

// loadedDates returns the loaded dates in ascending order. The caller holds timetableMu.
func loadedDates() []string {
	dates := make([]string, 0, len(timetable))
	for d := range timetable {
		dates = append(dates, d)
	}
	sort.Strings(dates)
	return dates
}

// subView is the loaded timetable read under the Sub chain, which the readers other than GetDayShows return.
// The caller holds timetableMu.
func subView() map[string][]ShowEntry {
	return chainDays(timetable, PreferSub)
}

// GetDayShows returns the shows of the UTC date of day under the preferred track, sorted by air time. For each
// show and UTC ISO week the preference shows one track, and only that track's rows show: Sub shows sub, else raw;
// Dub shows dub, else sub, else raw; Raw shows raw. A delayed row counts. Excludes donghua when false.
func GetDayShows(day time.Time, donghua bool, pref PreferredTrack) []ShowEntry {
	key := DateKey(day)
	timetableMu.RLock()
	week := make(map[string][]ShowEntry, 7)
	monday := mondayOf(day)
	for d := range 7 {
		k := DateKey(monday.AddDate(0, 0, d))
		if rows, ok := timetable[k]; ok {
			week[k] = rows
		}
	}
	entries := chainDay(key, week[key], tracksByWeek(week), pref)
	timetableMu.RUnlock()
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

// AiringShows returns the shows on this week's and next week's timetable, one entry per route (or per title when
// a route is missing), in date then air-time order, so a route on both weeks keeps its earliest entry from this
// week or later. Donghua is included. Last week is left out: a show whose final episode aired there is not
// airing. AllEntries covers every loaded week.
func AiringShows() []ShowEntry {
	weeks := weekLayout(Now())
	from, to := DateKey(weeks[1].monday), DateKey(weeks[2].monday.AddDate(0, 0, 6))
	timetableMu.RLock()
	defer timetableMu.RUnlock()
	view := subView()
	var out []ShowEntry
	seen := make(map[string]bool)
	for _, date := range loadedDates() {
		if date < from || date > to {
			continue
		}
		for _, e := range view[date] {
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

// showID identifies a show among entries: its route, or its title's match key when it has none.
func showID(e ShowEntry) string {
	if e.Route != "" {
		return e.Route
	}
	return anime_subs.MatchKey(e.Name)
}

// OnBreak returns, from last week's entries, the latest entry of each show whose latest entry there was not its
// final episode: such a show may be on a break and is kept by the daily cleanup.
func OnBreak(last []ShowEntry) []ShowEntry {
	latest := map[string]ShowEntry{}
	var order []string
	for _, e := range last {
		id := showID(e)
		cur, ok := latest[id]
		if !ok {
			order = append(order, id)
		}
		if !ok || e.AirTimeUnix >= cur.AirTimeUnix {
			latest[id] = e
		}
	}
	var out []ShowEntry
	for _, id := range order {
		if e := latest[id]; !e.Final {
			out = append(out, e)
		}
	}
	return out
}

// LoadedShows returns the shows of the three loaded weeks that the daily cleanup would keep: AiringShows, then the
// shows OnBreak from last week that are not already among them.
func LoadedShows() []ShowEntry {
	out := AiringShows()
	seen := make(map[string]bool, len(out))
	for _, e := range out {
		seen[showID(e)] = true
	}
	for _, e := range OnBreak(SnapshotWeeks().Last) {
		if !seen[showID(e)] {
			seen[showID(e)] = true
			out = append(out, e)
		}
	}
	return out
}

// AllEntries returns every entry of the loaded dates in date then air-time order, donghua included.
func AllEntries() []ShowEntry {
	timetableMu.RLock()
	defer timetableMu.RUnlock()
	view := subView()
	var out []ShowEntry
	for _, date := range loadedDates() {
		out = append(out, view[date]...)
	}
	return out
}

// WeekSnapshot is last week's, this week's and next week's entries (date then air-time order, donghua included),
// read under one lock and one clock reading. ThisLoaded and NextLoaded say whether that week was fetched
// successfully since startup and holds entries.
type WeekSnapshot struct {
	Last, This, Next       []ShowEntry
	ThisLoaded, NextLoaded bool
}

// SnapshotWeeks reads the three weeks around the shared clock's now.
func SnapshotWeeks() WeekSnapshot {
	weeks := weekLayout(Now())
	timetableMu.RLock()
	defer timetableMu.RUnlock()
	view := subView()
	read := func(w timetableWeek) (entries []ShowEntry, loaded bool) {
		for d := range 7 {
			entries = append(entries, view[DateKey(w.monday.AddDate(0, 0, d))]...)
		}
		return entries, fetchedWeeks[DateKey(w.monday)] && len(entries) > 0
	}
	var s WeekSnapshot
	s.Last, _ = read(weeks[0])
	s.This, s.ThisLoaded = read(weeks[1])
	s.Next, s.NextLoaded = read(weeks[2])
	return s
}

// timetableWeek is one ISO week of the loaded range: its Monday (midnight UTC) and ISO year and number.
type timetableWeek struct {
	monday time.Time
	year   int
	week   int
}

// weekLayout returns last week, this week and next week around now (UTC).
func weekLayout(now time.Time) []timetableWeek {
	thisMonday := mondayOf(now)
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
	return LoadedRangeAt(Now())
}

// LoadedRangeAt is LoadedRange for the instant now, for a caller that already holds one.
func LoadedRangeAt(now time.Time) (first, last time.Time) {
	weeks := weekLayout(now)
	return weeks[0].monday, weeks[len(weeks)-1].monday.AddDate(0, 0, 6)
}

// UpdateAnimeSchedule fetches last week, this week and next week from the timetables API (each by ISO year and
// week, as a UTC week) and updates the loaded timetable. A week that fails to fetch keeps its previous entries.
func UpdateAnimeSchedule(ctx context.Context, key string) {
	if key == "" {
		return
	}
	weeks := weekLayout(Now())
	first, last := weeks[0].monday, weeks[len(weeks)-1].monday.AddDate(0, 0, 6)
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
			if t, perr := time.Parse(dateKeyLayout, date); perr == nil && !t.Before(w.monday) && t.Before(w.monday.AddDate(0, 0, 7)) {
				fetched[date] = entries
			}
		}
	}
	if len(ok) == 0 {
		return
	}

	timetableMu.Lock()
	defer timetableMu.Unlock()
	next := make(map[string][]ShowEntry, len(timetable))
	for date, entries := range timetable {
		t, perr := time.Parse(dateKeyLayout, date)
		if perr != nil || t.Before(first) || t.After(last) {
			continue
		}
		if ok[mondayOf(t)] {
			continue
		}
		next[date] = entries
	}
	for date, entries := range fetched {
		next[date] = entries
	}
	timetable = next
	timetablePopulated = true
	// Only the weeks of the current layout stay fetched; the window moving past a week drops it.
	weeksFetched := make(map[string]bool, len(weeks))
	for _, w := range weeks {
		if k := DateKey(w.monday); ok[w.monday] || fetchedWeeks[k] {
			weeksFetched[k] = true
		}
	}
	fetchedWeeks = weeksFetched
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
		if err := waitForReset(ctx, resp.Header); err != nil {
			return nil, err
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
	timetableMu.Lock()
	lastFetchErr = err
	timetableMu.Unlock()
}

// processTimetable builds UTC date -> []ShowEntry from API, keeping every row with its track (a missing or
// unknown one counts as raw) and each row that repeats another in show, track, air time and episode once.
// The preferred track is applied when a day is read. Sorts each day by air time.
func processTimetable(list []TimetableAnime) map[string][]ShowEntry {
	byDate := make(map[string][]ShowEntry)
	for _, a := range list {
		date := DateKey(a.EpisodeDate)
		episodeStr := fmt.Sprintf("Ep %d", a.EpisodeNumber)
		if a.SubtractedEpisodeNumber > 0 && a.SubtractedEpisodeNumber < a.EpisodeNumber {
			episodeStr = fmt.Sprintf("Ep %d-%d", a.SubtractedEpisodeNumber, a.EpisodeNumber)
		}
		final := false
		if a.Episodes > 0 && a.EpisodeNumber >= a.Episodes {
			episodeStr += "F"
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

		airType := trackOf(a.AirType)
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
			Track:             airType,
			ImageVersionRoute: a.ImageVersionRoute,
			Final:             final,
		})
	}

	for d := range byDate {
		byDate[d] = normalizeRows(byDate[d])
		sort.Slice(byDate[d], func(i, j int) bool {
			return byDate[d][i].AirTimeUnix < byDate[d][j].AirTimeUnix
		})
	}
	return byDate
}
