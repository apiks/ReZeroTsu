package animesubs

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf16"

	"ReZeroTsu/internal/animeschedule"
	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/database/anime_subs"
	"ReZeroTsu/internal/discord"
)

const (
	embedTitleAnimeSubs  = "Anime subscriptions"
	maxUserSubscriptions = 100
)

// maxListTitleUnits is how much of a title a list line keeps, in the UTF-16 units Discord counts. A page holds 25
// lines, each with a status of up to about 70 units, so 25 clipped lines and the footer stay under the 4096 limit.
const maxListTitleUnits = 80

// clipListTitle cuts a title to maxListTitleUnits UTF-16 units at a character boundary and marks the cut.
func clipListTitle(title string) string {
	units := 0
	for i, r := range title {
		w := utf16.RuneLen(r)
		if units+w > maxListTitleUnits {
			return title[:i] + "…"
		}
		units += w
	}
	return title
}

func subsDescriptionForPage(list []*anime_subs.ShowSub, start, page, totalPages, n int) string {
	var b strings.Builder
	end := min(start+discord.MaxEmbedFields, len(list))
	upcoming, now := animeschedule.UpcomingShows(), animeschedule.Now()
	for j := start; j < end; j++ {
		if list[j] == nil {
			continue
		}
		fmt.Fprintf(&b, "%d. %s%s\n", j+1, clipListTitle(list[j].Show), findNextEpisode(loadTimetable(subscriptionTracks(list[j])), upcoming, list[j].Route, list[j].Show, subscriptionTracks(list[j]), now).listStatus())
	}
	endShow := min(start+discord.MaxEmbedFields, n)
	b.WriteString("\n")
	if totalPages > 1 {
		fmt.Fprintf(&b, "Page %d of %d. Showing %d–%d of %d.\n", page+1, totalPages, start+1, endShow, n)
	}
	return b.String()
}

// subscriptionTracks is the subscription tracks a stored entry holds; an entry without a set, or with an unreadable one,
// reads as Sub.
func subscriptionTracks(s *anime_subs.ShowSub) animeschedule.SubscriptionTracks {
	set, _ := animeschedule.ParseSubscriptionTracks(s.Tracks)
	return set
}

func docToSubList(doc *anime_subs.AnimeSubs) []*anime_subs.ShowSub {
	var list []*anime_subs.ShowSub
	for _, sub := range doc.Shows {
		if sub != nil {
			list = append(list, sub)
		}
	}
	return list
}

// findShowIn returns the show whose route is input (a suggestion's value), else whose display title matches input under anime_subs.MatchKey (case-insensitive and
// trimmed like the notifier matches names). Failing that, the first show whose romaji, English or native name
// matches; a title cut to Discord's choice limit also matches. It returns the zero value and false if none.
func findShowIn(shows []animeschedule.ShowEntry, input string) (animeschedule.ShowEntry, bool) {
	key := anime_subs.MatchKey(input)
	if key == "" {
		return animeschedule.ShowEntry{}, false
	}
	for _, show := range shows {
		if show.Route != "" && anime_subs.MatchKey(show.Route) == key {
			return show, true
		}
	}
	for _, show := range shows {
		if anime_subs.MatchKey(show.Name) == key || anime_subs.MatchKey(clip(show.Name)) == key {
			return show, true
		}
	}
	for _, show := range shows {
		for _, n := range show.AllNames() {
			if anime_subs.MatchKey(n) == key {
				return show, true
			}
		}
	}
	return animeschedule.ShowEntry{}, false
}

// findShowByName returns the candidate show (loaded weeks or upcoming) matching name by any of its four names, or
// the zero value and false.
func findShowByName(name string, hideDonghua bool) (animeschedule.ShowEntry, bool) {
	var shows []animeschedule.ShowEntry
	for _, c := range candidates(hideDonghua) {
		shows = append(shows, c.ShowEntry)
	}
	return findShowIn(shows, name)
}

// titleName is "Title (English)" when the English name differs from the title, else the title.
func titleName(e animeschedule.ShowEntry) string {
	if en := strings.TrimSpace(e.English); en != "" && anime_subs.MatchKey(en) != anime_subs.MatchKey(e.Name) {
		return fmt.Sprintf("%s (%s)", e.Name, en)
	}
	return e.Name
}

// subsStore is the narrow persistence surface of the subscription commands. It deliberately has no
// whole-document rewrite: every change is a single-statement operation.
type subsStore interface {
	GetByID(ctx context.Context, id string) (*anime_subs.AnimeSubs, error)
	AddShowIfAbsent(ctx context.Context, id string, sub *anime_subs.ShowSub) (bool, error)
	RemoveShows(ctx context.Context, id string, showNames []string) error
	SetShowTracks(ctx context.Context, id, showName, tracks string, marks []anime_subs.Mark) error
	ClearShows(ctx context.Context, id string) error
}

// showLookup resolves user input to a loaded-week or upcoming show; replaced in tests.
var showLookup = findShowByName

// subCore adds the subscription and answers. confirmed is true for a reply that confirms the subscription (new or
// already held), which is when the DMs footer applies.
func subCore(ctx context.Context, store subsStore, uid, animeInput string, set animeschedule.SubscriptionTracks, hideDonghua bool) (responseDesc string, confirmed bool, logErr error) {
	animeInput = strings.TrimSpace(animeInput)
	if animeInput == "" {
		return "Please provide an anime name: pick one of the suggestions, or check `/schedule` for the shows on the timetable.", false, nil
	}
	show, ok := showLookup(animeInput, hideDonghua)
	if !ok {
		msg := "I could not find that airing or upcoming show. Pick one of the suggestions or check the spelling, and see `/schedule` for the timetable."
		// A name that matches only a hidden donghua is told where to follow it, not that it does not exist.
		if hideDonghua {
			if hidden, found := showLookup(animeInput, false); found && hidden.Donghua {
				msg += fmt.Sprintf(" This server hides donghua. Run %s in a DM with ZeroTsu to follow it.", commands.CommandMention("subscription", "add"))
			}
		}
		return msg, false, nil
	}
	showName := show.Name

	doc, err := store.GetByID(ctx, uid)
	if err != nil {
		return "Could not load your subscriptions.", false, err
	}
	if doc != nil {
		for _, sub := range doc.Shows {
			if sub != nil && sub.Matches(show.Route, showName) {
				return replaceTracks(ctx, store, uid, sub, showName, set, animeschedule.Now())
			}
		}
	}
	if doc != nil && len(doc.Shows) >= maxUserSubscriptions {
		return fmt.Sprintf("You've reached the maximum number of subscriptions (%d). Unsubscribe from some shows to add more.", maxUserSubscriptions), false, nil
	}

	now := animeschedule.Now()
	newSub := &anime_subs.ShowSub{Show: showName, Route: show.Route, Guild: false, Tracks: set.Stored()}
	seedMemory(newSub, set, now)
	added, err := store.AddShowIfAbsent(ctx, uid, newSub)
	if err != nil {
		return "Could not save subscription.", false, err
	}
	if !added {
		return fmt.Sprintf("You are already subscribed to `%s`.", showName), true, nil
	}
	desc := fmt.Sprintf("Subscribed to **%s**.", showName)
	if set != animeschedule.SubscribeSub {
		desc = fmt.Sprintf("Subscribed to **%s** (%s).", showName, set.Title())
	}
	if line := findNextEpisode(loadTimetable(set), animeschedule.UpcomingShows(), show.Route, showName, set, now).confirmationLine(); line != "" {
		desc += "\n" + line
	}
	return desc, true, nil
}

// replaceTracks answers a re-add of a show the member already follows. The same set is already subscribed; a
// different set replaces the stored one in a single statement. Tracks kept from the old set keep their memory;
// each track new to the set is seeded from its own row that aired today (or cleared, if none), so switching
// never DMs something that aired earlier today. It never counts against the subscription limit.
func replaceTracks(ctx context.Context, store subsStore, uid string, existing *anime_subs.ShowSub, showName string, set animeschedule.SubscriptionTracks, now time.Time) (string, bool, error) {
	old, _ := animeschedule.ParseSubscriptionTracks(existing.Tracks)
	if old == set {
		return fmt.Sprintf("You are already subscribed to `%s`.", showName), true, nil
	}
	seeded := &anime_subs.ShowSub{Show: existing.Show, Route: existing.Route}
	seedMemory(seeded, set, now)
	var marks []anime_subs.Mark
	for _, m := range set.Tracks() {
		if slices.Contains(old.Tracks(), m) {
			continue
		}
		air, episode := seeded.Memory(string(m))
		marks = append(marks, anime_subs.Mark{Track: string(m), AirUnix: air, Episode: episode})
	}
	if err := store.SetShowTracks(ctx, uid, existing.Show, set.Stored(), marks); err != nil {
		return "Could not save subscription.", false, err
	}
	return fmt.Sprintf("**%s** now alerts for %s.", showName, set.Title()), true, nil
}

// notSubscribed is the reply for removing a show the member is not subscribed to, pointing at the list.
func notSubscribed(input string) string {
	return fmt.Sprintf("You are not subscribed to `%s`. See %s for your subscriptions.", input, commands.CommandMention("subscription", "list"))
}

func unsubCore(ctx context.Context, store subsStore, uid, animeInput string) (responseDesc string, logErr error) {
	animeInput = strings.TrimSpace(animeInput)
	if animeInput == "" {
		return "Please provide an anime name to unsubscribe from.", nil
	}
	doc, err := store.GetByID(ctx, uid)
	if err != nil {
		return "Could not load your subscriptions.", err
	}
	if doc == nil || len(doc.Shows) == 0 {
		return notSubscribed(animeInput), nil
	}

	// Resolve the stored titles (exact case) the input names, then remove exactly those.
	var stored []string
	for _, sub := range doc.Shows {
		if sub != nil && anime_subs.MatchKey(sub.Show) == anime_subs.MatchKey(animeInput) {
			stored = append(stored, sub.Show)
		}
	}
	if len(stored) == 0 {
		// An autocomplete choice carries the title cut to Discord's limit, as the Remove picker does.
		for _, sub := range doc.Shows {
			if sub != nil && anime_subs.MatchKey(clip(sub.Show)) == anime_subs.MatchKey(animeInput) {
				stored = append(stored, sub.Show)
			}
		}
		if len(stored) > 1 {
			return fmt.Sprintf("That name matches more than one of your subscriptions, so none were removed: `%s`. Use the Remove picker from `/subscription list` to choose one.", strings.Join(stored, "`, `")), nil
		}
	}
	if len(stored) == 0 {
		return notSubscribed(animeInput), nil
	}

	if err := store.RemoveShows(ctx, uid, stored); err != nil {
		return "Could not update subscriptions.", err
	}
	return fmt.Sprintf("Success! You have unsubscribed from `%s`.", animeInput), nil
}

// seedMemory marks, for each track of the set, the row of that track that already aired today as sent, so
// subscribing after an airing never DMs it. A track with no aired row today keeps no memory.
func seedMemory(sub *anime_subs.ShowSub, set animeschedule.SubscriptionTracks, now time.Time) {
	byTrack := make(map[animeschedule.Track][]animeschedule.ShowEntry)
	for _, row := range animeschedule.SelectRows(now, set) {
		for _, m := range row.TracksIn(set) {
			byTrack[m] = append(byTrack[m], row.ShowEntry)
		}
	}
	for _, m := range set.Tracks() {
		if aired, ok := airedTodayEntry(byTrack[m], sub.Route, sub.Show, now.Unix()); ok {
			sub.Remember(anime_subs.Mark{Track: string(m), AirUnix: aired.AirTimeUnix})
		}
	}
}

// airedTodayEntry returns the latest of today's entries for the show with this route and title that has already
// aired at nowUnix: the notifier's air-time rule then covers the earlier ones. Entries are matched like the
// notifier does (ShowSub.Matches: by route when the show has one, else by title under anime_subs.MatchKey), and the
// entry is today's own, so the seeded air time is today's even for a show the schedule lists on several weekdays.
func airedTodayEntry(todayShows []animeschedule.ShowEntry, route, showName string, nowUnix int64) (animeschedule.ShowEntry, bool) {
	sub := &anime_subs.ShowSub{Show: showName, Route: route}
	var latest animeschedule.ShowEntry
	found := false
	for _, e := range todayShows {
		if sub.Matches(e.Route, e.Name) && e.AirTimeUnix <= nowUnix && (!found || e.AirTimeUnix > latest.AirTimeUnix) {
			latest, found = e, true
		}
	}
	return latest, found
}
