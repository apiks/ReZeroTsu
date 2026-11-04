package animesubs

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"ReZeroTsu/internal/animeschedule"
	"ReZeroTsu/internal/database/anime_subs"
)

// msgDMsFooter closes every add confirmation: alerts are DMs, and closed DMs are the usual reason one never arrives.
const msgDMsFooter = "Episode alerts arrive by DM. Keep DMs from ZeroTsu open."

// nextEpisode is a show's place on the loaded timetable weeks.
type nextEpisode struct {
	// Entry is the earliest entry at or after now, or failing that the latest aired one.
	Entry animeschedule.ShowEntry
	// Found is false when the show has no entry in the loaded weeks.
	Found bool
	// Upcoming is true when Entry airs at or after now, or is an upcoming-list show with no date yet.
	Upcoming bool
	// Premiere is true when Entry is an upcoming-list show; Entry.AirTimeUnix is then its premiere, 0 while undated.
	Premiere bool
	// Track names the track of Entry ("sub", "dub" or "raw"), a row's or a premiere's, when the subscription's set
	// has several tracks; empty otherwise.
	Track animeschedule.Track
	// Set is the subscription's tracks.
	Set animeschedule.SubscriptionTracks
	// Unlisted are the tracks of a non-Sub set that are not listed yet.
	Unlisted []animeschedule.Track
}

// episode is the entry's episode label, with its track when the set has several tracks: "Ep 2 (dub)".
func (n nextEpisode) episode() string {
	if n.Track == "" {
		return n.Entry.Episode
	}
	return fmt.Sprintf("%s (%s)", n.Entry.Episode, n.Track)
}

// trackNoun is how a track reads in "a dubbed episode": subbed, dubbed or raw.
func trackNoun(t animeschedule.Track) string {
	switch t {
	case animeschedule.TrackSub:
		return "subbed"
	case animeschedule.TrackDub:
		return "dubbed"
	}
	return "raw"
}

// unlistedSentences are the confirmation sentences for each track not listed yet.
func (n nextEpisode) unlistedSentences() string {
	var out []string
	for _, m := range n.Unlisted {
		out = append(out, fmt.Sprintf("No %s is listed yet. You'll get a DM when a %s episode airs.", m, trackNoun(m)))
	}
	return strings.Join(out, "\n")
}

// noneListed is true when the set is not plain Sub and no track is listed.
func (n nextEpisode) noneListed() bool {
	return len(n.Unlisted) > 0 && len(n.Unlisted) == len(n.Set.Tracks())
}

// unlistedList is the list-line phrase for tracks not listed yet: "no dub listed yet", "no sub or dub listed
// yet".
func (n nextEpisode) unlistedList() string {
	names := make([]string, len(n.Unlisted))
	for i, m := range n.Unlisted {
		names[i] = string(m)
	}
	if len(names) > 1 {
		names = []string{strings.Join(names[:len(names)-1], ", ") + " or " + names[len(names)-1]}
	}
	return "no " + names[0] + " listed yet"
}

// itsPremiere is the subject of a premiere or release sentence: "It", or "Its dub" when the track is named.
func (n nextEpisode) itsPremiere() string {
	if n.Track == "" {
		return "It"
	}
	return "Its " + string(n.Track)
}

// trackLabelled prefixes a list-line verb with the premiere's track, as in "dub premieres".
func (n nextEpisode) trackLabelled(verb string) string {
	if n.Track == "" {
		return verb
	}
	return string(n.Track) + " " + verb
}

// sameShow reports whether e is the show a subscription to this route and title is subscribed to: the rule of
// anime_subs.ShowSub.Matches (ADR-0002), by route when the subscription has one, else by title under MatchKey.
func sameShow(e animeschedule.ShowEntry, route, title string) bool {
	return (&anime_subs.ShowSub{Show: title, Route: route}).Matches(e.Route, e.Name)
}

// timetableFor is what a subscription reads from the loaded weeks: every row of every track, and the rows its set
// selects.
type timetableFor struct {
	Rows     []animeschedule.ShowEntry
	Selected []animeschedule.SelectedRow
}

// loadTimetable reads the loaded weeks for a subscription's set.
func loadTimetable(set animeschedule.SubscriptionTracks) timetableFor {
	return timetableFor{Rows: animeschedule.LoadedTrackRows(), Selected: animeschedule.SelectLoaded(set)}
}

// findNextEpisode applies the next-episode rule: the earliest row the set selects at or after now across the
// loaded weeks, else the latest aired one, else the earliest known premiere of the set's tracks on the upcoming
// list. A show matches by the rule of sameShow. The track is named when the set has several tracks. Each track
// of a set other than plain Sub that the show has no row of, and that the upcoming list does not give either, is
// not listed yet; a raw fallback row lists the Sub track, as it is the row Sub will DM.
func findNextEpisode(tt timetableFor, upcoming []animeschedule.UpcomingShow, route, title string, set animeschedule.SubscriptionTracks, now time.Time) nextEpisode {
	n := placeOnTimetable(tt.Selected, upcoming, route, title, set, now)
	n.Set = set
	if set != animeschedule.SubscribeSub {
		n.Unlisted = unlistedTracks(tt, upcoming, route, title, set)
	}
	return n
}

func placeOnTimetable(selected []animeschedule.SelectedRow, upcoming []animeschedule.UpcomingShow, route, title string, set animeschedule.SubscriptionTracks, now time.Time) nextEpisode {
	nowUnix := now.Unix()
	// named is the track a line names: the track itself when the set has several, else none.
	named := func(t animeschedule.Track) animeschedule.Track {
		if len(set.Tracks()) > 1 {
			return t.Known()
		}
		return ""
	}
	var next, last animeschedule.ShowEntry
	var haveNext, haveLast bool
	for _, r := range selected {
		e := r.ShowEntry
		if !sameShow(e, route, title) {
			continue
		}
		if e.AirTimeUnix >= nowUnix {
			if !haveNext || e.AirTimeUnix < next.AirTimeUnix {
				next, haveNext = e, true
			}
		} else if !haveLast || e.AirTimeUnix > last.AirTimeUnix {
			last, haveLast = e, true
		}
	}
	switch {
	case haveNext:
		return nextEpisode{Entry: next, Found: true, Upcoming: true, Track: named(next.Track)}
	case haveLast:
		return nextEpisode{Entry: last, Found: true, Track: named(last.Track)}
	}
	for _, u := range upcoming {
		e := upcomingEntry(u)
		if !sameShow(e, route, title) {
			continue
		}
		// The premiere is the set's earliest known one, labelled with its track when the set has several.
		e.AirTimeUnix = 0
		var track animeschedule.Track
		if p, premiereTrack, ok := u.SetPremiere(set); ok {
			e.AirTimeUnix = p.Unix()
			track = named(premiereTrack)
		}
		return nextEpisode{Entry: e, Found: true, Upcoming: e.AirTimeUnix == 0 || e.AirTimeUnix >= nowUnix, Premiere: true, Track: track}
	}
	return nextEpisode{}
}

// unlistedTracks are the tracks of the set the show has no row of in the loaded weeks and that the upcoming list
// does not give it either.
func unlistedTracks(tt timetableFor, upcoming []animeschedule.UpcomingShow, route, title string, set animeschedule.SubscriptionTracks) []animeschedule.Track {
	var out []animeschedule.Track
	for _, m := range set.Tracks() {
		listed := false
		for _, e := range tt.Rows {
			if e.Track.Known() == m && sameShow(e, route, title) {
				listed = true
				break
			}
		}
		// The Sub track is listed by its raw fallback row too: that row is what the Sub track will DM.
		for _, r := range tt.Selected {
			if listed || m != animeschedule.TrackSub {
				break
			}
			listed = slices.Contains(r.Tracks, animeschedule.TrackSub) && sameShow(r.ShowEntry, route, title)
		}
		for _, u := range upcoming {
			if listed {
				break
			}
			listed = u.HasTrack(m) && sameShow(upcomingEntry(u), route, title)
		}
		if !listed {
			out = append(out, m)
		}
	}
	return out
}

// statusKind is the case a show's place on the timetable falls in; the confirmation and the list line each render
// it, so a new case is added here once.
type statusKind int

const (
	statusUnknown statusKind = iota
	statusPremiereTBA
	statusMovieTBA
	statusPremiere
	statusPremiered
	statusMovieUpcoming
	statusMovieReleased
	statusNextEpisode
	statusAired
)

// kind classifies n. A movie has a release, not episodes, so it is a movie case whenever its media type says so.
func (n nextEpisode) kind() statusKind {
	movie := n.Entry.MediaType == animeschedule.MediaMovie
	switch {
	case !n.Found:
		return statusUnknown
	case n.Premiere && n.Entry.AirTimeUnix == 0 && movie:
		return statusMovieTBA
	case n.Premiere && n.Entry.AirTimeUnix == 0:
		return statusPremiereTBA
	case movie && n.Upcoming:
		return statusMovieUpcoming
	case movie:
		return statusMovieReleased
	case n.Premiere && n.Upcoming:
		return statusPremiere
	case n.Premiere:
		return statusPremiered
	case n.Upcoming:
		return statusNextEpisode
	}
	return statusAired
}

// confirmationLine is the sentence after "Subscribed to ..." saying when the show airs next; "" when unknown.
func (n nextEpisode) confirmationLine() string {
	line := n.placeLine()
	switch {
	case n.noneListed():
		return n.unlistedSentences()
	case len(n.Unlisted) == 0:
		return line
	case line == "":
		return n.unlistedSentences()
	}
	return line + "\n" + n.unlistedSentences()
}

// placeLine is the sentence for the show's place on the timetable; "" when unknown.
func (n nextEpisode) placeLine() string {
	unix := n.Entry.AirTimeUnix
	switch n.kind() {
	case statusMovieTBA:
		return "Its release date isn't announced yet. You'll get a DM when it is released."
	case statusPremiereTBA:
		return "Its premiere date isn't announced yet. You'll get a DM when episode 1 airs."
	case statusPremiere:
		return fmt.Sprintf("%s premieres <t:%d:F> (<t:%d:R>). You'll get a DM when episode 1 airs.", n.itsPremiere(), unix, unix)
	case statusPremiered:
		return fmt.Sprintf("%s premiered <t:%d:R>. You'll get a DM when the next episode airs.", n.itsPremiere(), unix)
	case statusMovieUpcoming:
		return fmt.Sprintf("%s releases <t:%d:F> (<t:%d:R>).", n.itsPremiere(), unix, unix)
	case statusMovieReleased:
		return fmt.Sprintf("%s released <t:%d:R>.", n.itsPremiere(), unix)
	case statusNextEpisode:
		return fmt.Sprintf("Next episode: %s, <t:%d:F> (<t:%d:R>).", n.episode(), unix, unix)
	case statusAired:
		return fmt.Sprintf("%s aired <t:%d:R>. You'll get a DM when the next one airs.", n.episode(), unix)
	}
	return ""
}

// listStatus is the list-line suffix: the media-type label for non-TV, then one status.
func (n nextEpisode) listStatus() string {
	unix := n.Entry.AirTimeUnix
	out := ""
	if label := n.Entry.TypeLabel(); label != "" && n.Found {
		out = " · " + label
	}
	if n.Set != animeschedule.SubscribeSub {
		out += " · " + n.Set.Title()
	}
	if n.noneListed() {
		return out + " · " + n.unlistedList()
	}
	switch n.kind() {
	case statusMovieTBA:
		return out + " · release date TBA"
	case statusPremiereTBA:
		return out + " · premiere date TBA"
	case statusPremiere:
		return out + fmt.Sprintf(" · %s <t:%d:R>", n.trackLabelled("premieres"), unix)
	case statusPremiered:
		return out + fmt.Sprintf(" · %s <t:%d:R>", n.trackLabelled("premiered"), unix)
	case statusMovieUpcoming:
		return out + fmt.Sprintf(" · %s <t:%d:R>", n.trackLabelled("releases"), unix)
	case statusMovieReleased:
		return out + fmt.Sprintf(" · %s <t:%d:R>", n.trackLabelled("released"), unix)
	case statusNextEpisode:
		return out + fmt.Sprintf(" · %s <t:%d:R>", n.episode(), unix)
	case statusAired:
		return out + fmt.Sprintf(" · %s aired <t:%d:R>", n.episode(), unix)
	}
	return " · no upcoming episode listed"
}
