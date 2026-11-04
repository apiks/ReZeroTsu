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

// maxListTitleUnits is how much of a title a list line keeps, in the UTF-16 units Discord counts, before the "…" that
// marks a cut. A page holds 25 lines. The worst status is about 78 units: " · TV Short · Donghua" (21) +
// " · Every track" (14) + " · Ep 100-101F (sub) aired <t:…:R>" (43). A line is also a prefix of up to 4 units
// ("25. "), the clipped title (77 + 1 for "…") and a newline. That is 25 × (4 + 78 + 78 + 1) = 4025 units, under 4096
// with room for the 37-unit footer (4062).
const maxListTitleUnits = 77

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

// memberNames calls a member's subscriptions by their shown names. A subscription stores only its title and route,
// so its show's other names come from the names on hand: every row of the loaded weeks, then the upcoming shows. The
// zero value is Title with no names on hand, which calls every subscription by its stored title.
type memberNames struct {
	pref   animeschedule.PreferredName
	onHand []animeschedule.ShowEntry
}

// namesFor reads the names on hand for a member who prefers pref.
func namesFor(pref animeschedule.PreferredName) memberNames {
	onHand := animeschedule.LoadedTrackRows()
	for _, u := range animeschedule.UpcomingShows() {
		onHand = append(onHand, upcomingEntry(u))
	}
	return memberNames{pref: pref, onHand: onHand}
}

// of is the shown name of a subscription: the first show on hand it is subscribed to (ShowSub.Matches,
// ADR-0002), called by the member's preferred name with the stored title as its title. A show not on hand is called
// by its stored title.
func (m memberNames) of(subscription *anime_subs.ShowSub) string {
	for _, e := range m.onHand {
		if subscription.Matches(e.Route, e.Name) {
			e.Name = subscription.Show
			return e.ShownName(m.pref)
		}
	}
	return subscription.Show
}

func subscriptionsDescriptionForPage(list []*anime_subs.ShowSub, names memberNames, start, page, totalPages, n int) string {
	var b strings.Builder
	end := min(start+discord.MaxEmbedFields, len(list))
	upcoming, now := animeschedule.UpcomingShows(), animeschedule.Now()
	for j := start; j < end; j++ {
		if list[j] == nil {
			continue
		}
		fmt.Fprintf(&b, "%d. %s%s\n", j+1, clipListTitle(names.of(list[j])), findNextEpisode(loadTimetable(subscriptionTracks(list[j])), upcoming, list[j].Route, list[j].Show, subscriptionTracks(list[j]), now).listStatus())
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
// the zero value and false. It looks at every show, donghua included: whether a donghua may be subscribed is subCore's
// decision, made after the name has resolved.
func findShowByName(name string) (animeschedule.ShowEntry, bool) {
	var shows []animeschedule.ShowEntry
	for _, c := range candidates() {
		shows = append(shows, c.ShowEntry)
	}
	return findShowIn(shows, name)
}

// suggestionName is how an add suggestion names a show under pref: "Shown name (Title)" when the shown name differs
// from the title under anime_subs.MatchKey, else the title. Title keeps "Title (English)" when the English name
// differs.
func suggestionName(e animeschedule.ShowEntry, pref animeschedule.PreferredName) string {
	if pref == animeschedule.PreferTitle {
		if en := strings.TrimSpace(e.English); en != "" && anime_subs.MatchKey(en) != anime_subs.MatchKey(e.Name) {
			return fmt.Sprintf("%s (%s)", e.Name, en)
		}
		return e.Name
	}
	if shown := e.ShownName(pref); anime_subs.MatchKey(shown) != anime_subs.MatchKey(e.Name) {
		return fmt.Sprintf("%s (%s)", shown, e.Name)
	}
	return e.Name
}

// subsStore is the narrow persistence surface of the subscription commands. It deliberately has no
// whole-document rewrite: every change is a single-statement operation.
type subscriptionStore interface {
	GetByID(ctx context.Context, id string) (*anime_subs.AnimeSubs, error)
	AddShowIfAbsent(ctx context.Context, id string, sub *anime_subs.ShowSub) (bool, error)
	RemoveShows(ctx context.Context, id string, showNames []string) error
	SetShowTracks(ctx context.Context, id, showName, tracks string, marks []anime_subs.Mark) error
	ClearShows(ctx context.Context, id string) error
	SetPreferredName(ctx context.Context, id, name string) error
}

// showLookup resolves user input to a loaded-week or upcoming show; replaced in tests.
var showLookup = findShowByName

const msgShowNotFound = "I could not find that airing or upcoming show. Pick one of the suggestions or check the spelling, and see `/schedule` for the timetable."

// subCore adds the subscription and answers. confirmed is true for a reply that confirms the subscription (new or
// already held), which is when the DMs footer applies.
func subCore(ctx context.Context, store subscriptionStore, uid, animeInput string, set animeschedule.SubscriptionTracks, hideDonghua bool) (responseDesc string, confirmed bool, logErr error) {
	animeInput = strings.TrimSpace(animeInput)
	if animeInput == "" {
		return "Please provide an anime name: pick one of the suggestions, or check `/schedule` for the shows on the timetable.", false, nil
	}
	// The name resolves against every show first, with the usual precedence, so a hidden donghua is never skipped for a
	// lower-precedence match among the shown shows.
	show, ok := showLookup(animeInput)
	if !ok {
		return msgShowNotFound, false, nil
	}
	showName := show.Name

	doc, err := store.GetByID(ctx, uid)
	if err != nil {
		return "Could not load your subscriptions.", false, err
	}
	// Replies call the show by the member's own preferred name; what is stored and matched stays the title.
	shown := show.ShownName(animeschedule.MemberPreferredName(doc))
	if hideDonghua && show.Donghua {
		// The server hides this donghua. A member who follows it is told how to change its tracks; one who does not is
		// told where to follow it. Nothing is changed.
		mention := commands.CommandMention("subscription", "add")
		if doc != nil && slices.ContainsFunc(doc.Shows, func(sub *anime_subs.ShowSub) bool { return sub != nil && sub.Matches(show.Route, showName) }) {
			return fmt.Sprintf("You already follow `%s`. This server hides donghua, so run %s in a DM with ZeroTsu to change its tracks.", shown, mention), false, nil
		}
		return msgShowNotFound + fmt.Sprintf(" This server hides donghua. Run %s in a DM with ZeroTsu to follow it.", mention), false, nil
	}
	if doc != nil {
		for _, sub := range doc.Shows {
			if sub != nil && sub.Matches(show.Route, showName) {
				return replaceTracks(ctx, store, uid, sub, shown, set, animeschedule.Now())
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
		return fmt.Sprintf("You are already subscribed to `%s`.", shown), true, nil
	}
	desc := fmt.Sprintf("Subscribed to **%s**.", shown)
	if set != animeschedule.SubscribeSub {
		desc = fmt.Sprintf("Subscribed to **%s** (%s).", shown, set.Title())
	}
	if line := findNextEpisode(loadTimetable(set), animeschedule.UpcomingShows(), show.Route, showName, set, now).confirmationLine(); line != "" {
		desc += "\n" + line
	}
	return desc, true, nil
}

// replaceTracks answers a re-add of a show the member already follows. The same set is already subscribed; a
// different set replaces the stored one in a single statement. Tracks kept from the old set keep their memory;
// each track new to the set is seeded from its own row that aired today (or cleared, if none), so switching
// never DMs something that aired earlier today. It never counts against the subscription limit. The replies call
// the show shown.
func replaceTracks(ctx context.Context, store subscriptionStore, uid string, existing *anime_subs.ShowSub, shown string, set animeschedule.SubscriptionTracks, now time.Time) (string, bool, error) {
	old, _ := animeschedule.ParseSubscriptionTracks(existing.Tracks)
	if old == set {
		return fmt.Sprintf("You are already subscribed to `%s`.", shown), true, nil
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
	return fmt.Sprintf("**%s** now alerts for %s.", shown, set.Title()), true, nil
}

// notSubscribed is the reply for removing a show the member is not subscribed to, pointing at the list.
func notSubscribed(input string) string {
	return fmt.Sprintf("You are not subscribed to `%s`. See %s for your subscriptions.", input, commands.CommandMention("subscription", "list"))
}

func unsubCore(ctx context.Context, store subscriptionStore, uid, animeInput string) (responseDesc string, logErr error) {
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

	// Resolve the stored titles (exact case) the input names, then remove exactly those. last is the subscription
	// that named the final one.
	key := anime_subs.MatchKey(animeInput)
	// The names on hand are read once, and only when the stored titles do not settle the input.
	var names *memberNames
	shownName := func(s *anime_subs.ShowSub) string {
		if names == nil {
			n := namesFor(animeschedule.MemberPreferredName(doc))
			names = &n
		}
		return names.of(s)
	}
	stored, last := subscriptionsNamed(doc.Shows, key, func(s *anime_subs.ShowSub) string { return s.Show })
	if len(stored) == 0 {
		// An autocomplete choice carries the title cut to Discord's limit, as the Remove picker does.
		stored, last = subscriptionsNamed(doc.Shows, key, func(s *anime_subs.ShowSub) string { return clip(s.Show) })
		if len(stored) > 1 {
			return ambiguousRemoval(stored), nil
		}
	}
	if len(stored) == 0 {
		// Typed text submitted without a suggestion names a subscription by the member's shown name.
		stored, last = subscriptionsNamed(doc.Shows, key, shownName)
		if len(stored) > 1 {
			return ambiguousRemoval(stored), nil
		}
	}
	if len(stored) == 0 {
		return notSubscribed(animeInput), nil
	}

	if err := store.RemoveShows(ctx, uid, stored); err != nil {
		return "Could not update subscriptions.", err
	}
	removed := animeInput
	if len(stored) == 1 {
		// One subscription removed: called by the member's shown name when that is not its stored title. Otherwise
		// the typed input, as under Title.
		if shown := shownName(last); shown != last.Show {
			removed = shown
		}
	}
	return fmt.Sprintf("Success! You have unsubscribed from `%s`.", removed), nil
}

// subscriptionsNamed is the stored titles of the subscriptions whose name, under name, has the match key key, and
// the last such subscription.
func subscriptionsNamed(subscriptions []*anime_subs.ShowSub, key string, name func(*anime_subs.ShowSub) string) (stored []string, last *anime_subs.ShowSub) {
	for _, s := range subscriptions {
		if s != nil && anime_subs.MatchKey(name(s)) == key {
			stored, last = append(stored, s.Show), s
		}
	}
	return stored, last
}

// ambiguousRemoval refuses a remove whose input names more than one subscription, listing their stored titles.
func ambiguousRemoval(stored []string) string {
	return fmt.Sprintf("That name matches more than one of your subscriptions, so none were removed: `%s`. Use the Remove picker from `/subscription list` to choose one.", strings.Join(stored, "`, `"))
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
