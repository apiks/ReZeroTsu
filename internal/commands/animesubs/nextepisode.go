package animesubs

import (
	"fmt"
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
	// Track names the track whose premiere Entry is ("sub", "dub" or "raw") when the subscription's set has
	// several members; empty otherwise.
	Track animeschedule.Track
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

// findNextEpisode applies the next-episode rule over entries (every loaded week). A show matches by the rule of
// sameShow. A show with no timetable entry falls back to the upcoming list's premiere.
func findNextEpisode(entries []animeschedule.ShowEntry, upcoming []animeschedule.UpcomingShow, route, title string, set animeschedule.SubscriptionTracks, now time.Time) nextEpisode {
	nowUnix := now.Unix()
	var next, last animeschedule.ShowEntry
	var haveNext, haveLast bool
	for _, e := range entries {
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
		return nextEpisode{Entry: next, Found: true, Upcoming: true}
	case haveLast:
		return nextEpisode{Entry: last, Found: true}
	}
	for _, u := range upcoming {
		e := upcomingEntry(u)
		if !sameShow(e, route, title) {
			continue
		}
		// The premiere is the set's earliest known one, labelled with its track when the set has several.
		e.AirTimeUnix = 0
		var track animeschedule.Track
		if p, member, ok := u.SetPremiere(set); ok {
			e.AirTimeUnix = p.Unix()
			if len(set.Members()) > 1 {
				track = member
			}
		}
		return nextEpisode{Entry: e, Found: true, Upcoming: e.AirTimeUnix == 0 || e.AirTimeUnix >= nowUnix, Premiere: true, Track: track}
	}
	return nextEpisode{}
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
		return fmt.Sprintf("Next episode: %s, <t:%d:F> (<t:%d:R>).", n.Entry.Episode, unix, unix)
	case statusAired:
		return fmt.Sprintf("%s aired <t:%d:R>. You'll get a DM when the next one airs.", n.Entry.Episode, unix)
	}
	return ""
}

// listStatus is the list-line suffix: the media-type label for non-TV, then one status.
func (n nextEpisode) listStatus() string {
	unix := n.Entry.AirTimeUnix
	out := ""
	if label := n.Entry.MediaType.Label(); label != "" && n.Found {
		out = " · " + label
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
		return out + fmt.Sprintf(" · %s <t:%d:R>", n.Entry.Episode, unix)
	case statusAired:
		return out + fmt.Sprintf(" · %s aired <t:%d:R>", n.Entry.Episode, unix)
	}
	return " · no upcoming episode listed"
}
