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

	// autoposts is keyed by post type; deletedSubs lists the sent-state documents that were deleted, in order.
	autoposts    map[string]*guilds.Autopost
	deletedSubs  []string
	failAutopost error
}

func (f *fakeStore) GetAutopostByType(_ context.Context, _, postType string) (*guilds.Autopost, error) {
	if f.failGet != nil {
		return nil, f.failGet
	}
	if ap := f.autoposts[postType]; ap != nil {
		c := *ap
		return &c, nil
	}
	return nil, nil
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

func (f *fakeStore) DeleteAnimeSubs(_ context.Context, id string) error {
	f.deletedSubs = append(f.deletedSubs, id)
	return nil
}

func (f *fakeStore) GetGuildSettings(_ context.Context, _ string) (*guilds.GuildSettings, error) {
	if f.failGet != nil {
		return nil, f.failGet
	}
	c := *f.gs
	return &c, nil
}

func (f *fakeStore) SetGuildSettings(_ context.Context, _ string, gs *guilds.GuildSettings) error {
	if f.failSet != nil {
		return f.failSet
	}
	f.sets++
	c := *gs
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
		BotLogID:     &guilds.ChannelRef{Name: "logs", ID: "111"},
		Donghua:      true,
		PingMessage:  "Hello there",
		ModOnly:      true,
		ReactsModule: true,
		CommandRoles: []guilds.Role{{ID: "r1"}},
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
		t.Error("/settings must keep the bot-side moderator check")
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
	for _, want := range []string{"<#111>", "Hello there", "/ping"} {
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
	if len(buttons) != 2 || !strings.Contains(buttons[0].Label, "On") || buttons[1].Label != "Edit ping message" {
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
	if st.gs.Donghua != want.Donghua || st.gs.PingMessage != want.PingMessage || st.gs.ModOnly != want.ModOnly || len(st.gs.CommandRoles) != 1 {
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

func TestNonModeratorsAreRefusedOnEveryComponent(t *testing.T) {
	ch := &discordgo.Channel{ID: "222", Name: "x", Type: discordgo.ChannelTypeGuildText}
	actions := map[string]func(*interactiontest.Fake) error{
		"picker": func(f *interactiontest.Fake) error { return pick(t, f, member, idBotLog, ch) },
		"clear":  func(f *interactiontest.Fake) error { return pick(t, f, member, idBotLog) },
		"toggle": func(f *interactiontest.Fake) error { return click(t, f, member, idDonghua) },
		"edit":   func(f *interactiontest.Fake) error { return click(t, f, member, idPingEdit) },
		"form":   func(f *interactiontest.Fake) error { return submit(t, f, member, "hijacked") },
	}
	for name, act := range actions {
		t.Run(name, func(t *testing.T) {
			st := &fakeStore{gs: stored()}
			useStore(t, st)
			fake := &interactiontest.Fake{Guilds: map[string]*discordgo.Guild{"g": {ID: "g", OwnerID: "someone-else"}}}
			if err := act(fake); err != nil {
				t.Fatal(err)
			}
			if st.sets != 0 {
				t.Error("a non-moderator changed a setting")
			}
			calls := fake.Calls()
			last := calls[len(calls)-1]
			if last.Op != "Reply" || last.Visibility != interaction.Private || !strings.Contains(last.Response.Content, "You need") {
				t.Errorf("last call = %s %v %q, want a private refusal", last.Op, last.Visibility, last.Response.Content)
			}
			for _, c := range calls {
				if c.Op == "OpenForm" || c.Op == "UpdateMessage" {
					t.Errorf("a non-moderator got %s", c.Op)
				}
			}
		})
	}
}

func TestModeratorRoleStillPassesTheRecheck(t *testing.T) {
	gs := stored()
	st := &fakeStore{gs: gs}
	useStore(t, st)
	fake := &interactiontest.Fake{Guilds: map[string]*discordgo.Guild{"g": {ID: "g", OwnerID: "someone-else"}}}
	data := discordgo.MessageComponentInteractionData{CustomID: idDonghua}
	ic := pressContext(fake, member, data)
	ic.Interaction.Member.Roles = []string{"r1"}
	if err := handlePanel(ic, nil); err != nil {
		t.Fatal(err)
	}
	if st.gs.Donghua {
		t.Error("a member with a moderator role was refused")
	}
}

func TestStalledModeratorCheckFailsClosed(t *testing.T) {
	st := &fakeStore{gs: stored()}
	useStore(t, st)
	fake := &interactiontest.Fake{StallGuild: true}
	if err := click(t, fake, member, idDonghua); err != nil {
		t.Fatal(err)
	}
	if st.sets != 0 {
		t.Error("a check that did not finish let the change through")
	}
	if call := lastCall(t, fake); call.Op != "Reply" || call.Visibility != interaction.Private || !strings.Contains(call.Response.Content, "in time") {
		t.Errorf("last call = %s %q, want the private timeout message", call.Op, call.Response.Content)
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
	// The moderator check needs no stored roles for a Manage Server member, so the load failure is the settings read.
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
