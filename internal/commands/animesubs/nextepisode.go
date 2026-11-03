package animesubs

import (
	"fmt"
	"time"

	"ReZeroTsu/internal/animeschedule"
	"ReZeroTsu/internal/database/anime_subs"
)

// msgDMsFooter closes every add confirmation: alerts are DMs, and closed DMs are the usual reason one never arrives.
const msgDMsFooter = "Episode alerts arrive by DM. Keep DMs from ZeroTsu open."

// mediaTypeLabels names the non-TV media types; TV carries no label.
var mediaTypeLabels = map[animeschedule.MediaType]string{
	animeschedule.MediaTVShort: "TV Short",
	animeschedule.MediaONA:     "ONA",
	animeschedule.MediaDonghua: "Donghua",
	animeschedule.MediaOVA:     "OVA",
	animeschedule.MediaSpecial: "Special",
	animeschedule.MediaMovie:   "Movie",
}

// nextEpisode is a show's place on the loaded timetable weeks.
type nextEpisode struct {
	// Entry is the earliest entry at or after now, or failing that the latest aired one.
	Entry animeschedule.ShowEntry
	// Found is false when the show has no entry in the loaded weeks.
	Found bool
	// Upcoming is true when Entry airs at or after now.
	Upcoming bool
	// Premiere is true when Entry is an upcoming-list show; Entry.AirTimeUnix is then its premiere, 0 while undated.
	Premiere bool
}

// sameShow reports whether e is the show with this route and title: by route when both sides have one, else by
// title under anime_subs.MatchKey.
func sameShow(e animeschedule.ShowEntry, route, title string) bool {
	if route != "" && e.Route != "" {
		return e.Route == route
	}
	return anime_subs.MatchKey(e.Name) == anime_subs.MatchKey(title)
}

// findNextEpisode applies the next-episode rule over entries (every loaded week). A show matches by route when
// both sides have one, else by title under anime_subs.MatchKey. A show with no timetable entry falls back to the
// upcoming list's premiere.
func findNextEpisode(entries []animeschedule.ShowEntry, upcoming []animeschedule.UpcomingShow, route, title string, now time.Time) nextEpisode {
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
		if e := upcomingEntry(u); sameShow(e, route, title) {
			return nextEpisode{Entry: e, Found: true, Upcoming: true, Premiere: true}
		}
	}
	return nextEpisode{}
}

// confirmationLine is the sentence after "Subscribed to ..." saying when the show airs next; "" when unknown.
func (n nextEpisode) confirmationLine() string {
	if !n.Found {
		return ""
	}
	unix := n.Entry.AirTimeUnix
	movie := n.Entry.MediaType == animeschedule.MediaMovie
	switch {
	case n.Premiere && unix == 0 && movie:
		return "Its release date isn't announced yet."
	case n.Premiere && unix == 0:
		return "Its premiere date isn't announced yet. You'll get a DM when episode 1 airs."
	case n.Premiere && !movie:
		return fmt.Sprintf("It premieres <t:%d:F> (<t:%d:R>). You'll get a DM when episode 1 airs.", unix, unix)
	case movie && n.Upcoming:
		return fmt.Sprintf("It releases <t:%d:F> (<t:%d:R>).", unix, unix)
	case movie:
		return fmt.Sprintf("It released <t:%d:R>.", unix)
	case n.Upcoming:
		return fmt.Sprintf("Next episode: %s, <t:%d:F> (<t:%d:R>).", n.Entry.Episode, unix, unix)
	}
	return fmt.Sprintf("%s aired <t:%d:R>. You'll get a DM when the next one airs.", n.Entry.Episode, unix)
}

// listStatus is the list-line suffix: the media-type label for non-TV, then one status.
func (n nextEpisode) listStatus() string {
	if !n.Found {
		return " · no upcoming episode listed"
	}
	unix := n.Entry.AirTimeUnix
	out := ""
	if label := mediaTypeLabels[n.Entry.MediaType]; label != "" {
		out = " · " + label
	}
	movie := n.Entry.MediaType == animeschedule.MediaMovie
	switch {
	case n.Premiere && unix == 0 && movie:
		return out + " · release date TBA"
	case n.Premiere && unix == 0:
		return out + " · premiere date TBA"
	case n.Premiere && !movie:
		return out + fmt.Sprintf(" · premieres <t:%d:R>", unix)
	case movie && n.Upcoming:
		return out + fmt.Sprintf(" · releases <t:%d:R>", unix)
	case movie:
		return out + fmt.Sprintf(" · released <t:%d:R>", unix)
	case n.Upcoming:
		return out + fmt.Sprintf(" · %s <t:%d:R>", n.Entry.Episode, unix)
	}
	return out + fmt.Sprintf(" · %s aired <t:%d:R>", n.Entry.Episode, unix)
}
