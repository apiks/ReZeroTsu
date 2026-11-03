package animesubs

import (
	"context"
	"strings"
	"testing"
	"time"

	"ReZeroTsu/internal/animeschedule"
	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/database/anime_subs"

	"github.com/bwmarrin/discordgo"
)

// countingStore counts how often the invoker's subscriptions are read.
type countingStore struct {
	*fakeStore
	reads int
}

func (c *countingStore) GetByID(ctx context.Context, id string) (*anime_subs.AnimeSubs, error) {
	c.reads++
	return c.fakeStore.GetByID(ctx, id)
}

// seedTypes loads one entry of every media type for the week of 2 Nov 2026.
func seedTypes(t *testing.T) {
	t.Helper()
	t.Cleanup(animeschedule.SetClock(func() time.Time { return time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC) }))
	t.Cleanup(animeschedule.Seed(map[string][]animeschedule.ShowEntry{
		"2026-11-04": {
			{Name: "Plain Tv", Route: "plain-tv", MediaType: animeschedule.MediaTV},
			{Name: "Short Tv", Route: "short-tv", MediaType: animeschedule.MediaTVShort},
			{Name: "Web One", Route: "web-one", MediaType: animeschedule.MediaONA},
			{Name: "Chinese One", Route: "chinese-one", MediaType: animeschedule.MediaDonghua},
			{Name: "Video One", Route: "video-one", MediaType: animeschedule.MediaOVA},
			{Name: "Extra One", Route: "extra-one", MediaType: animeschedule.MediaSpecial},
			{Name: "Film One", Route: "film-one", MediaType: animeschedule.MediaMovie},
		},
	}))
}

// suggestions asks /subscription add for suggestions as user, with an optional type option already chosen.
func suggestions(t *testing.T, st subsStore, user, typed, typeValue string) []*discordgo.ApplicationCommandOptionChoice {
	t.Helper()
	old := storeOf
	storeOf = func(*database.Client) subsStore { return st }
	t.Cleanup(func() { storeOf = old })
	sub := []*discordgo.ApplicationCommandInteractionDataOption{{Name: "anime", Type: discordgo.ApplicationCommandOptionString, Value: typed, Focused: true}}
	if typeValue != "" {
		sub = append(sub, &discordgo.ApplicationCommandInteractionDataOption{Name: "type", Type: discordgo.ApplicationCommandOptionString, Value: typeValue})
	}
	i := &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{Type: discordgo.InteractionApplicationCommandAutocomplete,
		User: &discordgo.User{ID: user},
		Data: discordgo.ApplicationCommandInteractionData{Name: "subscription", Options: []*discordgo.ApplicationCommandInteractionDataOption{
			{Name: "add", Type: discordgo.ApplicationCommandOptionSubCommand, Options: sub}}}}}
	return subcommand(t, "add").AutocompleteOption(t.Context(), nil, nil, i, "anime", typed)
}

func names(choices []*discordgo.ApplicationCommandOptionChoice) []string {
	var out []string
	for _, c := range choices {
		out = append(out, c.Name)
	}
	return out
}

func TestAddSuggestionsLabelEveryNonTVMediaType(t *testing.T) {
	seedTypes(t)
	got := strings.Join(names(suggestions(t, newFakeStore(), "user", "", "")), "|")
	want := "Plain Tv|Short Tv · TV Short|Web One · ONA|Chinese One · Donghua|Video One · OVA|Extra One · Special|Film One · Movie"
	if got != want {
		t.Errorf("names = %q\nwant    %q", got, want)
	}
}

func TestAddSuggestionsLabelFollowedShowsAndSortThemLastWithoutHidingThem(t *testing.T) {
	seedTypes(t)
	st := newFakeStore()
	st.docs["user"] = []*anime_subs.ShowSub{{Show: "Web One", Route: "web-one"}, {Show: "Plain Tv"}}
	got := strings.Join(names(suggestions(t, st, "user", "", "")), "|")
	want := "Short Tv · TV Short|Chinese One · Donghua|Video One · OVA|Extra One · Special|Film One · Movie|Plain Tv · subscribed|Web One · ONA · subscribed"
	if got != want {
		t.Errorf("names = %q\nwant    %q", got, want)
	}
}

func TestAddSuggestionsReadTheInvokersSubscriptionsOncePerRequest(t *testing.T) {
	seedTypes(t)
	st := &countingStore{fakeStore: newFakeStore()}
	suggestions(t, st, "user", "", "")
	if st.reads != 1 {
		t.Errorf("subscriptions read %d times, want once", st.reads)
	}
}

func TestAddSuggestionNameKeepsItsLabelsWithinOneHundredCharacters(t *testing.T) {
	seedTypes(t)
	t.Cleanup(animeschedule.Seed(map[string][]animeschedule.ShowEntry{"2026-11-04": {
		{Name: "Long", Route: "long", English: strings.Repeat("x", 150), MediaType: animeschedule.MediaMovie},
	}}))
	st := newFakeStore()
	st.docs["user"] = []*anime_subs.ShowSub{{Show: "Long", Route: "long"}}
	got := suggestions(t, st, "user", "", "")[0].Name
	if len([]rune(got)) > 100 || !strings.HasSuffix(got, " · Movie · subscribed") {
		t.Errorf("name %q (%d runes), want at most 100 ending in the labels", got, len([]rune(got)))
	}
}

func TestAddTypeFilterNarrowsSuggestionsAndDonghuaMeansChineseONA(t *testing.T) {
	seedTypes(t)
	cases := map[string]string{
		"movie":       "Film One · Movie",
		"tv":          "Plain Tv",
		"ona":         "Web One · ONA",
		"ona-chinese": "Chinese One · Donghua",
		"tv-short":    "Short Tv · TV Short",
		"ova":         "Video One · OVA",
		"special":     "Extra One · Special",
	}
	for value, want := range cases {
		if got := strings.Join(names(suggestions(t, newFakeStore(), "user", "", value)), "|"); got != want {
			t.Errorf("type %q: names = %q, want %q", value, got, want)
		}
	}
}

func TestAddTypeOptionOffersTheSevenFixedChoicesAndIsOptional(t *testing.T) {
	var opt *discordgo.ApplicationCommandOption
	for _, o := range subcommand(t, "add").Options {
		if o.Name == "type" {
			opt = o
		}
	}
	if opt == nil {
		t.Fatal("/subscription add has no type option")
	}
	var got []string
	for _, c := range opt.Choices {
		got = append(got, c.Name)
	}
	if opt.Required || opt.Autocomplete || strings.Join(got, ",") != "TV,TV Short,ONA,Donghua,OVA,Special,Movie" {
		t.Errorf("required %v, autocomplete %v, choices %v", opt.Required, opt.Autocomplete, got)
	}
}

func TestAddResolvesTypedTextOnlyWithinTheChosenType(t *testing.T) {
	seedTypes(t)
	typeOpt := func(v string) *discordgo.ApplicationCommandInteractionDataOption {
		return &discordgo.ApplicationCommandInteractionDataOption{Name: "type", Type: discordgo.ApplicationCommandOptionString, Value: v}
	}
	st := newFakeStore()
	useStore(t, st)
	got := text(reply(t, invoke(t, "add", append(animeOption("Plain Tv"), typeOpt("movie"))...)))
	if !strings.Contains(got, "could not find") || len(st.docs["user"]) != 0 {
		t.Errorf("a TV show under the movie filter: reply %q, subscriptions %v", got, st.names("user"))
	}
	st = newFakeStore()
	useStore(t, st)
	got = text(reply(t, invoke(t, "add", append(animeOption("Film One"), typeOpt("movie"))...)))
	if !strings.Contains(got, "Subscribed to **Film One**") || strings.Join(st.names("user"), ",") != "Film One" {
		t.Errorf("a movie under the movie filter: reply %q, subscriptions %v", got, st.names("user"))
	}
}

func TestPickingAFollowedSuggestionStillAnswersAlreadySubscribed(t *testing.T) {
	seedTypes(t)
	st := newFakeStore()
	st.docs["user"] = []*anime_subs.ShowSub{{Show: "Film One", Route: "film-one"}}
	choice := suggestions(t, st, "user", "film", "")[0]
	if !strings.HasSuffix(choice.Name, "· subscribed") {
		t.Fatalf("choice %q is not labelled", choice.Name)
	}
	got := text(reply(t, invoke(t, "add", animeOption(choice.Value.(string))...)))
	if !strings.Contains(got, "already subscribed") {
		t.Errorf("reply %q", got)
	}
}
