package animesubs

import (
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
)

// The shape of /subscription add: a required, autocompleted anime and an optional track with its five fixed choices.
func TestAddRegistersWithAnimeAndTrackOnly(t *testing.T) {
	opts := subcommand(t, "add").Options
	var got []string
	for _, o := range opts {
		got = append(got, o.Name)
	}
	if strings.Join(got, ",") != "anime,track" {
		t.Fatalf("/subscription add options = %v, want anime,track", got)
	}
	anime, track := opts[0], opts[1]
	if !anime.Required || !anime.Autocomplete {
		t.Errorf("anime: required=%v autocomplete=%v, want both true", anime.Required, anime.Autocomplete)
	}
	if track.Required || track.Autocomplete {
		t.Errorf("track: required=%v autocomplete=%v, want both false", track.Required, track.Autocomplete)
	}
	var choices []string
	for _, c := range track.Choices {
		choices = append(choices, c.Name+"="+c.Value.(string))
	}
	want := "Sub=sub|Dub=dub|Raw=raw|Sub and Dub=sub_dub|Every track=every"
	if strings.Join(choices, "|") != want {
		t.Errorf("track choices = %v, want %s", choices, want)
	}
}

// A client that has not re-registered may still send the removed type option; it must not narrow the lookup.
func TestAddIgnoresAStaleTypeOptionAndItsNotFoundReplyNamesNoFilter(t *testing.T) {
	seedTypes(t)
	st := newFakeStore()
	useStore(t, st)
	stale := &discordgo.ApplicationCommandInteractionDataOption{Name: "type", Type: discordgo.ApplicationCommandOptionString, Value: "movie"}
	got := text(reply(t, invoke(t, "add", append(animeOption("Plain Tv"), stale)...)))
	if !strings.Contains(got, "Subscribed to **Plain Tv**") {
		t.Errorf("a TV show with a stale movie type: reply %q", got)
	}
	missing := text(reply(t, invoke(t, "add", append(animeOption("Nothing Like It"), stale)...)))
	if !strings.Contains(missing, "could not find") || strings.Contains(missing, "filter") {
		t.Errorf("not-found reply %q must not mention a filter", missing)
	}
}
