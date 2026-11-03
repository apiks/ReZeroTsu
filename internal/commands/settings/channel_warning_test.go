package settings

import (
	"strings"
	"testing"

	"ReZeroTsu/internal/database/guilds"
	"ReZeroTsu/internal/interaction/interactiontest"

	"github.com/bwmarrin/discordgo"
)

const (
	canPostPerms = discordgo.PermissionViewChannel | discordgo.PermissionSendMessages | discordgo.PermissionEmbedLinks
	channelWarn  = "cannot post in"
)

func panelWith(t *testing.T, st *fakeStore, fake *interactiontest.Fake) string {
	t.Helper()
	useStore(t, st)
	if err := click(t, fake, moderator, idDonghua); err != nil {
		t.Fatal(err)
	}
	return lastCall(t, fake).Response.Embeds[0].Description
}

func TestAutopostChannelWarningNamesMissingPermissions(t *testing.T) {
	tests := []struct {
		name  string
		perms map[string]int64
		chans map[string]*discordgo.Channel
		want  []string
		none  bool
	}{
		{"new episodes missing embed", map[string]int64{"300": discordgo.PermissionViewChannel | discordgo.PermissionSendMessages, "500": canPostPerms},
			nil, []string{"<#300>", "Embed Links"}, false},
		{"daily missing everything", map[string]int64{"300": canPostPerms, "500": 0},
			nil, []string{"<#500>", "View Channel", "Send Messages", "Embed Links"}, false},
		{"thread needs send in threads", map[string]int64{"300": canPostPerms, "500": canPostPerms},
			map[string]*discordgo.Channel{"300": {ID: "300", Type: discordgo.ChannelTypeGuildPublicThread}}, []string{"<#300>", "Send Messages in Threads"}, false},
		{"all present", map[string]int64{"300": canPostPerms, "500": canPostPerms}, nil, nil, true},
		{"administrator", map[string]int64{"300": discordgo.PermissionAdministrator, "500": discordgo.PermissionAdministrator}, nil, nil, true},
		{"unreadable", nil, nil, nil, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fake := &interactiontest.Fake{BotPerms: tc.perms, Channels: tc.chans}
			desc := panelWith(t, storeWithAutoposts(), fake)
			if tc.none {
				if strings.Contains(desc, channelWarn) {
					t.Errorf("panel %q has a channel warning", desc)
				}
				return
			}
			for _, w := range tc.want {
				if !strings.Contains(desc, w) {
					t.Errorf("panel %q is missing %q", desc, w)
				}
			}
		})
	}
}

func TestNoChannelWarningWhileAutopostsAreOff(t *testing.T) {
	st := &fakeStore{gs: stored(), autoposts: map[string]*guilds.Autopost{}}
	fake := &interactiontest.Fake{BotPerms: map[string]int64{"300": 0, "500": 0}}
	if desc := panelWith(t, st, fake); strings.Contains(desc, channelWarn) {
		t.Errorf("panel %q has a channel warning", desc)
	}
}

func TestChoosingUnpostableChannelStillSavesAndWarns(t *testing.T) {
	st := storeWithAutoposts()
	useStore(t, st)
	fake := &interactiontest.Fake{BotPerms: map[string]int64{"600": 0}}
	if err := pick(t, fake, moderator, idDailySchedule, &discordgo.Channel{ID: "600", Name: "new", Type: discordgo.ChannelTypeGuildText}); err != nil {
		t.Fatal(err)
	}
	if ap := st.autoposts[postTypeDailySchedule]; ap == nil || ap.ID != "600" {
		t.Fatalf("daily = %+v, want it saved", ap)
	}
	desc := lastCall(t, fake).Response.Embeds[0].Description
	if !strings.Contains(desc, channelWarn) || !strings.Contains(desc, "<#600>") {
		t.Errorf("panel %q lacks the warning", desc)
	}
}
