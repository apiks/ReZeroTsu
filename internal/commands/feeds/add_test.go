package feeds

import (
	"slices"
	"strings"
	"testing"
	"time"

	"ReZeroTsu/internal/database/guilds"
	"ReZeroTsu/internal/reddit"

	"github.com/bwmarrin/discordgo"
)

type opts = []*discordgo.ApplicationCommandInteractionDataOption

// setNow moves the Reddit cutoff clock for one test.
func setNow(t *testing.T, now time.Time) {
	t.Helper()
	old := reddit.Now
	reddit.Now = func() time.Time { return now }
	t.Cleanup(func() { reddit.Now = old })
}

const (
	beforeRefusal = "Reddit feeds can't be added any more. Reddit is shutting down the RSS feeds I read on 13 November 2026, and existing feeds stop that day."
	afterRefusal  = "Reddit feeds were retired on 13 November 2026, when Reddit shut down the RSS feeds I read."
)

func TestAddSortIsFixedChoicesNotAutocompleted(t *testing.T) {
	for _, o := range subcommand(t, "add").Options {
		if o.Name != "sort" {
			continue
		}
		var got []string
		for _, c := range o.Choices {
			got = append(got, c.Name+"="+c.Value.(string))
		}
		if o.Autocomplete || !slices.Equal(got, []string{"Hot=hot", "Rising=rising", "New=new"}) {
			t.Errorf("sort option = autocomplete %v, choices %v", o.Autocomplete, got)
		}
		return
	}
	t.Fatal("add has no sort option")
}

func TestAddDescriptionSaysItIsRetired(t *testing.T) {
	if got, want := subcommand(t, "add").Desc, "Retired: Reddit feeds can't be added any more."; got != want {
		t.Errorf("description = %q, want %q", got, want)
	}
}

func TestAddIsRefusedBeforeAnythingElseIsChecked(t *testing.T) {
	cases := []struct {
		name string
		now  time.Time
		want string
	}{
		{"before the cutoff", reddit.Cutoff.Add(-time.Hour), beforeRefusal},
		{"at the cutoff", reddit.Cutoff, afterRefusal},
		{"after the cutoff", reddit.Cutoff.Add(30 * 24 * time.Hour), afterRefusal},
	}
	calls := []struct {
		name string
		opts opts
	}{
		{"valid feed", opts{chanOpt("c1"), strOpt("subreddit", "anime")}},
		{"only a pin change", opts{chanOpt("c1"), strOpt("subreddit", "anime"), boolOpt("pin", true)}},
		{"no options", nil},
		{"bad subreddit", opts{chanOpt("c1"), strOpt("subreddit", "a b!")}},
		{"bad sort", opts{chanOpt("c1"), strOpt("subreddit", "anime"), strOpt("sort", "top")}},
	}
	for _, tc := range cases {
		for _, call := range calls {
			t.Run(tc.name+"/"+call.name, func(t *testing.T) {
				setNow(t, tc.now)
				st := useStore(t, guilds.FeedEntry{Subreddit: "anime", PostType: "hot", ChannelID: "c1"})
				fake := invoke(t, "add", call.opts...)
				if got := text(lastReply(t, fake)); !strings.Contains(got, tc.want) {
					t.Errorf("reply = %q, want %q", got, tc.want)
				}
				if len(st.feeds) != 1 || st.feeds[0].Pin {
					t.Errorf("feeds changed: %+v", st.feeds)
				}
				for _, c := range fake.Calls() {
					if c.Op != "Defer" && c.Op != "EditReply" {
						t.Errorf("unexpected interaction call %q (permissions or channel looked up?)", c.Op)
					}
				}
			})
		}
	}
}
