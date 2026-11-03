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

const optType = "type"

// typeChoices are the fixed values of the `type` option: a media-type route and its label. Donghua is
// AnimeSchedule's Chinese ONA.
var typeChoices = []*discordgo.ApplicationCommandOptionChoice{
	{Name: "TV", Value: "tv"},
	{Name: "TV Short", Value: "tv-short"},
	{Name: "ONA", Value: "ona"},
	{Name: "Donghua", Value: "ona-chinese"},
	{Name: "OVA", Value: "ova"},
	{Name: "Special", Value: "special"},
	{Name: "Movie", Value: "movie"},
}

// candidate is a show that can be suggested and looked up. Premiere is the "premieres 10 Jan" label of a show not
// yet airing and is empty for a timetable show.
type candidate struct {
	animeschedule.ShowEntry
	Premiere string
}

// candidateSources list where suggestions come from, in the order they are offered. Another source of shows
// (the upcoming list) is added here.
var candidateSources = []func() []candidate{timetableCandidates}

func timetableCandidates() []candidate {
	var out []candidate
	for _, e := range animeschedule.AiringShows() {
		out = append(out, candidate{ShowEntry: e})
	}
	return out
}

// candidates are all the sources' shows in offer order, narrowed to one media type when only is set.
func candidates(only *animeschedule.MediaType) []candidate {
	var out []candidate
	for _, src := range candidateSources {
		for _, c := range src() {
			if only == nil || c.MediaType == *only {
				out = append(out, c)
			}
		}
	}
	return out
}

// typeFilter reads the `type` option from the interaction's options, which is where autocomplete finds the
// other options' current values; nil when it is unset or unknown.
func typeFilter(opts []*discordgo.ApplicationCommandInteractionDataOption) *animeschedule.MediaType {
	for _, o := range opts {
		if o == nil {
			continue
		}
		if o.Type == discordgo.ApplicationCommandOptionSubCommand {
			return typeFilter(o.Options)
		}
		if o.Name == optType {
			if mt, ok := animeschedule.MediaTypeFromRoute(o.StringValue()); ok {
				return &mt
			}
		}
	}
	return nil
}

// follows reports whether sub is the subscription for c: by route, or by title when either side has no route.
func follows(sub *anime_subs.ShowSub, c candidate) bool {
	if sub.Route != "" && c.Route != "" {
		return sub.Route == c.Route
	}
	return anime_subs.MatchKey(sub.Show) == anime_subs.MatchKey(c.Name)
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

// autocompleteAdd offers the candidate shows matched anywhere in any of their four names, narrowed by the `type`
// option, with the invoker's followed shows labelled and last. A choice's value is the display title, which is
// what subscriptions are keyed by. The invoker's subscriptions are read once.
func autocompleteAdd(ctx context.Context, db *database.Client, _ *discordgo.Session, i *discordgo.InteractionCreate, _, typed string) []*discordgo.ApplicationCommandOptionChoice {
	matched := commands.MatchAnywhere(candidates(typeFilter(i.ApplicationCommandData().Options)), typed, func(c candidate) []string { return c.AllNames() })
	list, err := loadList(ctx, storeOf(db), commands.UserID(i))
	if err != nil {
		logger.For("animesubs").Error("subscription autocomplete failed", "err", err)
	}
	var rest, followed []*discordgo.ApplicationCommandOptionChoice
	for _, c := range matched {
		isFollowed := slices.ContainsFunc(list, func(s *anime_subs.ShowSub) bool { return follows(s, c) })
		choice := &discordgo.ApplicationCommandOptionChoice{Name: labelledName(c, isFollowed), Value: clip(c.Name)}
		if isFollowed {
			followed = append(followed, choice)
		} else {
			rest = append(rest, choice)
		}
	}
	return append(rest, followed...)
}
