package settings

import (
	"errors"
	"strings"
	"testing"

	"ReZeroTsu/internal/database/guilds"
	"ReZeroTsu/internal/interaction"
	"ReZeroTsu/internal/interaction/interactiontest"

	"github.com/bwmarrin/discordgo"
)

func channelOf(id, name string, typ discordgo.ChannelType) *discordgo.Channel {
	return &discordgo.Channel{ID: id, Name: name, Type: typ}
}

func pickRole(t *testing.T, fake *interactiontest.Fake, customID string, roles ...*discordgo.Role) error {
	t.Helper()
	data := discordgo.MessageComponentInteractionData{CustomID: customID, ComponentType: discordgo.RoleSelectMenuComponent}
	data.Resolved.Roles = map[string]*discordgo.Role{}
	for _, r := range roles {
		data.Values = append(data.Values, r.ID)
		data.Resolved.Roles[r.ID] = r
	}
	return handlePanel(pressContext(fake, moderator, data), nil)
}

func menuAt(r interaction.Response, row int) discordgo.SelectMenu {
	return r.Components[row].(discordgo.ActionsRow).Components[0].(discordgo.SelectMenu)
}

func storeWithAutoposts() *fakeStore {
	return &fakeStore{gs: stored(), autoposts: map[string]*guilds.Autopost{
		postTypeNewEpisodes:   {PostType: postTypeNewEpisodes, Name: "ep", ID: "300", RoleID: "400"},
		postTypeDailySchedule: {PostType: postTypeDailySchedule, Name: "daily", ID: "500"},
	}}
}

func TestPanelShowsAutopostRowsWithStoredDefaults(t *testing.T) {
	useStore(t, storeWithAutoposts())
	fake := &interactiontest.Fake{}
	c := settingsCommand(t)
	ic := interactiontest.NewCommandContext(fake, c.Visibility, c.Response, discordgo.ApplicationCommandInteractionData{Name: "settings"})
	if err := c.Run(ic); err != nil {
		t.Fatal(err)
	}
	r := lastCall(t, fake).Response
	desc, _, _ := panelOf(t, r)
	for _, want := range []string{"<#300>", "<@&400>", "<#500>"} {
		if !strings.Contains(desc, want) {
			t.Errorf("panel %q is missing %q", desc, want)
		}
	}
	ep, role, daily := menuAt(r, 1), menuAt(r, 2), menuAt(r, 3)
	if ep.CustomID != idNewEpisodes || ep.MenuType != discordgo.ChannelSelectMenu || *ep.MinValues != 0 || len(ep.ChannelTypes) != 3 || ep.DefaultValues[0].ID != "300" {
		t.Errorf("new-episodes picker = %+v", ep)
	}
	if role.CustomID != idNewEpisodesRole || role.MenuType != discordgo.RoleSelectMenu || *role.MinValues != 0 ||
		role.DefaultValues[0].ID != "400" || role.DefaultValues[0].Type != discordgo.SelectMenuDefaultValueRole {
		t.Errorf("role picker = %+v", role)
	}
	if daily.CustomID != idDailySchedule || *daily.MinValues != 0 || len(daily.ChannelTypes) != 3 || daily.DefaultValues[0].ID != "500" {
		t.Errorf("daily picker = %+v", daily)
	}
}

func TestChoosingNewEpisodesChannelKeepsRoleAndDropsPreviousSentState(t *testing.T) {
	st := storeWithAutoposts()
	useStore(t, st)
	fake := &interactiontest.Fake{}
	if err := pick(t, fake, moderator, idNewEpisodes, channelOf("301", "thread", discordgo.ChannelTypeGuildPublicThread)); err != nil {
		t.Fatal(err)
	}
	ap := st.autoposts[postTypeNewEpisodes]
	if ap == nil || ap.ID != "301" || ap.Name != "thread" || ap.RoleID != "400" {
		t.Errorf("new-episodes autopost = %+v", ap)
	}
	if len(st.deletedSentState) != 1 || st.deletedSentState[0] != "300" {
		t.Errorf("deleted sent state = %v, want the previous channel's", st.deletedSentState)
	}
	if st.sets != 0 || st.autoposts[postTypeDailySchedule].ID != "500" {
		t.Error("another setting changed")
	}
	if call := lastCall(t, fake); call.Op != "UpdateMessage" || !strings.Contains(call.Response.Embeds[0].Description, "<#301>") {
		t.Errorf("last call = %s, want the panel showing the new channel", call.Op)
	}
}

func TestChoosingSameNewEpisodesChannelKeepsSentState(t *testing.T) {
	st := storeWithAutoposts()
	useStore(t, st)
	if err := pick(t, &interactiontest.Fake{}, moderator, idNewEpisodes, channelOf("300", "renamed", discordgo.ChannelTypeGuildText)); err != nil {
		t.Fatal(err)
	}
	if len(st.deletedSentState) != 0 || st.autoposts[postTypeNewEpisodes].Name != "renamed" {
		t.Errorf("deleted %v, autopost %+v", st.deletedSentState, st.autoposts[postTypeNewEpisodes])
	}
}

func TestSettingNewEpisodesChannelFirstTimeDeletesNothing(t *testing.T) {
	st := &fakeStore{gs: stored()}
	useStore(t, st)
	if err := pick(t, &interactiontest.Fake{}, moderator, idNewEpisodes, channelOf("300", "ep", discordgo.ChannelTypeGuildText)); err != nil {
		t.Fatal(err)
	}
	if ap := st.autoposts[postTypeNewEpisodes]; ap == nil || ap.ID != "300" || ap.PostType != postTypeNewEpisodes {
		t.Errorf("autopost = %+v", ap)
	}
	if len(st.deletedSentState) != 0 {
		t.Errorf("deleted %v", st.deletedSentState)
	}
}

func TestClearingNewEpisodesChannelDeletesSentStateAndAutopost(t *testing.T) {
	st := storeWithAutoposts()
	useStore(t, st)
	fake := &interactiontest.Fake{}
	if err := pick(t, fake, moderator, idNewEpisodes); err != nil {
		t.Fatal(err)
	}
	if st.autoposts[postTypeNewEpisodes] != nil {
		t.Error("autopost still set")
	}
	if len(st.deletedSentState) != 1 || st.deletedSentState[0] != "300" {
		t.Errorf("deleted sent state = %v", st.deletedSentState)
	}
	if st.autoposts[postTypeDailySchedule] == nil {
		t.Error("daily schedule was cleared too")
	}
	r := lastCall(t, fake).Response
	if len(menuAt(r, 1).DefaultValues) != 0 || len(menuAt(r, 2).DefaultValues) != 0 {
		t.Error("pickers still show the cleared autopost")
	}
}

func TestRoleWithoutChannelIsRefused(t *testing.T) {
	st := &fakeStore{gs: stored()}
	useStore(t, st)
	fake := &interactiontest.Fake{}
	if err := pickRole(t, fake, idNewEpisodesRole, &discordgo.Role{ID: "400"}); err != nil {
		t.Fatal(err)
	}
	if st.autoposts[postTypeNewEpisodes] != nil {
		t.Error("a role was stored without a channel")
	}
	if call := lastCall(t, fake); call.Op != "FollowUp" || call.Visibility != interaction.Private || !strings.Contains(call.Response.Content, "Set a channel first") {
		t.Errorf("last call = %s %q", call.Op, call.Response.Content)
	}
}

func TestChoosingAndClearingRole(t *testing.T) {
	st := storeWithAutoposts()
	st.autoposts[postTypeNewEpisodes].RoleID = ""
	useStore(t, st)
	fake := &interactiontest.Fake{}
	if err := pickRole(t, fake, idNewEpisodesRole, &discordgo.Role{ID: "401", Mentionable: true}); err != nil {
		t.Fatal(err)
	}
	if got := st.autoposts[postTypeNewEpisodes]; got.RoleID != "401" || got.ID != "300" {
		t.Errorf("autopost = %+v", got)
	}
	if err := pickRole(t, fake, idNewEpisodesRole); err != nil {
		t.Fatal(err)
	}
	if got := st.autoposts[postTypeNewEpisodes]; got.RoleID != "" || got.ID != "300" {
		t.Errorf("autopost after clearing = %+v", got)
	}
	if len(st.deletedSentState) != 0 {
		t.Error("changing the role dropped sent state")
	}
}

func TestDailyScheduleChooseAndClear(t *testing.T) {
	st := &fakeStore{gs: stored()}
	useStore(t, st)
	fake := &interactiontest.Fake{}
	if err := pick(t, fake, moderator, idDailySchedule, channelOf("500", "daily", discordgo.ChannelTypeGuildText)); err != nil {
		t.Fatal(err)
	}
	if ap := st.autoposts[postTypeDailySchedule]; ap == nil || ap.ID != "500" || ap.Name != "daily" {
		t.Fatalf("daily = %+v", ap)
	}
	if err := pick(t, fake, moderator, idDailySchedule); err != nil {
		t.Fatal(err)
	}
	if st.autoposts[postTypeDailySchedule] != nil || len(st.deletedSentState) != 0 {
		t.Errorf("daily = %+v, deleted %v; want it disabled with no sent state touched", st.autoposts[postTypeDailySchedule], st.deletedSentState)
	}
}

func TestAutopostPickersRefuseNonModerators(t *testing.T) {
	st := storeWithAutoposts()
	useStore(t, st)
	fake := &interactiontest.Fake{Guilds: map[string]*discordgo.Guild{"g": {ID: "g", OwnerID: "x"}}}
	for _, id := range []string{idNewEpisodes, idDailySchedule} {
		if err := pick(t, fake, member, id); err != nil {
			t.Fatal(err)
		}
	}
	if st.autoposts[postTypeNewEpisodes] == nil || st.autoposts[postTypeDailySchedule] == nil || len(st.deletedSentState) != 0 {
		t.Error("a non-moderator changed an autopost")
	}
	if call := lastCall(t, fake); !strings.Contains(call.Response.Content, "You need") {
		t.Errorf("last = %q", call.Response.Content)
	}
}

func TestFailedAutopostSaveKeepsStoredPanel(t *testing.T) {
	st := storeWithAutoposts()
	st.failAutopost = errors.New("db down")
	useStore(t, st)
	fake := &interactiontest.Fake{}
	if err := pick(t, fake, moderator, idDailySchedule); err != nil {
		t.Fatal(err)
	}
	var calls []interactiontest.Call
	for _, c := range fake.Calls() {
		if c.Op == "UpdateMessage" || c.Op == "FollowUp" {
			calls = append(calls, c)
		}
	}
	if len(calls) != 2 || calls[0].Op != "UpdateMessage" || calls[1].Op != "FollowUp" || !strings.Contains(calls[0].Response.Embeds[0].Description, "<#500>") {
		t.Errorf("calls = %v", fake.Ops())
	}
}

func TestMentionableWarningFollowsItsCondition(t *testing.T) {
	tests := []struct {
		name   string
		roleID string
		role   *discordgo.Role
		perms  int64
		want   bool
	}{
		{"unmentionable, no permission", "400", &discordgo.Role{ID: "400"}, 0, true},
		{"mentionable", "400", &discordgo.Role{ID: "400", Mentionable: true}, 0, false},
		{"bot may mention everyone", "400", &discordgo.Role{ID: "400"}, discordgo.PermissionMentionEveryone, false},
		{"everyone role", "g", &discordgo.Role{ID: "g"}, 0, false},
		{"no role chosen", "", &discordgo.Role{ID: "400"}, 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := storeWithAutoposts()
			st.autoposts[postTypeNewEpisodes].RoleID = tc.roleID
			useStore(t, st)
			fake := &interactiontest.Fake{
				Guilds:   map[string]*discordgo.Guild{"g": {ID: "g", Roles: []*discordgo.Role{tc.role}}},
				BotPerms: map[string]int64{"300": tc.perms},
			}
			if err := click(t, fake, moderator, idDonghua); err != nil {
				t.Fatal(err)
			}
			got := strings.Contains(lastCall(t, fake).Response.Embeds[0].Description, warningMarker)
			if got != tc.want {
				t.Errorf("warning shown = %v, want %v", got, tc.want)
			}
		})
	}
}
