package react

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/database/guilds"
	"ReZeroTsu/internal/interaction"
	"ReZeroTsu/internal/interaction/interactiontest"

	"github.com/bwmarrin/discordgo"
)

// fakeStore is an in-memory reaction-role store keyed by message ID, like the guild repository's record.
type fakeStore struct {
	entries  map[string]*guilds.ReactJoin
	failGet  error
	failSave error
	failDel  error
	saves    int
}

func (f *fakeStore) GetReactJoinMap(context.Context, string) (map[string]*guilds.ReactJoin, error) {
	if f.failGet != nil {
		return nil, f.failGet
	}
	out := map[string]*guilds.ReactJoin{}
	for id, e := range f.entries {
		out[id] = cloneEntry(e)
	}
	return out, nil
}

func (f *fakeStore) SaveReactJoinEntry(_ context.Context, _, messageID string, e *guilds.ReactJoin) error {
	if f.failSave != nil {
		return f.failSave
	}
	f.saves++
	f.entries[messageID] = cloneEntry(e)
	return nil
}

func (f *fakeStore) DeleteReactJoinEntry(_ context.Context, _, messageID string) error {
	if f.failDel != nil {
		return f.failDel
	}
	delete(f.entries, messageID)
	return nil
}

func cloneEntry(e *guilds.ReactJoin) *guilds.ReactJoin {
	c := &guilds.ReactJoin{ChannelID: e.ChannelID}
	for _, m := range e.RoleEmojiMap {
		n := map[string][]string{}
		for k, v := range m {
			n[k] = slices.Clone(v)
		}
		c.RoleEmojiMap = append(c.RoleEmojiMap, n)
	}
	return c
}

func useStore(t *testing.T, entries map[string]*guilds.ReactJoin) *fakeStore {
	t.Helper()
	st := &fakeStore{entries: entries}
	if st.entries == nil {
		st.entries = map[string]*guilds.ReactJoin{}
	}
	old := bindStoreOf
	bindStoreOf = func(*database.Client) bindStore { return st }
	t.Cleanup(func() { bindStoreOf = old })
	return st
}

func command(t *testing.T, name string) *commands.Command {
	t.Helper()
	for _, c := range commands.AllCommands() {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("%q is not registered", name)
	return nil
}

// target is the message the moderator right-clicked: message m1 in channel c1.
func target() *discordgo.Message { return &discordgo.Message{ID: "m1", ChannelID: "c1"} }

func rightClick(t *testing.T, fake *interactiontest.Fake, name string, msg *discordgo.Message) error {
	t.Helper()
	cmd := command(t, name)
	data := discordgo.ApplicationCommandInteractionData{Name: name, CommandType: discordgo.MessageApplicationCommand, TargetID: "m1"}
	if msg != nil {
		data.Resolved = &discordgo.ApplicationCommandInteractionDataResolved{Messages: map[string]*discordgo.Message{"m1": msg}}
	}
	ic := interactiontest.NewCommandContext(fake, cmd.Visibility, cmd.Response, data)
	return cmd.Run(ic)
}

const formID = "reactrole:add:c1:m1"

// worldFake is a server where role "1000000000000000001" sits below the bot's highest role and "1000000000000000002" sits at it.
// The default submitter holds highestRole, above every other role, so the moderator's own hierarchy never gets in the way.
func worldFake() *interactiontest.Fake {
	return &interactiontest.Fake{
		Bot: &discordgo.User{ID: "bot"},
		Guilds: map[string]*discordgo.Guild{"g": {ID: "g", OwnerID: "owner", Members: []*discordgo.Member{
			{User: &discordgo.User{ID: "bot"}, Roles: []string{"1000000000000000005", "1000000000000000004"}},
		}}},
		RoleSets: map[string][]*discordgo.Role{"g": {
			{ID: "1000000000000000005", Position: 5}, {ID: "1000000000000000004", Position: 1},
			{ID: "1000000000000000001", Position: 2}, {ID: "1000000000000000002", Position: 5}, {ID: "1000000000000000003", Position: 9},
			{ID: highestRole, Position: 20},
		}},
	}
}

const highestRole = "1000000000000000020"

func submit(t *testing.T, fake *interactiontest.Fake, customID, emoji string, roles ...string) error {
	t.Helper()
	return submitAs(t, fake, &discordgo.Member{User: &discordgo.User{ID: "user"}, Roles: []string{highestRole}}, customID, emoji, roles...)
}

func submitAs(t *testing.T, fake *interactiontest.Fake, member *discordgo.Member, customID, emoji string, roles ...string) error {
	t.Helper()
	i := &discordgo.Interaction{Type: discordgo.InteractionModalSubmit, GuildID: "g", ChannelID: "c",
		Member: member,
		Data: discordgo.ModalSubmitInteractionData{CustomID: customID, Components: []discordgo.MessageComponent{
			&discordgo.Label{Component: discordgo.TextInput{CustomID: emojiField, Value: emoji}},
			&discordgo.Label{Component: discordgo.SelectMenu{CustomID: roleField, MenuType: discordgo.RoleSelectMenu, Values: roles}},
		}}}
	ic := interaction.NewContext(fake, i, nil, interaction.Private, interaction.Direct)
	return handleAddForm(ic, nil)
}

func only(t *testing.T, fake *interactiontest.Fake, op string) interactiontest.Call {
	t.Helper()
	var found []interactiontest.Call
	for _, c := range fake.Calls() {
		if c.Op == op {
			found = append(found, c)
		}
	}
	if len(found) != 1 {
		t.Fatalf("want exactly one %s, ops = %v", op, fake.Ops())
	}
	return found[0]
}

func replyText(t *testing.T, fake *interactiontest.Fake) string {
	t.Helper()
	c := only(t, fake, "Reply")
	if c.Visibility != interaction.Private {
		t.Errorf("reply visibility = %v, want private", c.Visibility)
	}
	return c.Response.Content
}

func bindings(t *testing.T, st *fakeStore, messageID string) []map[string][]string {
	t.Helper()
	e := st.entries[messageID]
	if e == nil {
		return nil
	}
	return e.RoleEmojiMap
}

func TestTheTwoRightClickCommandsAreModGuildCommands(t *testing.T) {
	for _, name := range []string{"Add reaction role", "Remove reaction roles"} {
		c := command(t, name)
		if c.Kind != commands.KindMessage || c.Permission != commands.PermMod || c.Context != commands.ContextGuildOnly || c.Visibility != commands.Private {
			t.Errorf("%s = kind %v perm %v context %v visibility %v", name, c.Kind, c.Permission, c.Context, c.Visibility)
		}
	}
}

func TestAddReactionRoleOpensAFormWithAnEmojiBoxAndARolePicker(t *testing.T) {
	fake := &interactiontest.Fake{}
	if err := rightClick(t, fake, "Add reaction role", target()); err != nil {
		t.Fatal(err)
	}
	form := only(t, fake, "OpenForm").Form
	if form.CustomID != formID || len(form.CustomID) >= 100 || form.Title == "" {
		t.Errorf("form = %+v", form)
	}
	if len(form.Components) != 2 {
		t.Fatalf("components = %d, want 2", len(form.Components))
	}
	emoji := form.Components[0].(*discordgo.Label)
	box := emoji.Component.(discordgo.TextInput)
	if emoji.Label == "" || box.CustomID != emojiField || box.Required == nil || !*box.Required {
		t.Errorf("emoji box = %+v in label %q", box, emoji.Label)
	}
	role := form.Components[1].(*discordgo.Label)
	picker := role.Component.(discordgo.SelectMenu)
	if role.Label == "" || picker.MenuType != discordgo.RoleSelectMenu || picker.CustomID != roleField || picker.MaxValues > 1 ||
		picker.MinValues == nil || *picker.MinValues != 1 {
		t.Errorf("role picker = %+v in label %q", picker, role.Label)
	}
}

func TestAddReactionRoleWithoutATargetIsRefusedPrivately(t *testing.T) {
	fake := &interactiontest.Fake{}
	if err := rightClick(t, fake, "Add reaction role", nil); err != nil {
		t.Fatal(err)
	}
	if replyText(t, fake) == "" || slices.Contains(fake.Ops(), "OpenForm") {
		t.Errorf("ops %v", fake.Ops())
	}
}

func TestAddingWithAValidEmojiSavesTheBindingWithItsChannelAndReactsOnTheTargetMessage(t *testing.T) {
	st := useStore(t, nil)
	fake := worldFake()
	if err := submit(t, fake, formID, "🎉", "1000000000000000001"); err != nil {
		t.Fatal(err)
	}
	react := only(t, fake, "AddReaction")
	if react.ChannelID != "c1" || react.MessageID != "m1" || react.Emoji != "🎉" {
		t.Errorf("reaction = %+v, want on c1/m1, not on the channel the form was submitted in", react)
	}
	saved := st.entries["m1"]
	if saved == nil || saved.ChannelID != "c1" || len(saved.RoleEmojiMap) != 1 || !slices.Equal(saved.RoleEmojiMap[0]["1000000000000000001"], []string{"🎉"}) {
		t.Errorf("stored = %+v", saved)
	}
	got := replyText(t, fake)
	for _, want := range []string{"🎉", "<@&1000000000000000001>", "https://discord.com/channels/g/c1/m1"} {
		if !strings.Contains(got, want) {
			t.Errorf("reply %q lacks %q", got, want)
		}
	}
	if strings.Contains(strings.ToLower(got), "highest role") {
		t.Errorf("reply %q warns although the role is below the bot's", got)
	}
}

func TestAServerEmojiIsStoredWithoutAngleBracketsAndReactedWithAsStored(t *testing.T) {
	for emoji, want := range map[string]string{"<:party:123>": "party:123", "<a:dance:456>": "a:dance:456"} {
		st := useStore(t, nil)
		fake := worldFake()
		if err := submit(t, fake, formID, emoji, "1000000000000000001"); err != nil {
			t.Fatal(err)
		}
		if got := only(t, fake, "AddReaction").Emoji; got != want {
			t.Errorf("%s: reaction %q, want %q", emoji, got, want)
		}
		if got := bindings(t, st, "m1"); len(got) != 1 || !slices.Equal(got[0]["1000000000000000001"], []string{want}) {
			t.Errorf("%s: stored %v", emoji, got)
		}
	}
}

func TestAnEmojiTheFullFormIsMissingFromGetsAPrivateExplanationAndNothingChanges(t *testing.T) {
	for _, emoji := range []string{"", "   ", "<:broken", "<party>"} {
		st := useStore(t, nil)
		fake := worldFake()
		if err := submit(t, fake, formID, emoji, "1000000000000000001"); err != nil {
			t.Fatal(err)
		}
		if got := replyText(t, fake); !strings.Contains(got, "<:name:id>") {
			t.Errorf("%q: reply %q does not show the accepted form", emoji, got)
		}
		if slices.Contains(fake.Ops(), "AddReaction") || st.saves != 0 {
			t.Errorf("%q: ops %v, saves %d", emoji, fake.Ops(), st.saves)
		}
	}
}

func TestAnEmojiDiscordRejectsIsExplainedAndNotStored(t *testing.T) {
	st := useStore(t, nil)
	fake := worldFake()
	fake.Fail = map[string]error{"AddReaction": &discordgo.RESTError{Message: &discordgo.APIErrorMessage{Code: discordgo.ErrCodeUnknownEmoji}}}
	if err := submit(t, fake, formID, "notanemoji", "1000000000000000001"); err != nil {
		t.Fatal(err)
	}
	if got := replyText(t, fake); !strings.Contains(got, "isn't an emoji") {
		t.Errorf("reply %q", got)
	}
	if st.saves != 0 {
		t.Error("a binding for an emoji nobody can react with was stored")
	}
}

func TestAddingNamesTheMissingPermissionWhenTheBotCannotReact(t *testing.T) {
	st := useStore(t, nil)
	fake := worldFake()
	fake.Fail = map[string]error{"AddReaction": &discordgo.RESTError{Response: &http.Response{StatusCode: 403}}}
	if err := submit(t, fake, formID, "🎉", "1000000000000000001"); err != nil {
		t.Fatal(err)
	}
	got := replyText(t, fake)
	if !strings.Contains(got, "<#c1>") || !strings.Contains(got, "Add Reactions") || !strings.Contains(got, "Read Message History") {
		t.Errorf("reply %q", got)
	}
	if st.saves != 0 {
		t.Error("a binding was stored although the bot cannot react")
	}
}

func TestADuplicateBindingIsReportedAndNotStoredTwice(t *testing.T) {
	st := useStore(t, map[string]*guilds.ReactJoin{"m1": {ChannelID: "c1", RoleEmojiMap: []map[string][]string{{"1000000000000000001": {"party:123"}}}}})
	for _, tc := range []struct{ emoji, role string }{
		{"<:party:123>", "1000000000000000001"},  // the same binding
		{"<a:party:123>", "1000000000000000001"}, // the animated spelling of it
		{"<:PARTY:123>", "1000000000000000001"},  // case does not matter
		{"<:party:123>", "1000000000000000004"},  // the emoji is taken by another role, so reacting could only reach the first
	} {
		fake := worldFake()
		if err := submit(t, fake, formID, tc.emoji, tc.role); err != nil {
			t.Fatal(err)
		}
		if got := replyText(t, fake); !strings.Contains(got, "already") || !strings.Contains(got, "<@&1000000000000000001>") {
			t.Errorf("%+v: reply %q", tc, got)
		}
		if slices.Contains(fake.Ops(), "AddReaction") {
			t.Errorf("%+v: the bot reacted again", tc)
		}
	}
	if st.saves != 0 || len(st.entries["m1"].RoleEmojiMap) != 1 {
		t.Errorf("saves %d, stored %+v", st.saves, st.entries["m1"])
	}
}

func TestAnotherEmojiForTheSameRoleJoinsItsBinding(t *testing.T) {
	st := useStore(t, map[string]*guilds.ReactJoin{"m1": {ChannelID: "c1", RoleEmojiMap: []map[string][]string{{"1000000000000000001": {"🎉"}}}}})
	if err := submit(t, worldFake(), formID, "🔥", "1000000000000000001"); err != nil {
		t.Fatal(err)
	}
	got := bindings(t, st, "m1")
	if len(got) != 1 || !slices.Equal(got[0]["1000000000000000001"], []string{"🎉", "🔥"}) {
		t.Errorf("stored %v", got)
	}
}

func TestAddingToALegacyRecordKeepsItsBindingsAndLearnsTheChannel(t *testing.T) {
	st := useStore(t, map[string]*guilds.ReactJoin{"m1": {RoleEmojiMap: []map[string][]string{{"1000000000000000002": {"🎉"}}}}})
	if err := submit(t, worldFake(), formID, "🔥", "1000000000000000001"); err != nil {
		t.Fatal(err)
	}
	e := st.entries["m1"]
	if e.ChannelID != "c1" || len(e.RoleEmojiMap) != 2 || !slices.Equal(e.RoleEmojiMap[0]["1000000000000000002"], []string{"🎉"}) || !slices.Equal(e.RoleEmojiMap[1]["1000000000000000001"], []string{"🔥"}) {
		t.Errorf("stored %+v", e)
	}
}

func TestARoleAtOrAboveTheBotsHighestRoleIsSavedWithAWarning(t *testing.T) {
	for _, role := range []string{"1000000000000000002", "1000000000000000003"} {
		st := useStore(t, nil)
		fake := worldFake()
		if err := submit(t, fake, formID, "🎉", role); err != nil {
			t.Fatal(err)
		}
		got := replyText(t, fake)
		if !strings.Contains(got, "<@&"+role+">") || !strings.Contains(got, "above") || !strings.Contains(got, "highest role") {
			t.Errorf("%s: reply %q", role, got)
		}
		if st.saves != 1 {
			t.Errorf("%s: the binding was not stored", role)
		}
	}
}

func TestWithoutTheBotsMemberTheHierarchyIsUnknownAndTheBindingIsStillSaved(t *testing.T) {
	st := useStore(t, nil)
	fake := worldFake()
	fake.Guilds["g"].Members = nil
	if err := submit(t, fake, formID, "🎉", "1000000000000000003"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(replyText(t, fake), "highest role") || st.saves != 1 {
		t.Errorf("saves %d", st.saves)
	}
}

func TestAModeratorCannotBindARoleAtOrAboveTheirOwnHighestRole(t *testing.T) {
	// The moderator's highest role is "1000000000000000002" (position 5).
	mod := &discordgo.Member{User: &discordgo.User{ID: "mod"}, Roles: []string{"1000000000000000004", "1000000000000000002"}}
	for _, role := range []string{"1000000000000000005", "1000000000000000003"} { // equal to theirs, and above it
		st := useStore(t, nil)
		fake := worldFake()
		if err := submitAs(t, fake, mod, formID, "🎉", role); err != nil {
			t.Fatal(err)
		}
		got := replyText(t, fake)
		if !strings.Contains(got, "<@&"+role+">") || !strings.Contains(got, "your highest role") {
			t.Errorf("%s: reply %q", role, got)
		}
		if st.saves != 0 || slices.Contains(fake.Ops(), "AddReaction") {
			t.Errorf("%s: saves %d, ops %v; want nothing stored or reacted", role, st.saves, fake.Ops())
		}
	}
}

func TestAModeratorCanBindARoleBelowTheirOwnHighestRole(t *testing.T) {
	mod := &discordgo.Member{User: &discordgo.User{ID: "mod"}, Roles: []string{"1000000000000000002"}}
	st := useStore(t, nil)
	fake := worldFake()
	if err := submitAs(t, fake, mod, formID, "🎉", "1000000000000000001"); err != nil {
		t.Fatal(err)
	}
	if st.saves != 1 {
		t.Errorf("saves %d, reply %q", st.saves, replyText(t, fake))
	}
}

func TestTheServerOwnerCanBindAnyRole(t *testing.T) {
	owner := &discordgo.Member{User: &discordgo.User{ID: "owner"}}
	st := useStore(t, nil)
	fake := worldFake()
	if err := submitAs(t, fake, owner, formID, "🎉", "1000000000000000003"); err != nil {
		t.Fatal(err)
	}
	if st.saves != 1 {
		t.Errorf("saves %d, reply %q", st.saves, replyText(t, fake))
	}
}

func TestWhenTheOwnerCannotBeReadARoleAboveTheModeratorIsRefused(t *testing.T) {
	mod := &discordgo.Member{User: &discordgo.User{ID: "mod"}, Roles: []string{"1000000000000000004"}}
	st := useStore(t, nil)
	fake := worldFake()
	delete(fake.Guilds, "g")
	if err := submitAs(t, fake, mod, formID, "🎉", "1000000000000000003"); err != nil {
		t.Fatal(err)
	}
	if got := replyText(t, fake); !strings.Contains(got, "your highest role") || st.saves != 0 {
		t.Errorf("reply %q, saves %d", got, st.saves)
	}
}

func TestAPickedRoleThatNoLongerExistsIsRefused(t *testing.T) {
	st := useStore(t, nil)
	fake := worldFake()
	if err := submit(t, fake, formID, "🎉", "1000000000000000009"); err != nil {
		t.Fatal(err)
	}
	if got := replyText(t, fake); !strings.Contains(got, "no longer exists") || st.saves != 0 || slices.Contains(fake.Ops(), "AddReaction") {
		t.Errorf("reply %q, saves %d, ops %v", got, st.saves, fake.Ops())
	}
}

func TestAFormWithoutARoleOrWithAForeignCustomIDIsRefused(t *testing.T) {
	for name, tc := range map[string]struct {
		id    string
		roles []string
	}{
		"no role":        {formID, nil},
		"two roles":      {formID, []string{"1000000000000000001", "1000000000000000002"}},
		"no message":     {"reactrole:add:c1", []string{"1000000000000000001"}},
		"no channel":     {"reactrole:add::m1", []string{"1000000000000000001"}},
		"extra segments": {"reactrole:add:c1:m1:x", []string{"1000000000000000001"}},
	} {
		st := useStore(t, nil)
		fake := worldFake()
		if err := submit(t, fake, tc.id, "🎉", tc.roles...); err != nil {
			t.Fatal(err)
		}
		if replyText(t, fake) == "" || st.saves != 0 || slices.Contains(fake.Ops(), "AddReaction") {
			t.Errorf("%s: ops %v saves %d", name, fake.Ops(), st.saves)
		}
	}
}

func TestAStoreFailureOnSaveTakesTheNewReactionBackDown(t *testing.T) {
	st := useStore(t, nil)
	st.failSave = errors.New("db down")
	fake := worldFake()
	if err := submit(t, fake, formID, "🎉", "1000000000000000001"); err != nil {
		t.Fatal(err)
	}
	if got := replyText(t, fake); got != interaction.SomethingWentWrong {
		t.Errorf("reply %q", got)
	}
	if r := only(t, fake, "RemoveOwnReaction"); r.ChannelID != "c1" || r.MessageID != "m1" || r.Emoji != "🎉" {
		t.Errorf("rollback = %+v", r)
	}
}

func TestAStoreFailureOnSaveKeepsAReactionAnotherBindingStillNeeds(t *testing.T) {
	// 🎉 is bound to another role, so it is a duplicate and never reaches the save. A different emoji on the same
	// message is the case: its rollback removes only its own reaction.
	st := useStore(t, map[string]*guilds.ReactJoin{"m1": {ChannelID: "c1", RoleEmojiMap: []map[string][]string{{"1000000000000000002": {"🎉"}}}}})
	st.failSave = errors.New("db down")
	fake := worldFake()
	if err := submit(t, fake, formID, "🔥", "1000000000000000001"); err != nil {
		t.Fatal(err)
	}
	if r := only(t, fake, "RemoveOwnReaction"); r.Emoji != "🔥" {
		t.Errorf("rollback = %+v", r)
	}
}

func TestALoadFailureIsReportedWithoutReacting(t *testing.T) {
	st := useStore(t, nil)
	st.failGet = errors.New("db down")
	fake := worldFake()
	if err := submit(t, fake, formID, "🎉", "1000000000000000001"); err != nil {
		t.Fatal(err)
	}
	if got := replyText(t, fake); got != interaction.SomethingWentWrong || slices.Contains(fake.Ops(), "AddReaction") {
		t.Errorf("reply %q ops %v", got, fake.Ops())
	}
}

func TestEmojiParsingKeepsTodaysRules(t *testing.T) {
	for _, tc := range []struct {
		in     string
		stored string
		ok     bool
	}{
		{"🎉", "🎉", true},
		{"  🎉  ", "🎉", true},
		{"<:party:123>", "party:123", true},
		{"<a:dance:456>", "a:dance:456", true},
		{"<A:Dance:456>", "a:Dance:456", true}, // the animated marker is lower-cased, the name keeps its case
		{"<:Party_1:99>", "Party_1:99", true},
		{"party:123", "party:123", true}, // anything not in angle brackets goes to Discord as typed
		{"plain", "plain", true},
		{"", "", false},
		{"   ", "", false},
		{"<:broken", "", false},
		{"<:name:abc>", "", false}, // the ID must be digits
		{"<:bad name:123>", "", false},
		{"<>", "", false},
	} {
		stored, ok := emojiFromInput(tc.in)
		if stored != tc.stored || ok != tc.ok {
			t.Errorf("emojiFromInput(%q) = %q, %v; want %q, %v", tc.in, stored, ok, tc.stored, tc.ok)
		}
	}
}

func TestRemovingDeletesEveryBindingAndTheBotsReactionsForTheirEmojis(t *testing.T) {
	st := useStore(t, map[string]*guilds.ReactJoin{
		"m1": {ChannelID: "c1", RoleEmojiMap: []map[string][]string{{"1000000000000000001": {"🎉", "party:123"}}, {"1000000000000000002": {"🔥"}}}},
		"m2": {ChannelID: "c2", RoleEmojiMap: []map[string][]string{{"1000000000000000001": {"🎉"}}}},
	})
	fake := worldFake()
	if err := rightClick(t, fake, "Remove reaction roles", target()); err != nil {
		t.Fatal(err)
	}
	if _, still := st.entries["m1"]; still || st.entries["m2"] == nil {
		t.Errorf("entries = %v, want only m2 left", st.entries)
	}
	var removed []string
	for _, c := range fake.Calls() {
		if c.Op == "RemoveOwnReaction" {
			if c.ChannelID != "c1" || c.MessageID != "m1" {
				t.Errorf("reaction removed from %s/%s", c.ChannelID, c.MessageID)
			}
			removed = append(removed, c.Emoji)
		}
	}
	slices.Sort(removed)
	if !slices.Equal(removed, []string{"party:123", "🎉", "🔥"}) {
		t.Errorf("removed reactions = %v", removed)
	}
	got := replyText(t, fake)
	for _, want := range []string{"🎉", "<@&1000000000000000001>", "🔥", "<@&1000000000000000002>", "https://discord.com/channels/g/c1/m1"} {
		if !strings.Contains(got, want) {
			t.Errorf("reply %q lacks %q", got, want)
		}
	}
}

func TestRemovingFromALegacyRecordUsesTheRightClickedMessagesChannel(t *testing.T) {
	st := useStore(t, map[string]*guilds.ReactJoin{"m1": {RoleEmojiMap: []map[string][]string{{"1000000000000000001": {"🎉"}}}}})
	fake := worldFake()
	if err := rightClick(t, fake, "Remove reaction roles", target()); err != nil {
		t.Fatal(err)
	}
	if r := only(t, fake, "RemoveOwnReaction"); r.ChannelID != "c1" || r.MessageID != "m1" {
		t.Errorf("reaction = %+v", r)
	}
	if len(st.entries) != 0 {
		t.Errorf("entries = %v", st.entries)
	}
}

func TestRemovingFromAMessageWithoutBindingsSaysSoAndChangesNothing(t *testing.T) {
	for name, entries := range map[string]map[string]*guilds.ReactJoin{
		"no entry":    {"m2": {RoleEmojiMap: []map[string][]string{{"1000000000000000001": {"🎉"}}}}},
		"empty entry": {"m1": {ChannelID: "c1"}},
	} {
		st := useStore(t, entries)
		fake := worldFake()
		if err := rightClick(t, fake, "Remove reaction roles", target()); err != nil {
			t.Fatal(err)
		}
		if got := replyText(t, fake); !strings.Contains(got, "no reaction roles") {
			t.Errorf("%s: reply %q", name, got)
		}
		if slices.Contains(fake.Ops(), "RemoveOwnReaction") || len(st.entries) != len(entries) {
			t.Errorf("%s: ops %v entries %v", name, fake.Ops(), st.entries)
		}
	}
}

func TestAFailedDeleteLeavesTheBotsReactionsAlone(t *testing.T) {
	st := useStore(t, map[string]*guilds.ReactJoin{"m1": {ChannelID: "c1", RoleEmojiMap: []map[string][]string{{"1000000000000000001": {"🎉"}}}}})
	st.failDel = errors.New("db down")
	fake := worldFake()
	if err := rightClick(t, fake, "Remove reaction roles", target()); err != nil {
		t.Fatal(err)
	}
	if got := replyText(t, fake); got != interaction.SomethingWentWrong || slices.Contains(fake.Ops(), "RemoveOwnReaction") {
		t.Errorf("reply %q ops %v", got, fake.Ops())
	}
}

func TestReactionsThatCannotBeRemovedAreReportedAfterTheBindingsAreGone(t *testing.T) {
	st := useStore(t, map[string]*guilds.ReactJoin{"m1": {ChannelID: "c1", RoleEmojiMap: []map[string][]string{{"1000000000000000001": {"🎉"}}}}})
	fake := worldFake()
	fake.Fail = map[string]error{"RemoveOwnReaction": &discordgo.RESTError{Response: &http.Response{StatusCode: 403}}}
	if err := rightClick(t, fake, "Remove reaction roles", target()); err != nil {
		t.Fatal(err)
	}
	got := replyText(t, fake)
	if len(st.entries) != 0 || !strings.Contains(got, "<@&1000000000000000001>") || !strings.Contains(got, "<#c1>") || !strings.Contains(got, "Read Message History") {
		t.Errorf("reply %q entries %v", got, st.entries)
	}
}

func TestRemovingWithoutATargetIsRefusedPrivately(t *testing.T) {
	useStore(t, nil)
	fake := worldFake()
	if err := rightClick(t, fake, "Remove reaction roles", nil); err != nil {
		t.Fatal(err)
	}
	if replyText(t, fake) == "" {
		t.Error("no refusal")
	}
}
