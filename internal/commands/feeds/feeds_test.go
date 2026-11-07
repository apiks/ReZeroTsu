package feeds

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/database/guilds"
	"ReZeroTsu/internal/interaction"
	"ReZeroTsu/internal/interaction/interactiontest"
	"ReZeroTsu/internal/reddit"

	"github.com/bwmarrin/discordgo"
)

// fakeStore is an in-memory feed list. RemoveExactFeed matches whole entries only, as the real one does.
type fakeStore struct {
	feeds      []guilds.FeedEntry
	checksGone []string
	failGet    error
	failRemove error
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

func (f *fakeStore) DeleteChecks(_ context.Context, _, channelID, subreddit, postType, author, title string) error {
	f.checksGone = append(f.checksGone, strings.Join([]string{channelID, subreddit, postType, author, title}, "|"))
	return nil
}

// useStore installs the fake store.
func useStore(t *testing.T, feeds ...guilds.FeedEntry) *fakeStore {
	t.Helper()
	st := &fakeStore{feeds: feeds}
	oldStore := storeOf
	storeOf = func(*database.Client) store { return st }
	t.Cleanup(func() { storeOf = oldStore })
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
	if !strings.Contains(text(r), "There are no Reddit feeds.") || strings.Contains(text(r), "/reddit add") || len(r.Components) != 0 {
		t.Errorf("reply = %+v", r)
	}
}

func TestListSaysWhenTheFeedsStopOrStopped(t *testing.T) {
	cases := []struct {
		name string
		now  time.Time
		want string
	}{
		{"before", reddit.Cutoff.Add(-time.Second), "These feeds stop on 13 November 2026, when Reddit shuts down its RSS feeds."},
		{"at the cutoff", reddit.Cutoff, "These feeds stopped on 13 November 2026, when Reddit shut down its RSS feeds."},
		{"after", reddit.Cutoff.Add(10 * 24 * time.Hour), "These feeds stopped on 13 November 2026, when Reddit shut down its RSS feeds."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setNow(t, tc.now)
			useStore(t, manyFeeds(30)...)
			first := lastReply(t, invoke(t, "list"))
			if desc := first.Embeds[0].Description; !strings.HasPrefix(desc, tc.want) || !strings.Contains(desc, "Page 1 of 2") {
				t.Errorf("first page description = %q", desc)
			}
			second := RenderFeedsPage(t.Context(), nil, nil, &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{GuildID: "g"}}, 1, 30, "user")
			if desc := second.Response.Embeds[0].Description; !strings.HasPrefix(desc, tc.want) {
				t.Errorf("second page description = %q", desc)
			}
			if picker, ok := pickerOf(first); !ok || len(picker.Options) != 25 {
				t.Errorf("picker = %+v", picker)
			}
		})
	}
}

func TestListNoticeShowsAboveASinglePageOfFeeds(t *testing.T) {
	setNow(t, reddit.Cutoff.Add(-time.Hour))
	useStore(t, guilds.FeedEntry{Subreddit: "anime", PostType: "hot", ChannelID: "c1"})
	r := lastReply(t, invoke(t, "list"))
	if desc := r.Embeds[0].Description; !strings.HasPrefix(desc, "These feeds stop on 13 November 2026") {
		t.Errorf("description = %q", desc)
	}
}

func TestRemoveStillWorksAfterTheCutoff(t *testing.T) {
	setNow(t, reddit.Cutoff.Add(24*time.Hour))
	a := guilds.FeedEntry{Subreddit: "anime", PostType: "hot", ChannelID: "c1"}
	st := useStore(t, a)
	invoke(t, "remove", strOpt("feed", "anime"))
	if len(st.feeds) != 0 {
		t.Errorf("feeds = %+v", st.feeds)
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
	picker := firstPicker(t, "user")
	if err := handleRemovePicker(pick("user", picker.CustomID, shortCode(plain)), nil); err != nil {
		t.Fatal(err)
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
