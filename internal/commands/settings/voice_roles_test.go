package settings

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/database/guilds"
	"ReZeroTsu/internal/interaction"
	"ReZeroTsu/internal/interaction/interactiontest"

	"github.com/bwmarrin/discordgo"
)

type fakeVoiceStore struct {
	settings *guilds.GuildSettings
	getErr   error
	setErr   error
	saves    int
}

func (f *fakeVoiceStore) GetGuildSettings(context.Context, string) (*guilds.GuildSettings, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	// A copy, like a database read.
	cp := &guilds.GuildSettings{}
	for _, vc := range f.settings.VoiceChas {
		vc.Roles = append([]guilds.Role(nil), vc.Roles...)
		cp.VoiceChas = append(cp.VoiceChas, vc)
	}
	return cp, nil
}

func (f *fakeVoiceStore) SetVoiceChas(_ context.Context, _ string, chas []guilds.VoiceCha) error {
	if f.setErr != nil {
		return f.setErr
	}
	f.saves++
	f.settings = &guilds.GuildSettings{VoiceChas: chas}
	return nil
}

func useVoice(t *testing.T, chs ...guilds.VoiceCha) *fakeVoiceStore {
	t.Helper()
	st := &fakeVoiceStore{settings: &guilds.GuildSettings{VoiceChas: chs}}
	old := voiceStoreOf
	voiceStoreOf = func(*database.Client) voiceStore { return st }
	t.Cleanup(func() { voiceStoreOf = old })
	return st
}

func voiceCommand(t *testing.T) *commands.Command {
	t.Helper()
	for _, c := range commands.AllCommands() {
		if c.Name == "voicerole" {
			return c
		}
	}
	t.Fatal("/voicerole is not registered")
	return nil
}

func voiceSub(t *testing.T, name string) *commands.Subcommand {
	t.Helper()
	for _, s := range voiceCommand(t).Subcommands {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("/voicerole has no %q subcommand", name)
	return nil
}

func strOpt(name, v string, typ discordgo.ApplicationCommandOptionType) *discordgo.ApplicationCommandInteractionDataOption {
	return &discordgo.ApplicationCommandInteractionDataOption{Name: name, Type: typ, Value: v}
}

func chanOpt(id string) *discordgo.ApplicationCommandInteractionDataOption {
	return strOpt("channel", id, discordgo.ApplicationCommandOptionChannel)
}

func roleOpt(id string) *discordgo.ApplicationCommandInteractionDataOption {
	return strOpt("role", id, discordgo.ApplicationCommandOptionRole)
}

func resolved(chType discordgo.ChannelType) *discordgo.ApplicationCommandInteractionDataResolved {
	return &discordgo.ApplicationCommandInteractionDataResolved{
		Channels: map[string]*discordgo.Channel{"v1": {ID: "v1", Name: "Lounge", Type: chType}},
		Roles:    map[string]*discordgo.Role{"r1": {ID: "r1", Name: "Talkers", Position: 3}},
	}
}

// voiceTopRole sits above every role in these tests; the default invoker holds it, so their own hierarchy never gets in the way.
const voiceTopRole = "top"

// voiceServer is a server owned by "owner" where r1 is at position 3 and "mid" at position 3.
func voiceServer() *interactiontest.Fake {
	return &interactiontest.Fake{
		Guilds:   map[string]*discordgo.Guild{"g": {ID: "g", OwnerID: "owner"}},
		RoleSets: map[string][]*discordgo.Role{"g": {{ID: "r1", Position: 3}, {ID: "mid", Position: 3}, {ID: "low", Position: 1}, {ID: voiceTopRole, Position: 20}}},
	}
}

func runVoice(t *testing.T, name string, res *discordgo.ApplicationCommandInteractionDataResolved, opts ...*discordgo.ApplicationCommandInteractionDataOption) *interactiontest.Fake {
	t.Helper()
	return runVoiceAs(t, voiceServer(), "user", []string{voiceTopRole}, name, res, opts...)
}

func runVoiceAs(t *testing.T, fake *interactiontest.Fake, user string, roles []string, name string, res *discordgo.ApplicationCommandInteractionDataResolved, opts ...*discordgo.ApplicationCommandInteractionDataOption) *interactiontest.Fake {
	t.Helper()
	cmd := voiceCommand(t)
	ic := interactiontest.NewCommandContext(fake, cmd.Visibility, cmd.Response, discordgo.ApplicationCommandInteractionData{Name: "voicerole", Options: opts, Resolved: res})
	ic.Member.User.ID, ic.Member.Roles = user, roles
	ic.Invoker = ic.Member.User
	if err := ic.Defer(0); err != nil {
		t.Fatal(err)
	}
	if err := voiceSub(t, name).Run(ic); err != nil {
		t.Fatal(err)
	}
	return fake
}

func lastOf(t *testing.T, fake *interactiontest.Fake) interactiontest.Call {
	t.Helper()
	calls := fake.Calls()
	if calls[0].Op != "Defer" || calls[0].Visibility != interaction.Private {
		t.Fatalf("first call = %+v, want a private Defer", calls[0])
	}
	return calls[len(calls)-1]
}

func textOf(r interaction.Response) string {
	var b strings.Builder
	b.WriteString(r.Content)
	for _, e := range r.Embeds {
		b.WriteString(e.Title + " " + e.Description + " ")
		if e.Footer != nil {
			b.WriteString(e.Footer.Text)
		}
	}
	return b.String()
}

func TestVoiceRoleIsAPrivateGuildOnlyModCommandAndTheOldOnesAreGone(t *testing.T) {
	c := voiceCommand(t)
	if c.Visibility != commands.Private || c.Permission != commands.PermMod || c.Context != commands.ContextGuildOnly {
		t.Errorf("command = %+v", c)
	}
	for _, n := range []string{"add", "remove", "list"} {
		voiceSub(t, n)
	}
	if len(voiceSub(t, "add").Options) != 2 || len(voiceSub(t, "remove").Options) != 2 || voiceSub(t, "remove").Options[1].Required {
		t.Error("add and remove keep their channel and role options, the role optional on remove")
	}
	for _, d := range commands.Definitions() {
		if d.Name == "add-voice" || d.Name == "remove-voice" || d.Name == "voice-roles" {
			t.Errorf("old command %q is still registered", d.Name)
		}
	}
}

func TestAddStoresANewChannelAndMergesARoleIntoAnExistingOne(t *testing.T) {
	st := useVoice(t)
	fake := runVoice(t, "add", resolved(discordgo.ChannelTypeGuildVoice), chanOpt("v1"), roleOpt("r1"))
	got := textOf(lastOf(t, fake).Response)
	if !strings.Contains(got, "<#v1>") || !strings.Contains(got, "<@&r1>") {
		t.Errorf("reply %q lacks mentions", got)
	}
	if len(st.settings.VoiceChas) != 1 || st.settings.VoiceChas[0].Name != "Lounge" || st.settings.VoiceChas[0].Roles[0].Position != 3 {
		t.Fatalf("stored = %+v", st.settings.VoiceChas)
	}

	st = useVoice(t, guilds.VoiceCha{ID: "v1", Name: "Lounge", Roles: []guilds.Role{{ID: "r9", Name: "Old"}}})
	runVoice(t, "add", resolved(discordgo.ChannelTypeGuildVoice), chanOpt("v1"), roleOpt("r1"))
	if roles := st.settings.VoiceChas[0].Roles; len(st.settings.VoiceChas) != 1 || len(roles) != 2 || roles[1].ID != "r1" {
		t.Errorf("stored = %+v", st.settings.VoiceChas)
	}
}

func TestAddRefusalsStorePrivatelyNothing(t *testing.T) {
	cases := []struct {
		name string
		res  *discordgo.ApplicationCommandInteractionDataResolved
		opts []*discordgo.ApplicationCommandInteractionDataOption
		want string
		have []guilds.VoiceCha
	}{
		{"missing role", resolved(discordgo.ChannelTypeGuildVoice), []*discordgo.ApplicationCommandInteractionDataOption{chanOpt("v1")}, "both channel and role", nil},
		{"unresolved", nil, []*discordgo.ApplicationCommandInteractionDataOption{chanOpt("v1"), roleOpt("r1")}, "not found", nil},
		{"text channel", resolved(discordgo.ChannelTypeGuildText), []*discordgo.ApplicationCommandInteractionDataOption{chanOpt("v1"), roleOpt("r1")}, "not a voice channel", nil},
		{"duplicate", resolved(discordgo.ChannelTypeGuildVoice), []*discordgo.ApplicationCommandInteractionDataOption{chanOpt("v1"), roleOpt("r1")}, "already set",
			[]guilds.VoiceCha{{ID: "v1", Roles: []guilds.Role{{ID: "r1"}}}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := useVoice(t, c.have...)
			fake := runVoice(t, "add", c.res, c.opts...)
			last := lastOf(t, fake)
			if st.saves != 0 || !strings.Contains(textOf(last.Response), c.want) {
				t.Errorf("saves %d, reply %q, want %q", st.saves, textOf(last.Response), c.want)
			}
		})
	}
}

func TestAddRefusesARoleAtOrAboveTheModeratorsHighestRole(t *testing.T) {
	for _, mine := range []string{"mid", "low"} { // r1 is equal to "mid" and above "low"
		st := useVoice(t)
		fake := runVoiceAs(t, voiceServer(), "mod", []string{mine}, "add", resolved(discordgo.ChannelTypeGuildVoice), chanOpt("v1"), roleOpt("r1"))
		got := textOf(lastOf(t, fake).Response)
		if st.saves != 0 || !strings.Contains(got, "<@&r1>") || !strings.Contains(got, "your highest role") {
			t.Errorf("holding %s: saves %d, reply %q", mine, st.saves, got)
		}
	}
}

func TestAddLetsTheServerOwnerSetAnyRole(t *testing.T) {
	st := useVoice(t)
	runVoiceAs(t, voiceServer(), "owner", nil, "add", resolved(discordgo.ChannelTypeGuildVoice), chanOpt("v1"), roleOpt("r1"))
	if st.saves != 1 {
		t.Errorf("saves %d, want the owner's role stored", st.saves)
	}
}

func TestAddRefusesWhenTheOwnerCannotBeRead(t *testing.T) {
	st := useVoice(t)
	fake := voiceServer()
	fake.Guilds = nil
	runVoiceAs(t, fake, "mod", []string{"low"}, "add", resolved(discordgo.ChannelTypeGuildVoice), chanOpt("v1"), roleOpt("r1"))
	if st.saves != 0 {
		t.Errorf("saves %d, want nothing stored", st.saves)
	}
}

func TestAddReportsLoadAndSaveFailures(t *testing.T) {
	st := useVoice(t)
	st.getErr = errors.New("db down")
	r := lastOf(t, runVoice(t, "add", resolved(discordgo.ChannelTypeGuildVoice), chanOpt("v1"), roleOpt("r1"))).Response
	if r.Content != interaction.SomethingWentWrong {
		t.Errorf("load failure reply = %+v", r)
	}
	st.getErr, st.setErr = nil, errors.New("db down")
	r = lastOf(t, runVoice(t, "add", resolved(discordgo.ChannelTypeGuildVoice), chanOpt("v1"), roleOpt("r1"))).Response
	if r.Content != interaction.SomethingWentWrong {
		t.Errorf("save failure reply = %+v", r)
	}
}

func TestRemoveOneRoleDropsAnEmptiedChannelAndNoRoleDropsTheChannel(t *testing.T) {
	st := useVoice(t,
		guilds.VoiceCha{ID: "v1", Roles: []guilds.Role{{ID: "r1"}, {ID: "r2"}}},
		guilds.VoiceCha{ID: "v2", Roles: []guilds.Role{{ID: "r1"}}},
	)
	runVoice(t, "remove", nil, chanOpt("v1"), roleOpt("r1"))
	if len(st.settings.VoiceChas[0].Roles) != 1 || st.settings.VoiceChas[0].Roles[0].ID != "r2" {
		t.Fatalf("stored = %+v", st.settings.VoiceChas)
	}
	runVoice(t, "remove", nil, chanOpt("v2"), roleOpt("r1"))
	if len(st.settings.VoiceChas) != 1 {
		t.Fatalf("emptied channel kept: %+v", st.settings.VoiceChas)
	}
	r := lastOf(t, runVoice(t, "remove", nil, chanOpt("v1"))).Response
	if len(st.settings.VoiceChas) != 0 || !strings.Contains(textOf(r), "All roles removed from <#v1>") {
		t.Errorf("stored %+v, reply %q", st.settings.VoiceChas, textOf(r))
	}
}

func TestRemoveRefusals(t *testing.T) {
	st := useVoice(t, guilds.VoiceCha{ID: "v1", Roles: []guilds.Role{{ID: "r1"}}})
	r := lastOf(t, runVoice(t, "remove", nil, chanOpt("v9")))
	if !strings.Contains(textOf(r.Response), "<#v9> is not in the voice roles list") {
		t.Errorf("reply %q", textOf(r.Response))
	}
	r = lastOf(t, runVoice(t, "remove", nil, chanOpt("v1"), roleOpt("r9")))
	if !strings.Contains(textOf(r.Response), "not set for this channel") || st.saves != 0 {
		t.Errorf("reply %q, saves %d", textOf(r.Response), st.saves)
	}
	r = lastOf(t, runVoice(t, "remove", nil))
	if !strings.Contains(textOf(r.Response), "provide a channel") {
		t.Errorf("reply %q", textOf(r.Response))
	}
}

func TestListShowsMentionsPrivatelyWithAPickerPerPair(t *testing.T) {
	useVoice(t, guilds.VoiceCha{ID: "v1", Name: "Lounge", Roles: []guilds.Role{{ID: "r1", Name: "A"}, {ID: "r2", Name: "B"}}},
		guilds.VoiceCha{ID: "v2", Name: "Empty"})
	r := lastOf(t, runVoice(t, "list", nil)).Response
	body := textOf(r)
	for _, want := range []string{"<#v1> gives <@&r1>", "<#v1> gives <@&r2>", "<#v2>: no roles"} {
		if !strings.Contains(body, want) {
			t.Errorf("list %q lacks %q", body, want)
		}
	}
	picker, ok := pickerIn(r)
	if !ok || len(picker.Options) != 2 || picker.Options[0].Value != "v1:r1" || picker.Options[1].Label != "Lounge: B" {
		t.Errorf("picker = %+v", picker)
	}
	if !strings.HasPrefix(picker.CustomID, voicePickerPrefix) || len(picker.CustomID) >= 100 {
		t.Errorf("custom ID %q", picker.CustomID)
	}
}

func pickerIn(r interaction.Response) (discordgo.SelectMenu, bool) {
	for _, row := range r.Components {
		for _, c := range row.(discordgo.ActionsRow).Components {
			if m, ok := c.(discordgo.SelectMenu); ok {
				return m, true
			}
		}
	}
	return discordgo.SelectMenu{}, false
}

func TestListEmptyAndFailure(t *testing.T) {
	st := useVoice(t)
	r := lastOf(t, runVoice(t, "list", nil)).Response
	if !strings.Contains(textOf(r), "No voice channel roles are set") || len(r.Components) != 0 {
		t.Errorf("reply = %+v", r)
	}
	st.getErr = errors.New("db down")
	r = lastOf(t, runVoice(t, "list", nil)).Response
	if r.Content != interaction.SomethingWentWrong {
		t.Errorf("reply = %+v", r)
	}
}

func manyChannels(n int) []guilds.VoiceCha {
	out := make([]guilds.VoiceCha, n)
	for i := range out {
		out[i] = guilds.VoiceCha{ID: fmt.Sprintf("v%02d", i), Name: fmt.Sprintf("c%02d", i), Roles: []guilds.Role{{ID: "r", Name: "R"}}}
	}
	return out
}

func TestListPagesHoldAtMost25PairsAndTheirOwnPicker(t *testing.T) {
	useVoice(t, manyChannels(30)...)
	first := lastOf(t, runVoice(t, "list", nil)).Response
	p1, _ := pickerIn(first)
	if len(p1.Options) != 25 || len(first.Components) != 2 {
		t.Fatalf("first page: %d options, %d rows", len(p1.Options), len(first.Components))
	}
	second := RenderVoiceRolePage(t.Context(), nil, nil, &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{GuildID: "g"}}, 1, 30, "user")
	if second.Refusal != "" {
		t.Fatal(second.Refusal)
	}
	p2, _ := pickerIn(second.Response)
	if len(p2.Options) != 5 || p2.Options[0].Value != "v25:r" {
		t.Errorf("second page picker = %+v", p2.Options)
	}
}

func pickAs(user, customID string, values ...string) *interaction.Context {
	i := &discordgo.Interaction{Type: discordgo.InteractionMessageComponent, GuildID: "g", ChannelID: "c",
		Member: &discordgo.Member{User: &discordgo.User{ID: user}},
		Data:   discordgo.MessageComponentInteractionData{CustomID: customID, ComponentType: discordgo.SelectMenuComponent, Values: values}}
	return interaction.NewContext(&interactiontest.Fake{}, i, nil, interaction.Private, interaction.Direct)
}

func TestPickerRemovesThePairAndRerendersThePage(t *testing.T) {
	st := useVoice(t, guilds.VoiceCha{ID: "v1", Name: "L", Roles: []guilds.Role{{ID: "r1", Name: "A"}, {ID: "r2", Name: "B"}}})
	ic := pickAs("user", voicePickerPrefix+"user:0", "v1:r1")
	if err := handleVoiceRemovePicker(ic, nil); err != nil {
		t.Fatal(err)
	}
	if roles := st.settings.VoiceChas[0].Roles; len(roles) != 1 || roles[0].ID != "r2" {
		t.Errorf("stored = %+v", st.settings.VoiceChas)
	}
	calls := ic.Port.(*interactiontest.Fake).Calls()
	picker, _ := pickerIn(calls[len(calls)-1].Response)
	if calls[len(calls)-1].Op != "UpdateMessage" || len(picker.Options) != 1 {
		t.Errorf("calls = %+v", calls)
	}
}

func TestPickerRemovingTheLastPairClearsTheComponents(t *testing.T) {
	st := useVoice(t, guilds.VoiceCha{ID: "v1", Roles: []guilds.Role{{ID: "r1"}}})
	ic := pickAs("user", voicePickerPrefix+"user:0", "v1:r1")
	if err := handleVoiceRemovePicker(ic, nil); err != nil {
		t.Fatal(err)
	}
	calls := ic.Port.(*interactiontest.Fake).Calls()
	r := calls[len(calls)-1].Response
	if len(st.settings.VoiceChas) != 0 || r.Components == nil || len(r.Components) != 0 || !strings.Contains(textOf(r), "No voice channel roles") {
		t.Errorf("stored %+v, response %+v", st.settings.VoiceChas, r)
	}
}

func TestPickerIsOwnerCheckedBeforeTheStoreIsTouched(t *testing.T) {
	st := useVoice(t, guilds.VoiceCha{ID: "v1", Roles: []guilds.Role{{ID: "r1"}}})
	st.getErr = errors.New("must not be read")
	ic := pickAs("stranger", voicePickerPrefix+"user:0", "v1:r1")
	if err := handleVoiceRemovePicker(ic, nil); err != nil {
		t.Fatal(err)
	}
	calls := ic.Port.(*interactiontest.Fake).Calls()
	if len(calls) != 1 || calls[0].Op != "Reply" || calls[0].Visibility != interaction.Private || !strings.Contains(calls[0].Response.Content, "Only the moderator") || st.saves != 0 {
		t.Errorf("calls = %+v", calls)
	}
}

func TestPickerIgnoresAGonePairAndRefusesAStaleID(t *testing.T) {
	st := useVoice(t, guilds.VoiceCha{ID: "v1", Roles: []guilds.Role{{ID: "r1"}}})
	ic := pickAs("user", voicePickerPrefix+"user:0", "v1:gone")
	if err := handleVoiceRemovePicker(ic, nil); err != nil {
		t.Fatal(err)
	}
	if calls := ic.Port.(*interactiontest.Fake).Calls(); calls[0].Op != "UpdateMessage" || st.saves != 0 {
		t.Errorf("calls = %+v", calls)
	}
	ic = pickAs("user", voicePickerPrefix+"garbage", "v1:r1")
	_ = handleVoiceRemovePicker(ic, nil)
	if calls := ic.Port.(*interactiontest.Fake).Calls(); calls[0].Op != "Reply" || calls[0].Visibility != interaction.Private {
		t.Errorf("calls = %+v", calls)
	}
}

func TestPickerChoiceThatClipsToMoreThanOnePairRemovesNothing(t *testing.T) {
	long := strings.Repeat("x", 100)
	st := useVoice(t, guilds.VoiceCha{ID: long, Roles: []guilds.Role{{ID: "r1"}, {ID: "r2"}}})
	ic := pickAs("user", voicePickerPrefix+"user:0", long)
	if err := handleVoiceRemovePicker(ic, nil); err != nil {
		t.Fatal(err)
	}
	calls := ic.Port.(*interactiontest.Fake).Calls()
	if st.saves != 0 || len(calls) != 1 || calls[0].Op != "Reply" || !strings.Contains(calls[0].Response.Content, "removed nothing") {
		t.Errorf("saves %d, calls %+v", st.saves, calls)
	}
}

func TestPickerSaveFailureIsRefusedPrivately(t *testing.T) {
	st := useVoice(t, guilds.VoiceCha{ID: "v1", Roles: []guilds.Role{{ID: "r1"}}})
	st.setErr = errors.New("db down")
	ic := pickAs("user", voicePickerPrefix+"user:0", "v1:r1")
	_ = handleVoiceRemovePicker(ic, nil)
	calls := ic.Port.(*interactiontest.Fake).Calls()
	if calls[0].Op != "Reply" || calls[0].Visibility != interaction.Private {
		t.Errorf("calls = %+v", calls)
	}
}
