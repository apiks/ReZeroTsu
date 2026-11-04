package settings

import (
	"context"
	"errors"
	"strings"
	"testing"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/database/guilds"
	"ReZeroTsu/internal/interaction"
	"ReZeroTsu/internal/interaction/interactiontest"

	"github.com/bwmarrin/discordgo"
)

// fakeStore is an in-memory guild settings store.
type fakeStore struct {
	gs      *guilds.GuildSettings
	failGet error
	failSet error
	sets    int
	// afterGet runs once after the settings are read, to stand for a write by another command in between.
	afterGet func()

	// autoposts is keyed by post type; deletedSentState lists the sent-state documents that were deleted, in order.
	autoposts        map[string]*guilds.Autopost
	deletedSentState []string
	failAutopost     error
	guildReads       int
}

// GetGuild is the one read behind a panel: the settings and the stored autoposts together.
func (f *fakeStore) GetGuild(_ context.Context, _ string) (*guilds.Guild, error) {
	f.guildReads++
	if f.failGet != nil {
		return nil, f.failGet
	}
	g := &guilds.Guild{}
	if f.gs != nil {
		c := *f.gs
		g.GuildSettings = &c
	}
	for _, postType := range []string{postTypeNewEpisodes, postTypeDailySchedule} {
		if ap := f.autoposts[postType]; ap != nil {
			g.Autoposts = append(g.Autoposts, *ap)
		}
	}
	if f.afterGet != nil {
		f.afterGet()
	}
	return g, nil
}

func (f *fakeStore) SetAutopost(_ context.Context, _, postType string, ap *guilds.Autopost) error {
	if f.failAutopost != nil {
		return f.failAutopost
	}
	if f.autoposts == nil {
		f.autoposts = map[string]*guilds.Autopost{}
	}
	if ap == nil || ap.ID == "" {
		delete(f.autoposts, postType)
		return nil
	}
	c := *ap
	f.autoposts[postType] = &c
	return nil
}

func (f *fakeStore) DeleteSentState(_ context.Context, id string) error {
	f.deletedSentState = append(f.deletedSentState, id)
	return nil
}

func (f *fakeStore) GetGuildSettings(_ context.Context, _ string) (*guilds.GuildSettings, error) {
	if f.failGet != nil {
		return nil, f.failGet
	}
	c := *f.gs
	if f.afterGet != nil {
		f.afterGet()
	}
	return &c, nil
}

func (f *fakeStore) SetBotLog(_ context.Context, _ string, ref *guilds.ChannelRef) error {
	return f.write(func(gs *guilds.GuildSettings) { gs.BotLogID = ref })
}

func (f *fakeStore) SetDonghua(_ context.Context, _ string, on bool) error {
	return f.write(func(gs *guilds.GuildSettings) { gs.Donghua = on })
}

func (f *fakeStore) SetPingMessage(_ context.Context, _ string, text string) error {
	return f.write(func(gs *guilds.GuildSettings) { gs.PingMessage = text })
}

// write applies one field to the stored settings, as the real targeted writes do: whatever else changed since
// the panel read stays.
func (f *fakeStore) write(set func(*guilds.GuildSettings)) error {
	if f.failSet != nil {
		return f.failSet
	}
	f.sets++
	c := *f.gs
	set(&c)
	f.gs = &c
	return nil
}

func useStore(t *testing.T, st *fakeStore) {
	t.Helper()
	old := panelStoreOf
	panelStoreOf = func(*database.Client) settingsStore { return st }
	t.Cleanup(func() { panelStoreOf = old })
}

func stored() *guilds.GuildSettings {
	return &guilds.GuildSettings{
		BotLogID:    &guilds.ChannelRef{Name: "logs", ID: "111"},
		Donghua:     true,
		PingMessage: "Hello there",
	}
}

func settingsCommand(t *testing.T) *commands.Command {
	t.Helper()
	for _, c := range commands.AllCommands() {
		if c.Name == "settings" {
			return c
		}
	}
	t.Fatal("/settings is not registered")
	return nil
}

// moderator and member are the permission bits of the interaction's member.
const (
	moderator = discordgo.PermissionManageGuild
	member    = 0
)

func newMember(perms int64) *discordgo.Member {
	return &discordgo.Member{User: &discordgo.User{ID: "user", Username: "mod"}, Permissions: perms}
}

// pressContext builds the context dispatch hands to a panel component: private, direct, on a private message.
func pressContext(fake *interactiontest.Fake, perms int64, data discordgo.InteractionData) *interaction.Context {
	i := &discordgo.Interaction{
		Type: discordgo.InteractionMessageComponent, GuildID: "g", ChannelID: "c", Member: newMember(perms), Data: data,
		Message: &discordgo.Message{Flags: discordgo.MessageFlagsEphemeral},
	}
	if _, ok := data.(discordgo.ModalSubmitInteractionData); ok {
		i.Type = discordgo.InteractionModalSubmit
	}
	return interaction.NewContext(fake, i, nil, interaction.Private, interaction.Direct)
}

func pick(t *testing.T, fake *interactiontest.Fake, perms int64, customID string, channels ...*discordgo.Channel) error {
	t.Helper()
	data := discordgo.MessageComponentInteractionData{CustomID: customID, ComponentType: discordgo.ChannelSelectMenuComponent}
	data.Resolved.Channels = map[string]*discordgo.Channel{}
	for _, ch := range channels {
		data.Values = append(data.Values, ch.ID)
		data.Resolved.Channels[ch.ID] = ch
	}
	return handlePanel(pressContext(fake, perms, data), nil)
}

func click(t *testing.T, fake *interactiontest.Fake, perms int64, customID string) error {
	t.Helper()
	return handlePanel(pressContext(fake, perms, discordgo.MessageComponentInteractionData{CustomID: customID, ComponentType: discordgo.ButtonComponent}), nil)
}

func submit(t *testing.T, fake *interactiontest.Fake, perms int64, text string) error {
	t.Helper()
	data := discordgo.ModalSubmitInteractionData{CustomID: idPingForm, Components: []discordgo.MessageComponent{
		&discordgo.Label{Component: discordgo.TextInput{CustomID: idPingText, Value: text}},
	}}
	return handlePanel(pressContext(fake, perms, data), nil)
}

// panelOf returns the embed description, the bot-log picker and the buttons of a panel response.
func panelOf(t *testing.T, r interaction.Response) (desc string, picker discordgo.SelectMenu, buttons []discordgo.Button) {
	t.Helper()
	if len(r.Embeds) != 1 {
		t.Fatalf("panel has %d embeds, want 1", len(r.Embeds))
	}
	desc = r.Embeds[0].Description
	if len(r.Components) != 5 {
		t.Fatalf("panel has %d rows, want 5", len(r.Components))
	}
	row1 := r.Components[0].(discordgo.ActionsRow)
	picker = row1.Components[0].(discordgo.SelectMenu)
	for _, c := range r.Components[4].(discordgo.ActionsRow).Components {
		buttons = append(buttons, c.(discordgo.Button))
	}
	return desc, picker, buttons
}

func lastCall(t *testing.T, fake *interactiontest.Fake) interactiontest.Call {
	t.Helper()
	calls := fake.Calls()
	if len(calls) == 0 {
		t.Fatal("nothing was sent")
	}
	return calls[len(calls)-1]
}

func TestSettingsCommandShape(t *testing.T) {
	c := settingsCommand(t)
	if c.Visibility != commands.Private || c.Response != commands.Deferred {
		t.Errorf("/settings is %v %v, want private and deferred", c.Visibility, c.Response)
	}
	if c.Permission != commands.PermMod {
		t.Error("/settings must be a mod command, which registers with Manage Server")
	}
	if c.Context != commands.ContextGuildOnly {
		t.Error("/settings must be guild only")
	}
}

func TestOldSettingCommandsAreGone(t *testing.T) {
	for _, c := range commands.AllCommands() {
		switch c.Name {
		case "bot-log", "donghua", "ping-message":
			t.Errorf("/%s is still registered", c.Name)
		}
	}
}

func TestSettingsShowsStoredValuesPrivately(t *testing.T) {
	useStore(t, &fakeStore{gs: stored()})
	fake := &interactiontest.Fake{}
	c := settingsCommand(t)
	ic := interactiontest.NewCommandContext(fake, c.Visibility, c.Response, discordgo.ApplicationCommandInteractionData{Name: "settings"})
	if err := ic.Defer(0); err != nil {
		t.Fatal(err)
	}
	if err := c.Run(ic); err != nil {
		t.Fatal(err)
	}
	if got := fake.Calls()[0]; got.Op != "Defer" || got.Visibility != interaction.Private {
		t.Errorf("acknowledgement = %s %v, want a private defer", got.Op, got.Visibility)
	}
	call := lastCall(t, fake)
	if call.Op != "EditReply" {
		t.Fatalf("last call = %s, want EditReply", call.Op)
	}
	desc, picker, buttons := panelOf(t, call.Response)
	for _, want := range []string{"<#111>", "Hello there", "/ping", "**Donghua in the schedule and new subscriptions:** Shown"} {
		if !strings.Contains(desc, want) {
			t.Errorf("panel %q is missing %q", desc, want)
		}
	}
	if picker.MenuType != discordgo.ChannelSelectMenu || picker.CustomID != idBotLog {
		t.Errorf("picker = %v %q", picker.MenuType, picker.CustomID)
	}
	if picker.MinValues == nil || *picker.MinValues != 0 || picker.MaxValues != 1 {
		t.Errorf("picker takes min %v max %d, want 0 and 1", picker.MinValues, picker.MaxValues)
	}
	if len(picker.ChannelTypes) != 1 || picker.ChannelTypes[0] != discordgo.ChannelTypeGuildText {
		t.Errorf("picker channel types = %v, want text only", picker.ChannelTypes)
	}
	if len(picker.DefaultValues) != 1 || picker.DefaultValues[0].ID != "111" || picker.DefaultValues[0].Type != discordgo.SelectMenuDefaultValueChannel {
		t.Errorf("picker defaults = %v, want the stored bot-log channel", picker.DefaultValues)
	}
	if len(buttons) != 4 || !strings.Contains(buttons[0].Label, "On") || buttons[2].Label != "Edit ping message" {
		t.Errorf("buttons = %+v", buttons)
	}
}

func TestSettingsWithoutBotLogHasNoDefaultValue(t *testing.T) {
	gs := stored()
	gs.BotLogID, gs.Donghua = nil, false
	useStore(t, &fakeStore{gs: gs})
	fake := &interactiontest.Fake{}
	c := settingsCommand(t)
	ic := interactiontest.NewCommandContext(fake, c.Visibility, c.Response, discordgo.ApplicationCommandInteractionData{Name: "settings"})
	if err := c.Run(ic); err != nil {
		t.Fatal(err)
	}
	_, picker, buttons := panelOf(t, lastCall(t, fake).Response)
	if len(picker.DefaultValues) != 0 {
		t.Errorf("picker defaults = %v, want none", picker.DefaultValues)
	}
	if !strings.Contains(buttons[0].Label, "Off") {
		t.Errorf("donghua button = %q, want its off state", buttons[0].Label)
	}
}

func TestSettingsLoadFailureIsRefusedPrivately(t *testing.T) {
	useStore(t, &fakeStore{gs: stored(), failGet: errors.New("db down")})
	fake := &interactiontest.Fake{}
	c := settingsCommand(t)
	ic := interactiontest.NewCommandContext(fake, c.Visibility, c.Response, discordgo.ApplicationCommandInteractionData{Name: "settings"})
	_ = ic.Defer(0)
	if err := c.Run(ic); err != nil {
		t.Fatal(err)
	}
	if call := lastCall(t, fake); call.Op != "EditReply" || !strings.Contains(call.Response.Content, "load") {
		t.Errorf("last call = %s %q, want a load failure message", call.Op, call.Response.Content)
	}
}

func TestChoosingBotLogChannelChangesOnlyThatSetting(t *testing.T) {
	st := &fakeStore{gs: stored()}
	useStore(t, st)
	fake := &interactiontest.Fake{}
	if err := pick(t, fake, moderator, idBotLog, &discordgo.Channel{ID: "222", Name: "new-logs", Type: discordgo.ChannelTypeGuildText}); err != nil {
		t.Fatal(err)
	}
	want := stored()
	want.BotLogID = &guilds.ChannelRef{Name: "new-logs", ID: "222"}
	if st.gs.BotLogID == nil || *st.gs.BotLogID != *want.BotLogID {
		t.Errorf("bot log = %v, want %v", st.gs.BotLogID, want.BotLogID)
	}
	if st.gs.Donghua != want.Donghua || st.gs.PingMessage != want.PingMessage {
		t.Errorf("other settings changed: %+v", st.gs)
	}
	call := lastCall(t, fake)
	if call.Op != "UpdateMessage" {
		t.Fatalf("last call = %s, want UpdateMessage", call.Op)
	}
	desc, picker, _ := panelOf(t, call.Response)
	if !strings.Contains(desc, "<#222>") || picker.DefaultValues[0].ID != "222" {
		t.Errorf("panel does not show the new channel: %q %v", desc, picker.DefaultValues)
	}
}

func TestClearingBotLogPickerTurnsItOff(t *testing.T) {
	st := &fakeStore{gs: stored()}
	useStore(t, st)
	fake := &interactiontest.Fake{}
	if err := pick(t, fake, moderator, idBotLog); err != nil {
		t.Fatal(err)
	}
	if st.gs.BotLogID != nil {
		t.Errorf("bot log = %v, want cleared", st.gs.BotLogID)
	}
	if !st.gs.Donghua || st.gs.PingMessage != "Hello there" {
		t.Errorf("other settings changed: %+v", st.gs)
	}
	_, picker, _ := panelOf(t, lastCall(t, fake).Response)
	if len(picker.DefaultValues) != 0 {
		t.Errorf("picker defaults = %v, want none after clearing", picker.DefaultValues)
	}
}

func TestChoiceOfUnresolvedChannelIsRefused(t *testing.T) {
	st := &fakeStore{gs: stored()}
	useStore(t, st)
	fake := &interactiontest.Fake{}
	data := discordgo.MessageComponentInteractionData{CustomID: idBotLog, Values: []string{"999"}}
	if err := handlePanel(pressContext(fake, moderator, data), nil); err != nil {
		t.Fatal(err)
	}
	if st.sets != 0 {
		t.Error("an unresolved channel was saved")
	}
	if call := lastCall(t, fake); call.Op != "FollowUp" || !strings.Contains(call.Response.Content, "not found") || call.Visibility != interaction.Private {
		t.Errorf("last call = %s %q %v, want a private not-found message", call.Op, call.Response.Content, call.Visibility)
	}
}

func TestDonghuaToggleFlipsOnlyDonghua(t *testing.T) {
	st := &fakeStore{gs: stored()}
	useStore(t, st)
	fake := &interactiontest.Fake{}
	if err := click(t, fake, moderator, idDonghua); err != nil {
		t.Fatal(err)
	}
	if st.gs.Donghua {
		t.Error("donghua still on")
	}
	if st.gs.BotLogID == nil || st.gs.PingMessage != "Hello there" {
		t.Errorf("other settings changed: %+v", st.gs)
	}
	_, _, buttons := panelOf(t, lastCall(t, fake).Response)
	if !strings.Contains(buttons[0].Label, "Off") {
		t.Errorf("button = %q, want the off state", buttons[0].Label)
	}
	if err := click(t, fake, moderator, idDonghua); err != nil {
		t.Fatal(err)
	}
	if !st.gs.Donghua {
		t.Error("second press did not turn donghua back on")
	}
}

func TestEditPingMessageOpensPrefilledForm(t *testing.T) {
	useStore(t, &fakeStore{gs: stored()})
	fake := &interactiontest.Fake{}
	if err := click(t, fake, moderator, idPingEdit); err != nil {
		t.Fatal(err)
	}
	call := lastCall(t, fake)
	if call.Op != "OpenForm" || call.Form.CustomID != idPingForm {
		t.Fatalf("last call = %s %q, want the ping form", call.Op, call.Form.CustomID)
	}
	label, ok := call.Form.Components[0].(*discordgo.Label)
	if !ok || label.Label != "Ping message (shown by /ping)" {
		t.Fatalf("form field = %#v, want a Label reading %q", call.Form.Components[0], "Ping message (shown by /ping)")
	}
	box, ok := label.Component.(discordgo.TextInput)
	if !ok || box.CustomID != idPingText || box.Style != discordgo.TextInputParagraph || box.Value != "Hello there" {
		t.Errorf("text box = %+v, want a paragraph prefilled with the current text", label.Component)
	}
}

func TestEditPingMessageRefusesStoredTextTooLongForTheForm(t *testing.T) {
	gs := stored()
	gs.PingMessage = strings.Repeat("x", maxPingMessageLength+1)
	useStore(t, &fakeStore{gs: gs})
	fake := &interactiontest.Fake{}
	if err := click(t, fake, moderator, idPingEdit); err != nil {
		t.Fatal(err)
	}
	call := lastCall(t, fake)
	if call.Op == "OpenForm" {
		t.Fatal("a form that Discord would reject was opened")
	}
	if call.Visibility != interaction.Private || !strings.Contains(call.Response.Content, "over 4000 characters") {
		t.Errorf("last call = %s %q %v, want a private refusal naming the limit", call.Op, call.Response.Content, call.Visibility)
	}
}

func TestSavingPingMessageChangesOnlyIt(t *testing.T) {
	st := &fakeStore{gs: stored()}
	useStore(t, st)
	fake := &interactiontest.Fake{}
	if err := submit(t, fake, moderator, "Back off"); err != nil {
		t.Fatal(err)
	}
	if st.gs.PingMessage != "Back off" {
		t.Errorf("ping message = %q", st.gs.PingMessage)
	}
	if !st.gs.Donghua || st.gs.BotLogID == nil {
		t.Errorf("other settings changed: %+v", st.gs)
	}
	call := lastCall(t, fake)
	if call.Op != "UpdateMessage" {
		t.Fatalf("last call = %s, want UpdateMessage", call.Op)
	}
	if desc, _, _ := panelOf(t, call.Response); !strings.Contains(desc, "Back off") {
		t.Errorf("panel %q does not show the new message", desc)
	}
}

func TestBlankPingMessageIsRefused(t *testing.T) {
	st := &fakeStore{gs: stored()}
	useStore(t, st)
	fake := &interactiontest.Fake{}
	if err := submit(t, fake, moderator, "  \n "); err != nil {
		t.Fatal(err)
	}
	if st.sets != 0 {
		t.Error("a blank message was saved")
	}
	if call := lastCall(t, fake); call.Visibility != interaction.Private {
		t.Errorf("refusal visibility = %v, want private", call.Visibility)
	}
}

// Discord gates /settings by its default member permissions and server overrides, so a press, choice or
// submission is not re-checked by the bot: no guild fetch, no refusal, whatever the member's permissions. Each
// press also has its effect, so a press that silently does nothing fails here.
func TestPanelComponentsAreNotRecheckedByTheBot(t *testing.T) {
	ch := &discordgo.Channel{ID: "222", Name: "x", Type: discordgo.ChannelTypeGuildText}
	cases := map[string]struct {
		act   func(*interactiontest.Fake) error
		saved func(*guilds.GuildSettings) bool
	}{
		"picker": {func(f *interactiontest.Fake) error { return pick(t, f, member, idBotLog, ch) },
			func(gs *guilds.GuildSettings) bool { return gs.BotLogID != nil && gs.BotLogID.ID == "222" }},
		"toggle": {func(f *interactiontest.Fake) error { return click(t, f, member, idDonghua) },
			func(gs *guilds.GuildSettings) bool { return !gs.Donghua }},
		"edit": {func(f *interactiontest.Fake) error { return click(t, f, member, idPingEdit) },
			func(gs *guilds.GuildSettings) bool { return true }}, // opens the form; nothing is saved yet
		"form": {func(f *interactiontest.Fake) error { return submit(t, f, member, "new text") },
			func(gs *guilds.GuildSettings) bool { return gs.PingMessage == "new text" }},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			st := &fakeStore{gs: stored()}
			useStore(t, st)
			fake := &interactiontest.Fake{StallGuild: true}
			if err := tc.act(fake); err != nil {
				t.Fatal(err)
			}
			for _, c := range fake.Calls() {
				if c.Op == "Guild" || c.Op == "Reply" || c.Op == "FollowUp" {
					t.Errorf("calls = %v, want no permission lookup and no refusal", fake.Ops())
				}
			}
			if !tc.saved(st.gs) {
				t.Errorf("settings after the press = %+v, want the change applied", st.gs)
			}
		})
	}
}

func TestFailedSaveKeepsPanelOnStoredValues(t *testing.T) {
	ch := &discordgo.Channel{ID: "222", Name: "new-logs", Type: discordgo.ChannelTypeGuildText}
	cases := map[string]func(*interactiontest.Fake) error{
		"picker": func(f *interactiontest.Fake) error { return pick(t, f, moderator, idBotLog, ch) },
		"toggle": func(f *interactiontest.Fake) error { return click(t, f, moderator, idDonghua) },
		"form":   func(f *interactiontest.Fake) error { return submit(t, f, moderator, "Back off") },
	}
	for name, act := range cases {
		t.Run(name, func(t *testing.T) {
			st := &fakeStore{gs: stored(), failSet: errors.New("db down")}
			useStore(t, st)
			fake := &interactiontest.Fake{}
			if err := act(fake); err != nil {
				t.Fatal(err)
			}
			calls := fake.Calls()
			if len(calls) != 2 || calls[0].Op != "UpdateMessage" || calls[1].Op != "FollowUp" {
				t.Fatalf("calls = %v, want the stored panel then a private follow-up", fake.Ops())
			}
			desc, picker, buttons := panelOf(t, calls[0].Response)
			if !strings.Contains(desc, "<#111>") || !strings.Contains(desc, "Hello there") || picker.DefaultValues[0].ID != "111" || !strings.Contains(buttons[0].Label, "On") {
				t.Errorf("panel does not show the stored values: %q", desc)
			}
			if calls[1].Visibility != interaction.Private || !strings.Contains(calls[1].Response.Content, "save") {
				t.Errorf("follow-up = %v %q, want a private save failure", calls[1].Visibility, calls[1].Response.Content)
			}
		})
	}
}

func TestFailedLoadOnPressChangesNothing(t *testing.T) {
	st := &fakeStore{gs: stored(), failGet: errors.New("db down")}
	useStore(t, st)
	fake := &interactiontest.Fake{Guilds: map[string]*discordgo.Guild{"g": {ID: "g"}}}
	if err := click(t, fake, moderator, idDonghua); err != nil {
		t.Fatal(err)
	}
	if st.sets != 0 {
		t.Error("a setting was saved without reading the stored ones")
	}
	if call := lastCall(t, fake); call.Visibility != interaction.Private || !strings.Contains(call.Response.Content, "load") {
		t.Errorf("last call = %v %q, want a private load failure", call.Visibility, call.Response.Content)
	}
}

func TestUnknownPanelComponentIsRefused(t *testing.T) {
	st := &fakeStore{gs: stored()}
	useStore(t, st)
	fake := &interactiontest.Fake{}
	if err := click(t, fake, moderator, "settings:nope"); err != nil {
		t.Fatal(err)
	}
	if st.sets != 0 || lastCall(t, fake).Visibility != interaction.Private {
		t.Error("an unknown component must be refused privately without saving")
	}
}

func TestPanelSaveKeepsAWriteMadeAfterItsRead(t *testing.T) {
	for name, id := range map[string]string{"donghua": idDonghua} {
		t.Run(name, func(t *testing.T) {
			st := &fakeStore{gs: stored()}
			st.afterGet = func() {
				st.afterGet = nil
				gs := *st.gs
				gs.PingMessage = "written elsewhere"
				st.gs = &gs
			}
			useStore(t, st)
			if err := click(t, &interactiontest.Fake{}, moderator, id); err != nil {
				t.Fatal(err)
			}
			if st.gs.PingMessage != "written elsewhere" {
				t.Errorf("a ping message written during the press was lost: %+v", st.gs)
			}
		})
	}
}

func TestAPanelPressReadsTheGuildOncePerRender(t *testing.T) {
	st := &fakeStore{gs: stored()}
	useStore(t, st)
	if err := click(t, &interactiontest.Fake{}, moderator, idDonghua); err != nil {
		t.Fatal(err)
	}
	// One read before the change and one for the fresh panel.
	if st.guildReads != 2 {
		t.Errorf("guild reads = %d, want 2", st.guildReads)
	}
}
