package animesubs

import (
	"context"
	"fmt"
	"strings"

	"ReZeroTsu/internal/animeschedule"
	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/database/anime_subs"
	"ReZeroTsu/internal/discord"

	"github.com/bwmarrin/discordgo"
)

const (
	embedTitleAnimeSubs  = "Anime subscriptions"
	maxUserSubscriptions = 100
)

func subsDescriptionForPage(list []*anime_subs.ShowSub, start, page, totalPages, n int) string {
	var b strings.Builder
	end := min(start+discord.MaxEmbedFields, len(list))
	for j := start; j < end; j++ {
		if list[j] == nil {
			continue
		}
		fmt.Fprintf(&b, "%d. %s\n", j+1, list[j].Show)
	}
	endShow := min(start+discord.MaxEmbedFields, n)
	b.WriteString("\n")
	if totalPages > 1 {
		fmt.Fprintf(&b, "Page %d of %d. Showing %d–%d of %d.\n", page+1, totalPages, start+1, endShow, n)
	}
	s := b.String()
	if len(s) > discord.MaxEmbedDescriptionLength {
		return s[:discord.MaxEmbedDescriptionLength]
	}
	return s
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

// findShowIn returns the show whose display title matches input under anime_subs.MatchKey (case-insensitive and
// trimmed like the notifier matches names). Failing that, the first show whose romaji, English or native name
// matches; a title cut to Discord's choice limit also matches. It returns the zero value and false if none.
func findShowIn(shows []animeschedule.ShowEntry, input string) (animeschedule.ShowEntry, bool) {
	key := anime_subs.MatchKey(input)
	if key == "" {
		return animeschedule.ShowEntry{}, false
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

// findShowByName returns the airing ShowEntry matching name by any of its four names, or zero value and false.
func findShowByName(name string) (animeschedule.ShowEntry, bool) {
	return findShowIn(animeschedule.AiringShows(), name)
}

// choiceName is the autocomplete label: "Title (English)" when the English name differs from the title, else the
// title, cut to Discord's limit.
func choiceName(e animeschedule.ShowEntry) string {
	name := e.Name
	if en := strings.TrimSpace(e.English); en != "" && anime_subs.MatchKey(en) != anime_subs.MatchKey(e.Name) {
		name = fmt.Sprintf("%s (%s)", e.Name, en)
	}
	return clip(name)
}

// autocompleteAdd offers this week's airing shows matched anywhere in any of their four names. A choice's value is
// the display title, which is what subscriptions are keyed by.
func autocompleteAdd(_ context.Context, _ *database.Client, _ *discordgo.Session, _ *discordgo.InteractionCreate, _, typed string) []*discordgo.ApplicationCommandOptionChoice {
	matched := commands.MatchAnywhere(animeschedule.AiringShows(), typed, animeschedule.ShowEntry.AllNames)
	out := make([]*discordgo.ApplicationCommandOptionChoice, 0, len(matched))
	for _, e := range matched {
		out = append(out, &discordgo.ApplicationCommandOptionChoice{Name: choiceName(e), Value: clip(e.Name)})
	}
	return out
}

// subsStore is the narrow persistence surface of the subscription commands. It deliberately has no
// whole-document rewrite: every change is a single-statement operation.
type subsStore interface {
	GetByID(ctx context.Context, id string) (*anime_subs.AnimeSubs, error)
	AddShowIfAbsent(ctx context.Context, id string, sub *anime_subs.ShowSub) (bool, error)
	RemoveShows(ctx context.Context, id string, showNames []string) error
	ClearShows(ctx context.Context, id string) error
}

// showLookup resolves user input to an airing show; replaced in tests.
var showLookup = findShowByName

func subCore(ctx context.Context, store subsStore, uid, animeInput string) (responseDesc string, logErr error) {
	animeInput = strings.TrimSpace(animeInput)
	if animeInput == "" {
		return "Please provide an anime name: pick one of the suggestions, or check `/schedule` for this week's shows.", nil
	}
	show, ok := showLookup(animeInput)
	if !ok {
		return "I could not find that airing show. Pick one of the suggestions or check the spelling, and see `/schedule` for this week's shows.", nil
	}
	showName := show.Name

	doc, err := store.GetByID(ctx, uid)
	if err != nil {
		return "Could not load your subscriptions.", err
	}
	if doc != nil {
		for _, sub := range doc.Shows {
			if sub != nil && anime_subs.MatchKey(sub.Show) == anime_subs.MatchKey(showName) {
				return fmt.Sprintf("You are already subscribed to `%s`.", showName), nil
			}
		}
	}
	if doc != nil && len(doc.Shows) >= maxUserSubscriptions {
		return fmt.Sprintf("You've reached the maximum number of subscriptions (%d). Unsubscribe from some shows to add more.", maxUserSubscriptions), nil
	}

	now := animeschedule.Now()
	airedToday, hasAiredToday := airedTodayEntry(animeschedule.GetDayShows(now, true), showName, now.Unix())

	newSub := &anime_subs.ShowSub{Show: showName, Notified: hasAiredToday, Guild: false}
	if hasAiredToday {
		newSub.LastNotifiedAirUnix = airedToday.AirTimeUnix
	}
	added, err := store.AddShowIfAbsent(ctx, uid, newSub)
	if err != nil {
		return "Could not save subscription.", err
	}
	if !added {
		return fmt.Sprintf("You are already subscribed to `%s`.", showName), nil
	}
	return fmt.Sprintf("Success! You have subscribed to DM notifications for `%s`.", showName), nil
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
		return fmt.Sprintf("You are not subscribed to `%s`.", animeInput), nil
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
		return fmt.Sprintf("You are not subscribed to `%s`.", animeInput), nil
	}

	if err := store.RemoveShows(ctx, uid, stored); err != nil {
		return "Could not update subscriptions.", err
	}
	return fmt.Sprintf("Success! You have unsubscribed from `%s`.", animeInput), nil
}

func clearsubsCore(ctx context.Context, store subsStore, uid string) (responseDesc string, logErr error) {
	doc, err := store.GetByID(ctx, uid)
	if err != nil {
		return "Could not load your subscriptions.", err
	}
	if doc == nil || len(doc.Shows) == 0 {
		return "You have no active anime subscriptions.", nil
	}
	if err := store.ClearShows(ctx, uid); err != nil {
		return "Could not clear subscriptions.", err
	}
	return "Success! All your anime subscriptions have been cleared.", nil
}

// airedTodayEntry returns today's entry for the named show if it has already aired at nowUnix. Names are matched
// with anime_subs.MatchKey like the notifier does, and the entry is today's own, so the seeded air time is today's
// even for a show the schedule lists on several weekdays.
func airedTodayEntry(todayShows []animeschedule.ShowEntry, showName string, nowUnix int64) (animeschedule.ShowEntry, bool) {
	key := anime_subs.MatchKey(showName)
	for _, e := range todayShows {
		if anime_subs.MatchKey(e.Name) == key && e.AirTimeUnix <= nowUnix {
			return e, true
		}
	}
	return animeschedule.ShowEntry{}, false
}
