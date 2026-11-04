package animesubs

import (
	"context"
	"slices"
	"strings"

	"ReZeroTsu/internal/animeschedule"
	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/database/anime_subs"
	"ReZeroTsu/internal/logger"

	"github.com/bwmarrin/discordgo"
)

const optTrack = "track"

// trackOptionChoices are the fixed values of the `track` option: each subscription tracks set and its stored form.
var trackOptionChoices = func() []*discordgo.ApplicationCommandOptionChoice {
	var out []*discordgo.ApplicationCommandOptionChoice
	for _, set := range []animeschedule.SubscriptionTracks{
		animeschedule.SubscribeSub, animeschedule.SubscribeDub, animeschedule.SubscribeRaw,
		animeschedule.SubscribeSubDub, animeschedule.SubscribeEvery,
	} {
		out = append(out, &discordgo.ApplicationCommandOptionChoice{Name: set.Title(), Value: set.String()})
	}
	return out
}()

// candidate is a show that can be suggested and looked up. Premiere is the "premieres 10 Jan" label of a show not
// yet airing and is empty for a timetable show.
type candidate struct {
	animeschedule.ShowEntry
	Premiere string
}

// candidateSources are where suggestions come from, in the order they are offered: timetable shows, then upcoming
// shows. The upcoming shows are labelled with the premiere of the chosen tracks, or raw's when none are chosen (nil).
func candidateSources(chosen *animeschedule.SubscriptionTracks) [][]candidate {
	return [][]candidate{timetableCandidates(), upcomingCandidates(chosen)}
}

// upcomingEntry is an upcoming show as a ShowEntry: its air time is the premiere, 0 while AnimeSchedule has no date.
func upcomingEntry(u animeschedule.UpcomingShow) animeschedule.ShowEntry {
	e := animeschedule.ShowEntry{Name: u.Name, Route: u.Route, Romaji: u.Romaji, English: u.English, Native: u.Native, MediaType: u.MediaType}
	if !u.Premiere.IsZero() {
		e.AirTimeUnix = u.Premiere.Unix()
	}
	return e
}

// premiereLabel is "premieres 10 Jan" (a UTC day, like the timetable) or "premiere date TBA". It names raw's
// premiere, or the earliest known premiere of the chosen tracks when chosen is set.
func premiereLabel(u animeschedule.UpcomingShow, chosen *animeschedule.SubscriptionTracks) string {
	premiere := u.Premiere
	if chosen != nil {
		premiere, _, _ = u.SetPremiere(*chosen)
	}
	if premiere.IsZero() {
		return "premiere date TBA"
	}
	return "premieres " + premiere.UTC().Format("2 Jan")
}

// upcomingCandidates are the upcoming shows that are not on the loaded timetable; a show on both is offered once, as
// a timetable show, matched by route when both sides have one and by title otherwise.
func upcomingCandidates(chosen *animeschedule.SubscriptionTracks) []candidate {
	onTimetable := animeschedule.AllEntries()
	var out []candidate
	for _, u := range animeschedule.UpcomingShows() {
		e := upcomingEntry(u)
		if slices.ContainsFunc(onTimetable, func(t animeschedule.ShowEntry) bool { return sameListedShow(t, e) }) {
			continue
		}
		out = append(out, candidate{ShowEntry: e, Premiere: premiereLabel(u, chosen)})
	}
	return out
}

// sameListedShow reports whether two list entries are one show, so a show on both lists is offered once: by route
// when both have one, else by title under anime_subs.MatchKey. This is not the subscription rule (ShowSub.Matches),
// which trusts a subscription's route alone.
func sameListedShow(a, b animeschedule.ShowEntry) bool {
	if a.Route != "" && b.Route != "" {
		return a.Route == b.Route
	}
	return anime_subs.MatchKey(a.Name) == anime_subs.MatchKey(b.Name)
}

// choiceValue identifies a suggested show: its route, or its title when no route is known, within Discord's limit.
func choiceValue(c candidate) string {
	if c.Route != "" {
		return clip(c.Route)
	}
	return clip(c.Name)
}

// timetableCandidates are the shows of the three loaded weeks that the daily cleanup keeps (animeschedule.LoadedShows).
func timetableCandidates() []candidate {
	var out []candidate
	for _, e := range animeschedule.LoadedShows() {
		out = append(out, candidate{ShowEntry: e})
	}
	return out
}

// candidates are all the sources' shows in offer order.
func candidates() []candidate {
	var out []candidate
	for _, source := range candidateSources(nil) {
		out = append(out, source...)
	}
	return out
}

// chosenTracks reads the `track` option from the interaction's options, which is where autocomplete finds the
// other options' current values; nil when it is unset or unknown.
func chosenTracks(opts []*discordgo.ApplicationCommandInteractionDataOption) *animeschedule.SubscriptionTracks {
	for _, o := range opts {
		if o == nil {
			continue
		}
		if o.Type == discordgo.ApplicationCommandOptionSubCommand {
			return chosenTracks(o.Options)
		}
		if o.Name == optTrack {
			if set, ok := animeschedule.ParseSubscriptionTracks(o.StringValue()); ok && o.StringValue() != "" {
				return &set
			}
		}
	}
	return nil
}

// labelledName is "Title (English)" then, in order, the premiere, the media type and "subscribed". The title is
// cut so the labels survive within Discord's limit.
func labelledName(c candidate, subscribed bool) string {
	var labels []string
	if c.Premiere != "" {
		labels = append(labels, c.Premiere)
	}
	if l := c.MediaType.Label(); l != "" {
		labels = append(labels, l)
	}
	if subscribed {
		labels = append(labels, "subscribed")
	}
	suffix := ""
	if len(labels) > 0 {
		suffix = " · " + strings.Join(labels, " · ")
	}
	title := []rune(titleName(c.ShowEntry))
	room := max(maxChoiceLength-len([]rune(suffix)), 0)
	return string(title[:min(room, len(title))]) + suffix
}

// autocompleteAdd offers the candidate shows matched anywhere in any of their four names, with the invoker's subscribed shows labelled and last. Each source ranks its own matches (prefix before
// substring), timetable shows come before upcoming shows, and the 25-choice cap applies to that final order. A
// choice's value is the show's route, or its title when it has none. The invoker's subscriptions are read once.
func autocompleteAdd(ctx context.Context, db *database.Client, _ *discordgo.Session, i *discordgo.InteractionCreate, _, typed string) []*discordgo.ApplicationCommandOptionChoice {
	chosen := chosenTracks(i.ApplicationCommandData().Options)
	list, err := loadList(ctx, storeOf(db), commands.UserID(i))
	if err != nil {
		logger.For("animesubs").Error("subscription autocomplete failed", "err", err)
	}
	var rest, subscribed []*discordgo.ApplicationCommandOptionChoice
	for _, source := range candidateSources(chosen) {
		for _, c := range commands.MatchAnywhereAll(source, typed, func(c candidate) []string { return c.AllNames() }) {
			isSubscribed := slices.ContainsFunc(list, func(s *anime_subs.ShowSub) bool { return s.Matches(c.Route, c.Name) })
			choice := &discordgo.ApplicationCommandOptionChoice{Name: labelledName(c, isSubscribed), Value: choiceValue(c)}
			if isSubscribed {
				subscribed = append(subscribed, choice)
			} else {
				rest = append(rest, choice)
			}
		}
	}
	return commands.Cap(append(rest, subscribed...))
}
