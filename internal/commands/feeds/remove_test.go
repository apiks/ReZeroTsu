package feeds

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"ReZeroTsu/internal/database/guilds"
	"ReZeroTsu/internal/interaction"
)

func TestRemoveHasExactlyOneRequiredAutocompletedFeedOption(t *testing.T) {
	opts := subcommand(t, "remove").Options
	if len(opts) != 1 || opts[0].Name != "feed" || !opts[0].Required || !opts[0].Autocomplete {
		t.Errorf("remove options = %+v", opts)
	}
}

func TestFeedSuggestionsDescribeEachFeedAndCarryItsShortCode(t *testing.T) {
	plain := guilds.FeedEntry{Subreddit: "anime", PostType: "hot", ChannelID: "c1"}
	filtered := guilds.FeedEntry{Subreddit: "anime", PostType: "new", ChannelID: "c2", Author: "bob", Title: "Episode"}
	st := useStore(t, plain, filtered)
	names := map[string]string{"c1": "general"}
	got := feedChoices(t.Context(), st, func(id string) string { return names[id] }, "g", "")
	if len(got) != 2 {
		t.Fatalf("choices = %+v", got)
	}
	want := map[string]string{
		shortCode(plain):    "r/anime (hot) in #general",
		shortCode(filtered): `r/anime (new) in #c2 · u/bob · "Episode"`,
	}
	for _, c := range got {
		if want[c.Value.(string)] != c.Name {
			t.Errorf("choice %q = %q, want %q", c.Value, c.Name, want[c.Value.(string)])
		}
	}
}

func TestFeedSuggestionsMatchAnywhereAndAreCutTo100(t *testing.T) {
	long := guilds.FeedEntry{Subreddit: "anime", PostType: "hot", ChannelID: "c1", Title: strings.Repeat("t", 150)}
	other := guilds.FeedEntry{Subreddit: "manga", PostType: "hot", ChannelID: "c2", Author: "amy"}
	st := useStore(t, long, other)
	nameOf := func(id string) string { return map[string]string{"c1": "general"}[id] }
	for typed, sub := range map[string]string{"gener": "anime", "AMY": "manga", "nim": "anime"} {
		got := feedChoices(t.Context(), st, nameOf, "g", typed)
		if len(got) != 1 || !strings.Contains(got[0].Name, sub) {
			t.Errorf("typed %q: %+v", typed, got)
		}
	}
	for _, c := range feedChoices(t.Context(), st, nameOf, "g", "") {
		if len([]rune(c.Name)) > 100 {
			t.Errorf("choice is %d characters", len([]rune(c.Name)))
		}
	}
	if got := feedChoices(t.Context(), st, nameOf, "", "a"); got != nil {
		t.Errorf("no guild: %+v", got)
	}
}

func TestRemoveByShortCodeRemovesExactlyThatFeedKeepingFilteredSiblings(t *testing.T) {
	plain := guilds.FeedEntry{Subreddit: "anime", PostType: "hot", ChannelID: "c1"}
	sibling := guilds.FeedEntry{Subreddit: "anime", PostType: "hot", ChannelID: "c1", Author: "bob"}
	st := useStore(t, plain, sibling)
	fake := invoke(t, "remove", strOpt("feed", shortCode(plain)))
	if !slices.Equal(st.feeds, []guilds.FeedEntry{sibling}) {
		t.Errorf("feeds = %+v", st.feeds)
	}
	if len(st.checksGone) != 0 {
		t.Errorf("records forgotten %v although a sibling shares them", st.checksGone)
	}
	if got := text(lastReply(t, fake)); !strings.Contains(got, "r/anime") || !strings.Contains(got, "<#c1>") {
		t.Errorf("reply = %q", got)
	}
	invoke(t, "remove", strOpt("feed", shortCode(sibling)))
	if len(st.feeds) != 0 || !slices.Equal(st.checksGone, []string{"c1|anime|hot|bob|"}) {
		t.Errorf("feeds %+v, records forgotten %v", st.feeds, st.checksGone)
	}
}

func TestRemovingAFilteredFeedKeepsTheDeliveryRecordsOfItsUnfilteredSibling(t *testing.T) {
	plain := guilds.FeedEntry{Subreddit: "anime", PostType: "hot", ChannelID: "c1"}
	filtered := guilds.FeedEntry{Subreddit: "anime", PostType: "hot", ChannelID: "c1", Author: "bob"}
	other := guilds.FeedEntry{Subreddit: "anime", PostType: "hot", ChannelID: "c1", Author: "amy"}
	st := useStore(t, plain, filtered, other)
	invoke(t, "remove", strOpt("feed", shortCode(filtered)))
	if !slices.Equal(st.feeds, []guilds.FeedEntry{plain, other}) {
		t.Errorf("feeds = %+v", st.feeds)
	}
	if len(st.checksGone) != 0 {
		t.Errorf("records forgotten %v although the unfiltered feed relies on them", st.checksGone)
	}
	// Two differently filtered feeds share nothing, so the last one with no unfiltered sibling forgets its records.
	st.feeds = []guilds.FeedEntry{filtered, other}
	invoke(t, "remove", strOpt("feed", shortCode(filtered)))
	if !slices.Equal(st.checksGone, []string{"c1|anime|hot|bob|"}) {
		t.Errorf("records forgotten %v", st.checksGone)
	}
}

func TestSeveralFeedsRefusalStaysUnderDiscordsLimitForLongTitleFilters(t *testing.T) {
	var feeds []guilds.FeedEntry
	for _, c := range []string{"c1", "c2", "c3", "c4", "c5", "c6"} {
		feeds = append(feeds, guilds.FeedEntry{Subreddit: "anime", PostType: "hot", ChannelID: c, Title: strings.Repeat("t", 450)})
	}
	useStore(t, feeds...)
	got := text(lastReply(t, invoke(t, "remove", strOpt("feed", "anime"))))
	if n := len([]rune(got)); n > 2000 {
		t.Errorf("reply is %d characters, Discord rejects over 2000", n)
	}
	if !strings.Contains(got, "and 1 more") || strings.Count(got, "r/anime (hot)") != 5 {
		t.Errorf("reply lost its list: %q", got)
	}
}

func TestRemoveByTypedSubredditWithOneFeedRemovesIt(t *testing.T) {
	st := useStore(t)
	for _, typed := range []string{"r/Anime", "anime"} {
		st.feeds = []guilds.FeedEntry{{Subreddit: "anime", PostType: "hot", ChannelID: "c1"}, {Subreddit: "manga", PostType: "hot", ChannelID: "c1"}}
		invoke(t, "remove", strOpt("feed", typed))
		if len(st.feeds) != 1 || st.feeds[0].Subreddit != "manga" {
			t.Errorf("typed %q: feeds = %+v", typed, st.feeds)
		}
	}
}

func TestRemoveByTypedSubredditWithSeveralFeedsListsUpToFiveAndRemovesNothing(t *testing.T) {
	var feeds []guilds.FeedEntry
	for _, c := range []string{"c1", "c2", "c3", "c4", "c5", "c6", "c7"} {
		feeds = append(feeds, guilds.FeedEntry{Subreddit: "anime", PostType: "hot", ChannelID: c})
	}
	st := useStore(t, feeds...)
	got := text(lastReply(t, invoke(t, "remove", strOpt("feed", "anime"))))
	if len(st.feeds) != 7 || !strings.Contains(got, "and 2 more") || !strings.Contains(got, "suggestion") {
		t.Errorf("feeds %d, reply %q", len(st.feeds), got)
	}
	if strings.Count(got, "r/anime (hot)") != 5 {
		t.Errorf("want five listed feeds: %q", got)
	}
	st.feeds = slices.Clone(feeds[:2])
	if got := text(lastReply(t, invoke(t, "remove", strOpt("feed", "anime")))); strings.Contains(got, "more") || len(st.feeds) != 2 {
		t.Errorf("two feeds: %q", got)
	}
}

func TestRemoveByTypedSubredditWithNoFeedPointsToList(t *testing.T) {
	st := useStore(t, guilds.FeedEntry{Subreddit: "anime", PostType: "hot", ChannelID: "c1"})
	if got := text(lastReply(t, invoke(t, "remove", strOpt("feed", "r/Manga")))); !strings.Contains(got, "No feed for r/manga. See /reddit list.") {
		t.Errorf("reply = %q", got)
	}
	if len(st.feeds) != 1 {
		t.Errorf("feeds = %+v", st.feeds)
	}
}

func TestRemoveRepliesForNoFeedsAndBlankInput(t *testing.T) {
	st := useStore(t)
	if got := text(lastReply(t, invoke(t, "remove", strOpt("feed", "anime")))); !strings.Contains(got, "no Reddit feeds") {
		t.Errorf("empty: %q", got)
	}
	st.feeds = []guilds.FeedEntry{{Subreddit: "anime", PostType: "hot", ChannelID: "c1"}}
	if got := text(lastReply(t, invoke(t, "remove", strOpt("feed", "r/")))); !strings.Contains(got, "valid subreddit") {
		t.Errorf("blank: %q", got)
	}
}

func TestRemoveStoreFailuresAreSomethingWentWrong(t *testing.T) {
	st := useStore(t, guilds.FeedEntry{Subreddit: "anime", PostType: "hot", ChannelID: "c1"})
	st.failRemove = errors.New("db down")
	if r := lastReply(t, invoke(t, "remove", strOpt("feed", "anime"))); r.Content != interaction.SomethingWentWrong {
		t.Errorf("remove failure reply = %+v", r)
	}
	st.failGet = errors.New("db down")
	if r := lastReply(t, invoke(t, "remove", strOpt("feed", "anime"))); r.Content != interaction.SomethingWentWrong {
		t.Errorf("load failure reply = %+v", r)
	}
}
