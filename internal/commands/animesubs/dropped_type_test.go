package animesubs

import (
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestAddRegistersWithAnimeAndTrackOnly(t *testing.T) {
	var got []string
	for _, o := range subcommand(t, "add").Options {
		got = append(got, o.Name)
	}
	if strings.Join(got, ",") != "anime,track" {
		t.Errorf("/subscription add options = %v, want anime,track", got)
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
