package feeds

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/database/guilds"
	"ReZeroTsu/internal/interaction"
	"ReZeroTsu/internal/interaction/interactiontest"
	"ReZeroTsu/internal/reddit"

	"github.com/bwmarrin/discordgo"
)

// fakeStore is an in-memory feed list. RemoveFeed matches the way the real one does: an empty author or title
// does not narrow the match.
type fakeStore struct {
	feeds      []guilds.FeedEntry
	checksGone []string
	failGet    error
	failAdd    error
	failRemove error
	addCalls   int
	pinCalls   int
}

func (f *fakeStore) RemoveExactFeed(_ context.Context, _ string, feed guilds.FeedEntry) (int64, error) {
	if f.failRemove != nil {
		return 0, f.failRemove
	}
	before := len(f.feeds)
	f.feeds = slices.DeleteFunc(f.feeds, func(g guilds.FeedEntry) bool { return g == feed })
	return int64(before - len(f.feeds)), nil
}

func (f *fakeStore) GetFeeds(context.Context, string) ([]guilds.FeedEntry, error) {
	if f.failGet != nil {
		return nil, f.failGet
	}
	return slices.Clone(f.feeds), nil
}

func (f *fakeStore) AddFeed(_ context.Context, _ string, feed guilds.FeedEntry) error {
	f.addCalls++
	if f.failAdd != nil {
		return f.failAdd
	}
	if !slices.Contains(f.feeds, feed) {
		f.feeds = append(f.feeds, feed)
	}
	return nil
}

func (f *fakeStore) SetFeedPin(_ context.Context, _ string, feed guilds.FeedEntry, pin bool) error {
	f.pinCalls++
	for i, g := range f.feeds {
		if sameIdentity(g, feed) {
			f.feeds[i].Pin = pin
		}
	}
	return nil
}

func (f *fakeStore) RemoveFeed(_ context.Context, _, channelID, subreddit, postType, author, title string) (int64, error) {
	if f.failRemove != nil {
		return 0, f.failRemove
	}
	before := len(f.feeds)
	f.feeds = slices.DeleteFunc(f.feeds, func(g guilds.FeedEntry) bool {
		return sameFeedScope(guilds.FeedEntry{ChannelID: channelID, Subreddit: subreddit, PostType: postType, Author: author, Title: title}, g)
	})
	return int64(before - len(f.feeds)), nil
}

func (f *fakeStore) DeleteChecks(_ context.Context, _, channelID, subreddit, postType, author, title string) error {
	f.checksGone = append(f.checksGone, strings.Join([]string{channelID, subreddit, postType, author, title}, "|"))
	return nil
}

// useStore installs the fake store and an availability check that records its calls and returns checkErr.
func useStore(t *testing.T, feeds ...guilds.FeedEntry) *fakeStore {
	t.Helper()
	st := &fakeStore{feeds: feeds}
	oldStore, oldCheck := storeOf, checkFeed
	storeOf = func(*database.Client) store { return st }
	checkFeed = func(context.Context, string, string) error { return nil }
	t.Cleanup(func() { storeOf, checkFeed = oldStore, oldCheck })
	return st
}

func redditCommand(t *testing.T) *commands.Command {
	t.Helper()
	for _, c := range commands.AllCommands() {
		if c.Name == "reddit" {
			return c
		}
	}
	t.Fatal("/reddit is not registered")
	return nil
}

func subcommand(t *testing.T, name string) *commands.Subcommand {
	t.Helper()
	for _, s := range redditCommand(t).Subcommands {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("/reddit has no %q subcommand", name)
	return nil
}

func strOpt(name, v string) *discordgo.ApplicationCommandInteractionDataOption {
	return &discordgo.ApplicationCommandInteractionDataOption{Name: name, Type: discordgo.ApplicationCommandOptionString, Value: v}
}

func chanOpt(id string) *discordgo.ApplicationCommandInteractionDataOption {
	return &discordgo.ApplicationCommandInteractionDataOption{Name: optChannel, Type: discordgo.ApplicationCommandOptionChannel, Value: id}
}

func boolOpt(name string, v bool) *discordgo.ApplicationCommandInteractionDataOption {
	return &discordgo.ApplicationCommandInteractionDataOption{Name: name, Type: discordgo.ApplicationCommandOptionBoolean, Value: v}
}

// invoke runs a /reddit subcommand the way dispatch does for a private deferred command.
func invoke(t *testing.T, name string, opts ...*discordgo.ApplicationCommandInteractionDataOption) *interactiontest.Fake {
	t.Helper()
	fake := &interactiontest.Fake{}
	cmd := redditCommand(t)
	ic := interactiontest.NewCommandContext(fake, cmd.Visibility, cmd.Response, discordgo.ApplicationCommandInteractionData{Name: "reddit", Options: opts})
	if err := ic.Defer(0); err != nil {
		t.Fatal(err)
	}
	if err := subcommand(t, name).Run(ic); err != nil {
		t.Fatal(err)
	}
	return fake
}

// lastReply is the text the moderator ends up seeing: the placeholder's replacement or a private follow-up.
func lastReply(t *testing.T, fake *interactiontest.Fake) interaction.Response {
	t.Helper()
	calls := fake.Calls()
	if calls[0].Op != "Defer" || calls[0].Visibility != interaction.Private {
		t.Fatalf("first call = %+v, want a private Defer", calls[0])
	}
	last := calls[len(calls)-1]
	if last.Op != "EditReply" {
		t.Fatalf("last call = %+v, want an EditReply of the private placeholder", last)
	}
	return last.Response
}

func text(r interaction.Response) string {
	var b strings.Builder
	b.WriteString(r.Content)
	for _, e := range r.Embeds {
		b.WriteString(e.Title + " " + e.Description)
		for _, f := range e.Fields {
			b.WriteString(" " + f.Name + " " + f.Value)
		}
	}
	return b.String()
}

func pickerOf(r interaction.Response) (discordgo.SelectMenu, bool) {
	for _, row := range r.Components {
		for _, c := range row.(discordgo.ActionsRow).Components {
			if m, ok := c.(discordgo.SelectMenu); ok {
				return m, true
			}
		}
	}
	return discordgo.SelectMenu{}, false
}

func hasPageButtons(r interaction.Response) bool {
	for _, row := range r.Components {
		for _, c := range row.(discordgo.ActionsRow).Components {
			if _, ok := c.(discordgo.Button); ok {
				return true
			}
		}
	}
	return false
}

func manyFeeds(n int) []guilds.FeedEntry {
	out := make([]guilds.FeedEntry, n)
	for i := range out {
		out[i] = guilds.FeedEntry{Subreddit: fmt.Sprintf("sub%02d", i), PostType: "hot", ChannelID: "c1"}
	}
	return out
}

func TestRedditIsOneServerOnlyModeratorCommandWithPrivateReplies(t *testing.T) {
	cmd := redditCommand(t)
	if cmd.Permission != commands.PermMod || cmd.Context != commands.ContextGuildOnly || cmd.Visibility != interaction.Private || cmd.Response != interaction.Deferred {
		t.Errorf("command = %+v", cmd)
	}
	for _, name := range []string{"add", "remove", "list"} {
		subcommand(t, name)
	}
	for _, d := range commands.Definitions() {
		if d.Name == "add-reddit-feed" || d.Name == "remove-reddit-feed" || d.Name == "reddit-feeds" {
			t.Errorf("old command %q is still registered", d.Name)
		}
	}
}

func TestAddKeepsEveryOption(t *testing.T) {
	var names []string
	for _, o := range subcommand(t, "add").Options {
		names = append(names, o.Name)
	}
	if !slices.Equal(names, []string{"channel", "subreddit", "sort", "author", "title", "pin"}) {
		t.Errorf("add options = %v", names)
	}
	names = nil
	for _, o := range subcommand(t, "remove").Options {
		names = append(names, o.Name)
	}
	if !slices.Equal(names, []string{"channel", "subreddit", "post-type", "author", "title"}) {
		t.Errorf("remove options = %v", names)
	}
}

func TestAddSavesTheFeedWithNormalizedFiltersAndPin(t *testing.T) {
	st := useStore(t)
	var checked []string
	checkFeed = func(_ context.Context, sub, pt string) error { checked = append(checked, sub+"/"+pt); return nil }
	fake := invoke(t, "add", chanOpt("c1"), strOpt("subreddit", " /r/Anime "), strOpt("sort", "New"),
		strOpt("author", "u/SomeOne"), strOpt("title", " Episode "), boolOpt("pin", true))
	want := guilds.FeedEntry{Subreddit: "anime", PostType: "new", Author: "someone", Title: "episode", Pin: true, ChannelID: "c1"}
	if !slices.Equal(st.feeds, []guilds.FeedEntry{want}) {
		t.Errorf("feeds = %+v, want %+v", st.feeds, want)
	}
	if !slices.Equal(checked, []string{"anime/new"}) {
		t.Errorf("availability checked %v", checked)
	}
	if got := text(lastReply(t, fake)); !strings.Contains(got, "<#c1>") || !strings.Contains(got, "r/anime") {
		t.Errorf("reply = %q", got)
	}
}

func TestAddDefaultsToHotWithNoFilters(t *testing.T) {
	st := useStore(t)
	invoke(t, "add", chanOpt("c1"), strOpt("subreddit", "anime"))
	if want := (guilds.FeedEntry{Subreddit: "anime", PostType: "hot", ChannelID: "c1"}); !slices.Equal(st.feeds, []guilds.FeedEntry{want}) {
		t.Errorf("feeds = %+v", st.feeds)
	}
}

func TestAddRefusesBadInputBeforeAskingRedditOrSaving(t *testing.T) {
	cases := []struct {
		name string
		opts []*discordgo.ApplicationCommandInteractionDataOption
		want string
	}{
		{"no channel", []*discordgo.ApplicationCommandInteractionDataOption{strOpt("subreddit", "anime")}, "Pick a channel"},
		{"no subreddit", []*discordgo.ApplicationCommandInteractionDataOption{chanOpt("c1")}, "Pick a channel"},
		{"blank subreddit", []*discordgo.ApplicationCommandInteractionDataOption{chanOpt("c1"), strOpt("subreddit", "r/")}, "valid subreddit"},
		{"bad characters", []*discordgo.ApplicationCommandInteractionDataOption{chanOpt("c1"), strOpt("subreddit", "a b!")}, "letters, numbers"},
		{"bad post type", []*discordgo.ApplicationCommandInteractionDataOption{chanOpt("c1"), strOpt("subreddit", "anime"), strOpt("sort", "top")}, "hot, rising, new"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := useStore(t)
			checkFeed = func(context.Context, string, string) error { t.Error("Reddit was asked"); return nil }
			fake := invoke(t, "add", tc.opts...)
			if got := text(lastReply(t, fake)); !strings.Contains(got, tc.want) {
				t.Errorf("reply = %q, want %q", got, tc.want)
			}
			if len(st.feeds) != 0 {
				t.Errorf("saved %+v", st.feeds)
			}
		})
	}
}

func TestAddExplainsAnUnavailableSubredditInPlainLanguage(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"private or missing", fmt.Errorf("fetch: %w", reddit.ErrUnavailable), "private, banned or does not exist"},
		{"rate limited", errors.New("reddit globally rate-limited until later"), "rate-limiting"},
		{"other", errors.New("timeout"), "couldn't reach Reddit"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := useStore(t)
			checkFeed = func(context.Context, string, string) error { return tc.err }
			got := text(lastReply(t, invoke(t, "add", chanOpt("c1"), strOpt("subreddit", "anime"))))
			if !strings.Contains(got, tc.want) || !strings.Contains(got, "anime") && tc.name != "rate limited" || strings.Contains(got, tc.err.Error()) {
				t.Errorf("reply = %q", got)
			}
			if len(st.feeds) != 0 {
				t.Errorf("saved %+v", st.feeds)
			}
		})
	}
}

func TestAddStoreFailureIsSomethingWentWrong(t *testing.T) {
	st := useStore(t)
	st.failAdd = errors.New("db down")
	r := lastReply(t, invoke(t, "add", chanOpt("c1"), strOpt("subreddit", "anime")))
	if r.Content != interaction.SomethingWentWrong {
		t.Errorf("reply = %+v", r)
	}
}

func TestRemoveDeletesTheMatchingFeedForOneTypeAndItsDeliveryRecords(t *testing.T) {
	st := useStore(t,
		guilds.FeedEntry{Subreddit: "anime", PostType: "hot", ChannelID: "c1"},
		guilds.FeedEntry{Subreddit: "anime", PostType: "new", ChannelID: "c1"})
	fake := invoke(t, "remove", chanOpt("c1"), strOpt("subreddit", "r/Anime"), strOpt("post-type", "hot"))
	if len(st.feeds) != 1 || st.feeds[0].PostType != "new" {
		t.Errorf("feeds = %+v", st.feeds)
	}
	if !slices.Equal(st.checksGone, []string{"c1|anime|hot||"}) {
		t.Errorf("records forgotten %v", st.checksGone)
	}
	if got := text(lastReply(t, fake)); !strings.Contains(got, "<#c1>") {
		t.Errorf("reply = %q", got)
	}
}

func TestRemoveWithoutPostTypeRemovesAllThree(t *testing.T) {
	st := useStore(t,
		guilds.FeedEntry{Subreddit: "anime", PostType: "hot", ChannelID: "c1"},
		guilds.FeedEntry{Subreddit: "anime", PostType: "rising", ChannelID: "c1"},
		guilds.FeedEntry{Subreddit: "anime", PostType: "new", ChannelID: "c1"},
		guilds.FeedEntry{Subreddit: "anime", PostType: "hot", ChannelID: "c2"})
	got := text(lastReply(t, func() *interactiontest.Fake {
		return invoke(t, "remove", chanOpt("c1"), strOpt("subreddit", "anime"))
	}()))
	if len(st.feeds) != 1 || st.feeds[0].ChannelID != "c2" || !strings.Contains(got, "all post types") {
		t.Errorf("feeds = %+v, reply %q", st.feeds, got)
	}
}

func TestRemoveNarrowsByAuthorAndTitle(t *testing.T) {
	st := useStore(t,
		guilds.FeedEntry{Subreddit: "anime", PostType: "hot", ChannelID: "c1", Author: "bob"},
		guilds.FeedEntry{Subreddit: "anime", PostType: "hot", ChannelID: "c1", Author: "amy", Title: "ep"})
	invoke(t, "remove", chanOpt("c1"), strOpt("subreddit", "anime"), strOpt("post-type", "hot"), strOpt("author", "u/Amy"), strOpt("title", "EP"))
	if len(st.feeds) != 1 || st.feeds[0].Author != "bob" {
		t.Errorf("feeds = %+v", st.feeds)
	}
}

func TestRemoveRepliesForNoFeedsNoMatchAndBadInput(t *testing.T) {
	st := useStore(t)
	if got := text(lastReply(t, invoke(t, "remove", chanOpt("c1"), strOpt("subreddit", "anime")))); !strings.Contains(got, "no Reddit feeds") {
		t.Errorf("empty: %q", got)
	}
	st.feeds = []guilds.FeedEntry{{Subreddit: "anime", PostType: "hot", ChannelID: "c1"}}
	if got := text(lastReply(t, invoke(t, "remove", chanOpt("c2"), strOpt("subreddit", "anime")))); !strings.Contains(got, "No matching feed") {
		t.Errorf("no match: %q", got)
	}
	if got := text(lastReply(t, invoke(t, "remove", chanOpt("c1"), strOpt("subreddit", "anime"), strOpt("post-type", "top")))); !strings.Contains(got, "hot, rising, new") {
		t.Errorf("bad type: %q", got)
	}
	if got := text(lastReply(t, invoke(t, "remove", chanOpt("c1"), strOpt("subreddit", "r/")))); !strings.Contains(got, "valid subreddit") {
		t.Errorf("blank: %q", got)
	}
	if len(st.feeds) != 1 {
		t.Errorf("feeds = %+v", st.feeds)
	}
}

func TestRemoveStoreFailuresAreSomethingWentWrong(t *testing.T) {
	st := useStore(t, guilds.FeedEntry{Subreddit: "anime", PostType: "hot", ChannelID: "c1"})
	st.failRemove = errors.New("db down")
	if r := lastReply(t, invoke(t, "remove", chanOpt("c1"), strOpt("subreddit", "anime"))); r.Content != interaction.SomethingWentWrong {
		t.Errorf("remove failure reply = %+v", r)
	}
	st.failGet = errors.New("db down")
	if r := lastReply(t, invoke(t, "remove", chanOpt("c1"), strOpt("subreddit", "anime"))); r.Content != interaction.SomethingWentWrong {
		t.Errorf("load failure reply = %+v", r)
	}
}

func TestRemoveAutocompleteMatchesAnywhereAndClipsTo100(t *testing.T) {
	st := useStore(t,
		guilds.FeedEntry{Subreddit: "anime", PostType: "hot", ChannelID: "c1"},
		guilds.FeedEntry{Subreddit: "manga", PostType: "hot", ChannelID: "c2"},
		guilds.FeedEntry{Subreddit: strings.Repeat("x", 150), PostType: "hot", ChannelID: "c2"})
	got := subredditChoices(t.Context(), st, "g", "nim")
	if len(got) != 1 || got[0].Value != "anime" || got[0].Name != "r/anime" {
		t.Errorf("subreddit choices = %+v", got)
	}
	for _, c := range subredditChoices(t.Context(), st, "g", "") {
		if len([]rune(c.Name)) > 100 || len([]rune(c.Value.(string))) > 100 {
			t.Errorf("choice %q is over 100 characters", c.Name)
		}
	}
	names := map[string]string{"c1": "general"}
	ch := channelChoices(t.Context(), st, func(id string) string { return names[id] }, "g", "ener")
	if len(ch) != 1 || ch[0].Value != "c1" || ch[0].Name != "#general" {
		t.Errorf("channel choices = %+v", ch)
	}
	if ch := channelChoices(t.Context(), st, func(string) string { return "" }, "g", "c2"); len(ch) != 1 || ch[0].Name != "#c2" {
		t.Errorf("unnamed channel choices = %+v", ch)
	}
	if got := subredditChoices(t.Context(), st, "", "a"); got != nil {
		t.Errorf("no guild: %+v", got)
	}
	if pts := autocompletePostType(t.Context(), nil, nil, nil, "post-type", "IS"); len(pts) != 1 || pts[0].Value != "rising" {
		t.Errorf("post types = %+v", pts)
	}
}

func TestListShowsFeedsWithChannelMentionsAndAPickerPrivately(t *testing.T) {
	useStore(t,
		guilds.FeedEntry{Subreddit: "anime", PostType: "hot", ChannelID: "c1", Author: "bob", Title: "ep", Pin: true},
		guilds.FeedEntry{Subreddit: "manga", PostType: "new", ChannelID: "c2"})
	r := lastReply(t, invoke(t, "list"))
	body := text(r)
	for _, want := range []string{"r/anime", "<#c1>", "u/bob", "ep", "Pinned", "r/manga", "<#c2>", "new"} {
		if !strings.Contains(body, want) {
			t.Errorf("list %q lacks %q", body, want)
		}
	}
	picker, ok := pickerOf(r)
	if !ok || len(picker.Options) != 2 || hasPageButtons(r) {
		t.Errorf("picker %+v, ok %v, buttons %v", picker, ok, hasPageButtons(r))
	}
}

func TestListWithNoFeedsSaysSoWithoutAPicker(t *testing.T) {
	useStore(t)
	r := lastReply(t, invoke(t, "list"))
	if !strings.Contains(text(r), "no Reddit feeds") || len(r.Components) != 0 {
		t.Errorf("reply = %+v", r)
	}
}

func TestListStoreFailureIsSomethingWentWrong(t *testing.T) {
	st := useStore(t)
	st.failGet = errors.New("db down")
	if r := lastReply(t, invoke(t, "list")); r.Content != interaction.SomethingWentWrong {
		t.Errorf("reply = %+v", r)
	}
}

func TestListPagesCarryTheirOwnPickerOfAtMost25(t *testing.T) {
	useStore(t, manyFeeds(30)...)
	first := lastReply(t, invoke(t, "list"))
	picker, _ := pickerOf(first)
	if len(picker.Options) != 25 || !hasPageButtons(first) {
		t.Fatalf("first page picker has %d options, buttons %v", len(picker.Options), hasPageButtons(first))
	}
	second := RenderFeedsPage(t.Context(), nil, nil, &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{GuildID: "g"}}, 1, 30, "user")
	got, _ := pickerOf(second.Response)
	if second.Refusal != "" || len(got.Options) != 5 || got.Options[0].Value != shortCode(manyFeeds(30)[25]) {
		t.Errorf("second page = %+v", second)
	}
}

func TestPageRendererRefusesAnEmptyOrUnreadableList(t *testing.T) {
	st := useStore(t)
	i := &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{GuildID: "g"}}
	if p := RenderFeedsPage(t.Context(), nil, nil, i, 0, 0, "user"); !strings.Contains(p.Refusal, "no Reddit feeds") {
		t.Errorf("empty: %+v", p)
	}
	st.failGet = errors.New("db down")
	if p := RenderFeedsPage(t.Context(), nil, nil, i, 0, 0, "user"); p.Refusal != interaction.SomethingWentWrong {
		t.Errorf("failure: %+v", p)
	}
}

func TestEveryCustomIDStaysUnder100Characters(t *testing.T) {
	long := strings.Repeat("x", 300)
	st := useStore(t, guilds.FeedEntry{Subreddit: "anime", PostType: "hot", ChannelID: "123456789012345678", Title: long, Author: long})
	_ = st
	r := renderPage(st.feeds, 0, "123456789012345678")
	picker, _ := pickerOf(r)
	if len(picker.CustomID) >= 100 {
		t.Errorf("picker custom ID has %d characters", len(picker.CustomID))
	}
	for _, o := range picker.Options {
		if len([]rune(o.Value)) > 100 || len([]rune(o.Label)) > 100 || len([]rune(o.Description)) > 100 {
			t.Errorf("option %+v is over 100 characters", o)
		}
	}
	for _, row := range renderPage(manyFeeds(30), 0, "123456789012345678").Components {
		for _, c := range row.(discordgo.ActionsRow).Components {
			if b, ok := c.(discordgo.Button); ok && len(b.CustomID) >= 100 {
				t.Errorf("button custom ID %q is too long", b.CustomID)
			}
		}
	}
}

func pick(user, customID string, values ...string) *interaction.Context {
	i := &discordgo.Interaction{Type: discordgo.InteractionMessageComponent, GuildID: "g", ChannelID: "c",
		Member: &discordgo.Member{User: &discordgo.User{ID: user}},
		Data:   discordgo.MessageComponentInteractionData{CustomID: customID, ComponentType: discordgo.SelectMenuComponent, Values: values}}
	return interaction.NewContext(&interactiontest.Fake{}, i, nil, interaction.Private, interaction.Direct)
}

func firstPicker(t *testing.T, owner string) discordgo.SelectMenu {
	t.Helper()
	st, _ := storeOf(nil).GetFeeds(t.Context(), "g")
	p, ok := pickerOf(renderPage(st, 0, owner))
	if !ok {
		t.Fatal("no picker")
	}
	return p
}

func TestChoosingAFeedRemovesItWithoutRetypingItsFiltersAndRerenders(t *testing.T) {
	hot := guilds.FeedEntry{Subreddit: "anime", PostType: "hot", ChannelID: "c1", Author: "bob", Title: "ep", Pin: true}
	st := useStore(t, hot, guilds.FeedEntry{Subreddit: "manga", PostType: "new", ChannelID: "c2"})
	picker := firstPicker(t, "user")
	ic := pick("user", picker.CustomID, picker.Options[0].Value)
	if err := handleRemovePicker(ic, nil); err != nil {
		t.Fatal(err)
	}
	if len(st.feeds) != 1 || st.feeds[0].Subreddit != "manga" {
		t.Errorf("feeds = %+v", st.feeds)
	}
	if !slices.Equal(st.checksGone, []string{"c1|anime|hot|bob|ep"}) {
		t.Errorf("records forgotten %v", st.checksGone)
	}
	update := lastCall(t, ic.Port.(*interactiontest.Fake), "UpdateMessage")
	got, _ := pickerOf(update.Response)
	if len(got.Options) != 1 || strings.Contains(text(update.Response), "r/anime") {
		t.Errorf("update = %+v", update.Response)
	}
}

func lastCall(t *testing.T, fake *interactiontest.Fake, op string) interactiontest.Call {
	t.Helper()
	calls := fake.Calls()
	for i := len(calls) - 1; i >= 0; i-- {
		if calls[i].Op == op {
			return calls[i]
		}
	}
	t.Fatalf("no %s call in %v", op, fake.Ops())
	return interactiontest.Call{}
}

func TestChoosingAnUnfilteredFeedKeepsItsFilteredSiblings(t *testing.T) {
	plain := guilds.FeedEntry{Subreddit: "anime", PostType: "hot", ChannelID: "c1"}
	sibling := guilds.FeedEntry{Subreddit: "anime", PostType: "hot", ChannelID: "c1", Author: "bob"}
	st := useStore(t, plain, sibling)
	st.failAdd = errors.New("AddFeed must not be called")
	picker := firstPicker(t, "user")
	if err := handleRemovePicker(pick("user", picker.CustomID, shortCode(plain)), nil); err != nil {
		t.Fatal(err)
	}
	if st.addCalls != 0 {
		t.Errorf("AddFeed called %d times", st.addCalls)
	}
	if !slices.Equal(st.feeds, []guilds.FeedEntry{sibling}) {
		t.Errorf("feeds = %+v, want only the filtered sibling", st.feeds)
	}
	if len(st.checksGone) != 0 {
		t.Errorf("the sibling's delivery records were forgotten: %v", st.checksGone)
	}
}

func TestChoosingTheLastFeedClearsThePicker(t *testing.T) {
	useStore(t, guilds.FeedEntry{Subreddit: "anime", PostType: "hot", ChannelID: "c1"})
	picker := firstPicker(t, "user")
	ic := pick("user", picker.CustomID, picker.Options[0].Value)
	if err := handleRemovePicker(ic, nil); err != nil {
		t.Fatal(err)
	}
	r := lastCall(t, ic.Port.(*interactiontest.Fake), "UpdateMessage").Response
	if !strings.Contains(text(r), "no Reddit feeds") || r.Components == nil || len(r.Components) != 0 {
		t.Errorf("response = %+v", r)
	}
}

func TestPickerRemovesOnlyForTheModeratorWhoseListItIs(t *testing.T) {
	st := useStore(t, guilds.FeedEntry{Subreddit: "anime", PostType: "hot", ChannelID: "c1"})
	picker := firstPicker(t, "user")
	ic := pick("stranger", picker.CustomID, picker.Options[0].Value)
	if err := handleRemovePicker(ic, nil); err != nil {
		t.Fatal(err)
	}
	calls := ic.Port.(*interactiontest.Fake).Calls()
	if len(calls) != 1 || calls[0].Op != "Reply" || calls[0].Visibility != interaction.Private {
		t.Fatalf("calls = %+v, want one private refusal", calls)
	}
	if len(st.feeds) != 1 || len(st.checksGone) != 0 {
		t.Errorf("a refused press must not change the store: %+v", st.feeds)
	}
}

func TestPickerChecksTheOwnerBeforeReadingTheStore(t *testing.T) {
	st := useStore(t, guilds.FeedEntry{Subreddit: "anime", PostType: "hot", ChannelID: "c1"})
	st.failGet = errors.New("must not be read")
	ic := pick("stranger", pickerPrefix+"user:0", "x")
	if err := handleRemovePicker(ic, nil); err != nil {
		t.Fatal(err)
	}
	if got := ic.Port.(*interactiontest.Fake).Calls()[0].Response.Content; got != msgOnlyOwnerRemoves {
		t.Errorf("refusal = %q", got)
	}
}

func TestPickerRejectsAMalformedCustomIDOrChoice(t *testing.T) {
	useStore(t, guilds.FeedEntry{Subreddit: "anime", PostType: "hot", ChannelID: "c1"})
	for _, ic := range []*interaction.Context{
		pick("user", pickerPrefix+"user", "x"),
		pick("user", pickerPrefix+"user:nope", "x"),
		pick("user", pickerPrefix+"user:0"),
	} {
		if err := handleRemovePicker(ic, nil); err != nil {
			t.Fatal(err)
		}
		if got := ic.Port.(*interactiontest.Fake).Calls()[0].Response.Content; got != msgStalePicker {
			t.Errorf("reply = %q", got)
		}
	}
}

func TestPickerIgnoresAFeedThatIsAlreadyGone(t *testing.T) {
	st := useStore(t, guilds.FeedEntry{Subreddit: "manga", PostType: "hot", ChannelID: "c1"})
	ic := pick("user", pickerPrefix+"user:0", "c9|hot|gone||")
	if err := handleRemovePicker(ic, nil); err != nil {
		t.Fatal(err)
	}
	calls := ic.Port.(*interactiontest.Fake).Calls()
	if len(calls) != 1 || calls[0].Op != "UpdateMessage" || len(st.feeds) != 1 {
		t.Errorf("calls %+v, feeds %+v: a stale choice just re-renders", calls, st.feeds)
	}
}

func TestPickerRemovesTheChosenFeedWhenIdentitiesShareTheirFirst100Characters(t *testing.T) {
	prefix := strings.Repeat("t", 120)
	a := guilds.FeedEntry{Subreddit: "anime", PostType: "hot", ChannelID: "c1", Title: prefix + "a"}
	b := guilds.FeedEntry{Subreddit: "anime", PostType: "hot", ChannelID: "c1", Title: prefix + "b"}
	if shortCode(a) == shortCode(b) {
		t.Fatal("two different feeds need different short codes")
	}
	for _, chosen := range []guilds.FeedEntry{a, b} {
		st := useStore(t, a, b)
		ic := pick("user", pickerPrefix+"user:0", shortCode(chosen))
		if err := handleRemovePicker(ic, nil); err != nil {
			t.Fatal(err)
		}
		if len(st.feeds) != 1 || st.feeds[0] == chosen {
			t.Errorf("feeds %+v: want only the other feed left", st.feeds)
		}
	}
}

func TestShortCodeFitsDiscordLimits(t *testing.T) {
	f := guilds.FeedEntry{Subreddit: strings.Repeat("s", 300), PostType: "hot", ChannelID: "c1", Author: strings.Repeat("a", 300), Title: strings.Repeat("t", 300)}
	if n := len([]rune(shortCode(f))); n == 0 || n > 100 {
		t.Errorf("short code length %d", n)
	}
	if len(pickerPrefix+strings.Repeat("9", 20)+":99") > 100 {
		t.Error("picker custom ID too long")
	}
}

func TestPickerStoreFailuresAreSomethingWentWrong(t *testing.T) {
	st := useStore(t, guilds.FeedEntry{Subreddit: "anime", PostType: "hot", ChannelID: "c1"})
	picker := firstPicker(t, "user")
	st.failRemove = errors.New("db down")
	ic := pick("user", picker.CustomID, picker.Options[0].Value)
	if err := handleRemovePicker(ic, nil); err != nil {
		t.Fatal(err)
	}
	if got := ic.Port.(*interactiontest.Fake).Calls()[0].Response.Content; got != interaction.SomethingWentWrong {
		t.Errorf("reply = %q", got)
	}
}

func TestMissingPermissionsAreStillExplainedByTheHelper(t *testing.T) {
	// Fail words a known Discord error by name; a store that surfaces one gets the sentence, not the raw error.
	st := useStore(t)
	st.failAdd = &discordgo.RESTError{Response: &http.Response{StatusCode: 403}}
	r := lastReply(t, invoke(t, "add", chanOpt("c1"), strOpt("subreddit", "anime")))
	if strings.Contains(r.Content, "403") {
		t.Errorf("raw error shown: %q", r.Content)
	}
}

func TestPickerOptionsAreNumberedLikeTheEmbedFields(t *testing.T) {
	a := guilds.FeedEntry{Subreddit: "anime", PostType: "hot", ChannelID: "c1"}
	b := guilds.FeedEntry{Subreddit: "anime", PostType: "hot", ChannelID: "c2"}
	useStore(t, a, b)
	resp := renderPage([]guilds.FeedEntry{a, b}, 0, "user")
	fields := resp.Embeds[0].Fields
	picker := firstPicker(t, "user")
	if len(picker.Options) != 2 {
		t.Fatalf("options = %d", len(picker.Options))
	}
	for i, o := range picker.Options {
		prefix := strconv.Itoa(i+1) + ". "
		if !strings.HasPrefix(o.Label, prefix) || !strings.HasPrefix(fields[i].Name, prefix) {
			t.Errorf("option %q / field %q lack prefix %q", o.Label, fields[i].Name, prefix)
		}
		if o.Description != "" {
			t.Errorf("option shows a description %q", o.Description)
		}
	}
}
