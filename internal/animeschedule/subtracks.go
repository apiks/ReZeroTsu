package animeschedule

import (
	"slices"
	"strings"
	"time"
)

// SubscriptionTracks is the closed set of tracks a subscription alerts for. Its zero value is Sub, so a
// subscription stored without a set reads as Sub.
type SubscriptionTracks int

const (
	SubscribeSub SubscriptionTracks = iota
	SubscribeDub
	SubscribeRaw
	SubscribeSubDub
	SubscribeEvery
)

// ParseSubscriptionTracks reads a stored set ("sub", "dub", "raw", "sub_dub" or "every"); the empty string is Sub.
// ok is false for anything else.
func ParseSubscriptionTracks(s string) (SubscriptionTracks, bool) {
	switch s {
	case "", "sub":
		return SubscribeSub, true
	case "dub":
		return SubscribeDub, true
	case "raw":
		return SubscribeRaw, true
	case "sub_dub":
		return SubscribeSubDub, true
	case "every":
		return SubscribeEvery, true
	}
	return SubscribeSub, false
}

// String is the stored form ParseSubscriptionTracks reads.
func (s SubscriptionTracks) String() string {
	switch s {
	case SubscribeDub:
		return "dub"
	case SubscribeRaw:
		return "raw"
	case SubscribeSubDub:
		return "sub_dub"
	case SubscribeEvery:
		return "every"
	}
	return "sub"
}

// Title is the name shown to members: Sub, Dub, Raw, Sub and Dub, or Every track.
func (s SubscriptionTracks) Title() string {
	switch s {
	case SubscribeSubDub:
		return "Sub and Dub"
	case SubscribeEvery:
		return "Every track"
	}
	str := s.String()
	return strings.ToUpper(str[:1]) + str[1:]
}

// Tracks lists the tracks in the set: Sub, then Dub, then Raw.
func (s SubscriptionTracks) Tracks() []Track {
	switch s {
	case SubscribeDub:
		return []Track{TrackDub}
	case SubscribeRaw:
		return []Track{TrackRaw}
	case SubscribeSubDub:
		return []Track{TrackSub, TrackDub}
	case SubscribeEvery:
		return []Track{TrackSub, TrackDub, TrackRaw}
	}
	return []Track{TrackSub}
}

// SelectedRow is a timetable row a subscription's set selects, with every track of the set that selected it.
type SelectedRow struct {
	ShowEntry
	Tracks []Track
}

// TracksIn are the tracks of the row that belong to set.
func (r SelectedRow) TracksIn(set SubscriptionTracks) []Track {
	var out []Track
	for _, m := range r.Tracks {
		if slices.Contains(set.Tracks(), m) {
			out = append(out, m)
		}
	}
	return out
}

// SelectRows returns the rows of the UTC date of day that the set selects, in timetable order, donghua included.
// Sub selects its sub rows, or the show's raw rows in a week the show has no sub row; Dub and Raw select exactly
// their track. A row more than one track of the set selects is returned once, with each of them.
func SelectRows(day time.Time, set SubscriptionTracks) []SelectedRow {
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
	out := selectFrom(key, week[key], tracksByWeek(week), set)
	timetableMu.RUnlock()
	return out
}

// SelectLoaded returns every loaded row the set selects, in date then air-time order, donghua included, by the
// same rule as SelectRows. A row more than one track of the set selects is returned once, with each of them.
func SelectLoaded(set SubscriptionTracks) []SelectedRow {
	timetableMu.RLock()
	defer timetableMu.RUnlock()
	weeks := tracksByWeek(timetable)
	var out []SelectedRow
	for _, date := range loadedDates() {
		out = append(out, selectFrom(date, timetable[date], weeks, set)...)
	}
	return out
}

// LoadedTrackRows returns every loaded row of every track, in date then air-time order, donghua included. A
// subscription reads it to see which tracks a show has in the loaded weeks.
func LoadedTrackRows() []ShowEntry {
	timetableMu.RLock()
	defer timetableMu.RUnlock()
	var out []ShowEntry
	for _, date := range loadedDates() {
		out = append(out, timetable[date]...)
	}
	return out
}

// selectFrom keeps the rows of one date that some track of the set selects. Sub's pick is the chain's: the Sub
// preference's track for the show that week.
func selectFrom(date string, rows []ShowEntry, weeks map[showWeek]weekTracks, set SubscriptionTracks) []SelectedRow {
	monday := mondayKeyOf(date)
	var out []SelectedRow
	for _, e := range rows {
		var tracks []Track
		for _, m := range set.Tracks() {
			if m == TrackSub && e.Track != PreferSub.shown(weeks[showWeek{show: showID(e), monday: monday}]) {
				continue
			}
			if m != TrackSub && e.Track != m {
				continue
			}
			tracks = append(tracks, m)
		}
		if len(tracks) > 0 {
			out = append(out, SelectedRow{ShowEntry: e, Tracks: tracks})
		}
	}
	return out
}

// TrackWeekSnapshot is one strict track's rows (Dub or Raw, no fallback) in last week, this week and next week,
// plus one row per show that has a row of the track in any loaded week.
type TrackWeekSnapshot struct {
	Last, This, Next []ShowEntry
	Started          []ShowEntry
}

// SnapshotTrackWeeks reads the rows of one track around the shared clock's now. The daily cleanup uses it to judge
// a Dub or Raw track of a subscription's set.
func SnapshotTrackWeeks(track Track) TrackWeekSnapshot {
	weeks := weekLayout(Now())
	timetableMu.RLock()
	defer timetableMu.RUnlock()
	read := func(w timetableWeek) []ShowEntry {
		var out []ShowEntry
		for d := range 7 {
			for _, e := range timetable[DateKey(w.monday.AddDate(0, 0, d))] {
				if e.Track == track {
					out = append(out, e)
				}
			}
		}
		return out
	}
	s := TrackWeekSnapshot{Last: read(weeks[0]), This: read(weeks[1]), Next: read(weeks[2])}
	seen := map[string]bool{}
	for _, date := range loadedDates() {
		for _, e := range timetable[date] {
			if e.Track == track && !seen[showID(e)] {
				seen[showID(e)] = true
				s.Started = append(s.Started, e)
			}
		}
	}
	return s
}
