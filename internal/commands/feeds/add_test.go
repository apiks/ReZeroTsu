package feeds

import (
	"context"
	"slices"
	"strings"
	"testing"

	"ReZeroTsu/internal/database/guilds"

	"github.com/bwmarrin/discordgo"
)

type opts = []*discordgo.ApplicationCommandInteractionDataOption

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

func TestAddIdenticalFeedSavesNothingAndSaysSo(t *testing.T) {
	st := useStore(t, guilds.FeedEntry{Subreddit: "anime", PostType: "hot", ChannelID: "c1", Pin: true})
	checkFeed = func(context.Context, string, string) error { t.Error("Reddit was asked"); return nil }
	fake := invoke(t, "add", chanOpt("c1"), strOpt("subreddit", "anime"), boolOpt("pin", true))
	if got := text(lastReply(t, fake)); !strings.Contains(got, "That feed is already set up in <#c1>.") {
		t.Errorf("reply = %q", got)
	}
	if st.addCalls != 0 || st.pinCalls != 0 {
		t.Errorf("addCalls=%d pinCalls=%d", st.addCalls, st.pinCalls)
	}
}

func TestAddWithOnlyADifferentPinUpdatesThePinSetting(t *testing.T) {
	cases := []struct {
		name     string
		had, pin bool
		want     string
	}{
		{"turn on", false, true, "Updated r/anime in <#c1>: now pinning the latest post."},
		{"turn off", true, false, "Updated r/anime in <#c1>: no longer pinning the latest post."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := useStore(t, guilds.FeedEntry{Subreddit: "anime", PostType: "hot", ChannelID: "c1", Author: "bob", Pin: tc.had})
			checkFeed = func(context.Context, string, string) error { t.Error("Reddit was asked"); return nil }
			fake := invoke(t, "add", chanOpt("c1"), strOpt("subreddit", "anime"), strOpt("author", "bob"), boolOpt("pin", tc.pin))
			if got := text(lastReply(t, fake)); !strings.Contains(got, tc.want) {
				t.Errorf("reply = %q, want %q", got, tc.want)
			}
			if st.addCalls != 0 || st.pinCalls != 1 || len(st.feeds) != 1 || st.feeds[0].Pin != tc.pin {
				t.Errorf("addCalls=%d pinCalls=%d feeds=%+v", st.addCalls, st.pinCalls, st.feeds)
			}
		})
	}
}

func TestAddSameSubredditWithDifferentIdentityIsANewFeed(t *testing.T) {
	st := useStore(t, guilds.FeedEntry{Subreddit: "anime", PostType: "hot", ChannelID: "c1"})
	invoke(t, "add", chanOpt("c1"), strOpt("subreddit", "anime"), strOpt("author", "bob"))
	if len(st.feeds) != 2 || st.addCalls != 1 {
		t.Errorf("feeds = %+v", st.feeds)
	}
}

func TestAddEchoesTheWholeFeed(t *testing.T) {
	cases := []struct {
		name string
		opts opts
		want string
	}{
		{"everything", opts{strOpt("sort", "new"), strOpt("author", "u/Bob"), strOpt("title", "Episode"), boolOpt("pin", true)},
			`Added r/anime (new) to <#c1>: only posts by u/bob whose title starts with "episode", pinning the latest. New posts start appearing within a few minutes.`},
		{"nothing", nil, "Added r/anime (hot) to <#c1>. New posts start appearing within a few minutes."},
		{"author only", opts{strOpt("author", "bob")}, "Added r/anime (hot) to <#c1>: only posts by u/bob. New posts"},
		{"title only", opts{strOpt("title", "ep")}, `Added r/anime (hot) to <#c1>: only posts whose title starts with "ep". New posts`},
		{"pin only", opts{boolOpt("pin", true)}, "Added r/anime (hot) to <#c1>: pinning the latest. New posts"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			useStore(t)
			o := append(opts{chanOpt("c1"), strOpt("subreddit", "anime")}, tc.opts...)
			if got := text(lastReply(t, invoke(t, "add", o...))); !strings.Contains(got, tc.want) {
				t.Errorf("reply = %q, want %q", got, tc.want)
			}
		})
	}
}
