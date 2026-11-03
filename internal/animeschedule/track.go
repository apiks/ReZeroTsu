package animeschedule

import "strings"

// Track is one airing stream of a show: raw, sub or dub.
type Track string

const (
	TrackRaw Track = "raw"
	TrackSub Track = "sub"
	TrackDub Track = "dub"
)

// trackOf reads a track from the API's Track; a missing or unknown one counts as raw.
func trackOf(s string) Track {
	switch Track(s) {
	case TrackSub, TrackDub:
		return Track(s)
	}
	return TrackRaw
}

// Known is the track with a missing or unknown value read as raw.
func (t Track) Known() Track { return trackOf(string(t)) }

// Label is the track's line label: RAW, SUB or DUB.
func (t Track) Label() string {
	return strings.ToUpper(string(trackOf(string(t))))
}

// PreferredTrack is the track a page wants shown: Sub, Dub or Raw. Its zero value is Sub.
type PreferredTrack int

const (
	PreferSub PreferredTrack = iota
	PreferDub
	PreferRaw
)

// ParsePreferredTrack reads "sub", "dub" or "raw"; ok is false for anything else.
func ParsePreferredTrack(s string) (PreferredTrack, bool) {
	switch s {
	case "sub":
		return PreferSub, true
	case "dub":
		return PreferDub, true
	case "raw":
		return PreferRaw, true
	}
	return PreferSub, false
}

// Next is the preference after p in the settings button's cycle: Sub, Dub, Raw, Sub.
func (p PreferredTrack) Next() PreferredTrack {
	switch p {
	case PreferSub:
		return PreferDub
	case PreferDub:
		return PreferRaw
	}
	return PreferSub
}

// Title is the capitalised name shown to members: Sub, Dub or Raw.
func (p PreferredTrack) Title() string {
	s := p.String()
	return strings.ToUpper(s[:1]) + s[1:]
}

// String is the lower-case name ParsePreferredTrack reads.
func (p PreferredTrack) String() string {
	switch p {
	case PreferDub:
		return "dub"
	case PreferRaw:
		return "raw"
	}
	return "sub"
}

// showWeek is one show in one UTC ISO week, identified by the week's Monday (DateKey).
type showWeek struct{ show, monday string }

// weekTracks records which tracks one show has in one week.
type weekTracks struct{ sub, dub bool }

// shown returns the track the preference shows for a show with these tracks that week. Sub never shows dub, and
// Raw shows raw whatever else exists.
func (p PreferredTrack) shown(w weekTracks) Track {
	switch p {
	case PreferDub:
		if w.dub {
			return TrackDub
		}
		if w.sub {
			return TrackSub
		}
	case PreferSub:
		if w.sub {
			return TrackSub
		}
	}
	return TrackRaw
}

// mondayKeyOf is the DateKey of the Monday of the week holding the date key, or "" when it is malformed.
func mondayKeyOf(date string) string {
	t, err := ParseDateKey(date)
	if err != nil {
		return ""
	}
	return DateKey(mondayOf(t))
}

// tracksByWeek records, for every show and week among the dates, the tracks the show has.
func tracksByWeek(days map[string][]ShowEntry) map[showWeek]weekTracks {
	weeks := make(map[showWeek]weekTracks)
	for date, rows := range days {
		monday := mondayKeyOf(date)
		for _, e := range rows {
			k := showWeek{show: showID(e), monday: monday}
			w := weeks[k]
			switch e.Track {
			case TrackSub:
				w.sub = true
			case TrackDub:
				w.dub = true
			}
			weeks[k] = w
		}
	}
	return weeks
}

// chainDay keeps the rows of one date whose track is the one pref shows for their show that week.
func chainDay(date string, rows []ShowEntry, weeks map[showWeek]weekTracks, pref PreferredTrack) []ShowEntry {
	monday := mondayKeyOf(date)
	var out []ShowEntry
	for _, e := range rows {
		if e.Track == pref.shown(weeks[showWeek{show: showID(e), monday: monday}]) {
			out = append(out, e)
		}
	}
	return out
}

// chainDays applies pref to every loaded date. The caller holds timetableMu.
func chainDays(days map[string][]ShowEntry, pref PreferredTrack) map[string][]ShowEntry {
	weeks := tracksByWeek(days)
	out := make(map[string][]ShowEntry, len(days))
	for date, rows := range days {
		out[date] = chainDay(date, rows, weeks, pref)
	}
	return out
}

// normalizeRows gives every row a valid track and drops a row repeating another in show, track, air time and
// episode. Order is kept.
func normalizeRows(rows []ShowEntry) []ShowEntry {
	type key struct {
		show, episode string
		track         Track
		air           int64
	}
	seen := make(map[key]bool, len(rows))
	out := make([]ShowEntry, 0, len(rows))
	for _, e := range rows {
		e.Track = trackOf(string(e.Track))
		k := key{showID(e), e.Episode, e.Track, e.AirTimeUnix}
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, e)
	}
	return out
}
