package animesubs

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestAddNamesAnEpisodeSpanAsTheNextEpisode(t *testing.T) {
	seedEntries(t, entry("Frieren", "frieren", "Ep 1-12F", at(2*24*time.Hour)))
	got := addReply(t, "Frieren").Embeds[0].Description
	want := "Subscribed to **Frieren**.\nNext episode: Ep 1-12F, " + fullAndRelative(at(2*24*time.Hour)) + "."
	if got != want {
		t.Errorf("description = %q, want %q", got, want)
	}
}

func TestListNamesAnEpisodeSpan(t *testing.T) {
	seedEntries(t, entry("Frieren", "frieren", "Ep 1-12", at(2*24*time.Hour)))
	st := newFakeStore().with("user", "Frieren")
	useStore(t, st)
	got := reply(t, invoke(t, "list")).Embeds[0].Description
	if want := fmt.Sprintf("1. Frieren · Ep 1-12 <t:%d:R>\n", at(2*24*time.Hour)); !strings.Contains(got, want) {
		t.Errorf("list %q lacks line %q", got, want)
	}
}
