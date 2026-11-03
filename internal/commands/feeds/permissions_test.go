package feeds

import (
	"context"
	"strings"
	"testing"

	"ReZeroTsu/internal/discord"
	"ReZeroTsu/internal/interaction"
	"ReZeroTsu/internal/interaction/interactiontest"

	"github.com/bwmarrin/discordgo"
)

const postPerms int64 = discordgo.PermissionViewChannel | discordgo.PermissionSendMessages | discordgo.PermissionEmbedLinks | discordgo.PermissionReadMessageHistory
const pinPerms int64 = discordgo.PermissionManageMessages | discord.PermissionPinMessages

// invokeAdd runs /reddit add against a fake with its channel state set up.
func invokeAdd(t *testing.T, fake *interactiontest.Fake, opts ...*discordgo.ApplicationCommandInteractionDataOption) interaction.Response {
	t.Helper()
	cmd := redditCommand(t)
	ic := interactiontest.NewCommandContext(fake, cmd.Visibility, cmd.Response, discordgo.ApplicationCommandInteractionData{Name: "reddit", Options: opts})
	if err := ic.Defer(0); err != nil {
		t.Fatal(err)
	}
	if err := subcommand(t, "add").Run(ic); err != nil {
		t.Fatal(err)
	}
	return lastReply(t, fake)
}

func TestAddRefusesWhenARequiredPermissionIsMissing(t *testing.T) {
	cases := []struct {
		name    string
		missing int64
		want    []string
	}{
		{"view", discordgo.PermissionViewChannel, []string{"View Channel"}},
		{"send", discordgo.PermissionSendMessages, []string{"Send Messages"}},
		{"embed", discordgo.PermissionEmbedLinks, []string{"Embed Links"}},
		{"history", discordgo.PermissionReadMessageHistory, []string{"Read Message History"}},
		{"several", discordgo.PermissionSendMessages | discordgo.PermissionEmbedLinks, []string{"Send Messages", "Embed Links"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := useStore(t)
			checkFeed = func(context.Context, string, string) error { t.Error("Reddit was asked"); return nil }
			fake := &interactiontest.Fake{BotPerms: map[string]int64{"c1": postPerms &^ tc.missing}}
			got := text(invokeAdd(t, fake, chanOpt("c1"), strOpt("subreddit", "anime")))
			if !strings.Contains(got, "<#c1>") {
				t.Errorf("reply %q does not mention the channel", got)
			}
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("reply %q does not name %s", got, w)
				}
			}
			if len(st.feeds) != 0 || st.addCalls != 0 {
				t.Errorf("saved %+v", st.feeds)
			}
		})
	}
}

func TestAddChecksSendMessagesInThreadsForAThread(t *testing.T) {
	useStore(t)
	thread := &discordgo.Channel{ID: "t1", Type: discordgo.ChannelTypeGuildPublicThread}
	base := postPerms &^ discordgo.PermissionSendMessages
	fake := &interactiontest.Fake{Channels: map[string]*discordgo.Channel{"t1": thread}, BotPerms: map[string]int64{"t1": base}}
	got := text(invokeAdd(t, fake, chanOpt("t1"), strOpt("subreddit", "anime")))
	if !strings.Contains(got, "Send Messages in Threads") {
		t.Errorf("reply = %q, want Send Messages in Threads named", got)
	}
	st := useStore(t)
	fake = &interactiontest.Fake{Channels: map[string]*discordgo.Channel{"t1": thread}, BotPerms: map[string]int64{"t1": base | discordgo.PermissionSendMessagesInThreads}}
	invokeAdd(t, fake, chanOpt("t1"), strOpt("subreddit", "anime"))
	if len(st.feeds) != 1 {
		t.Errorf("thread feed not saved: %+v", st.feeds)
	}
}

// A thread the bot cannot look up (uncached, lookup failing) is still a thread: the add option's resolved channel
// carries its type, so Send Messages in Threads decides.
func TestAddUsesTheResolvedChannelTypeWhenTheLookupFails(t *testing.T) {
	st := useStore(t)
	perms := postPerms&^discordgo.PermissionSendMessages | discordgo.PermissionSendMessagesInThreads
	fake := &interactiontest.Fake{BotPerms: map[string]int64{"t1": perms}} // no Channels: Channel("t1") errors
	cmd := redditCommand(t)
	data := discordgo.ApplicationCommandInteractionData{Name: "reddit",
		Options:  []*discordgo.ApplicationCommandInteractionDataOption{chanOpt("t1"), strOpt("subreddit", "anime")},
		Resolved: &discordgo.ApplicationCommandInteractionDataResolved{Channels: map[string]*discordgo.Channel{"t1": {ID: "t1", Type: discordgo.ChannelTypeGuildPublicThread}}}}
	ic := interactiontest.NewCommandContext(fake, cmd.Visibility, cmd.Response, data)
	if err := ic.Defer(0); err != nil {
		t.Fatal(err)
	}
	if err := subcommand(t, "add").Run(ic); err != nil {
		t.Fatal(err)
	}
	if got := text(lastReply(t, fake)); len(st.feeds) != 1 || strings.Contains(got, "missing") {
		t.Errorf("feeds %+v, reply %q", st.feeds, got)
	}
}

func TestAddWarnsAboutPinningButSavesTheFeed(t *testing.T) {
	cases := []struct {
		name    string
		perms   int64
		pin     bool
		warning string
	}{
		{"manage messages missing", postPerms | discord.PermissionPinMessages, true, "Manage Messages"},
		{"pin messages missing", postPerms | discordgo.PermissionManageMessages, true, "Pin Messages"},
		{"pin off", postPerms, false, ""},
		{"both present", postPerms | pinPerms, true, ""},
		{"administrator", discordgo.PermissionAdministrator, true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := useStore(t)
			fake := &interactiontest.Fake{BotPerms: map[string]int64{"c1": tc.perms}}
			got := text(invokeAdd(t, fake, chanOpt("c1"), strOpt("subreddit", "anime"), boolOpt("pin", tc.pin)))
			if len(st.feeds) != 1 {
				t.Fatalf("feed not saved: %+v", st.feeds)
			}
			if tc.warning == "" {
				if strings.Contains(got, "missing") {
					t.Errorf("unexpected warning in %q", got)
				}
				return
			}
			if !strings.Contains(got, tc.warning) || !strings.Contains(got, "<#c1>") {
				t.Errorf("reply %q lacks the pin warning naming %s and the channel", got, tc.warning)
			}
		})
	}
}

func TestAddAdministratorPassesEveryCheck(t *testing.T) {
	st := useStore(t)
	fake := &interactiontest.Fake{BotPerms: map[string]int64{"c1": discordgo.PermissionAdministrator}}
	invokeAdd(t, fake, chanOpt("c1"), strOpt("subreddit", "anime"))
	if len(st.feeds) != 1 {
		t.Errorf("feeds = %+v", st.feeds)
	}
}

func TestAddWithUnreadablePermissionsNeitherRefusesNorWarns(t *testing.T) {
	st := useStore(t)
	fake := &interactiontest.Fake{} // no BotPerms: BotChannelPermissions errors
	got := text(invokeAdd(t, fake, chanOpt("c1"), strOpt("subreddit", "anime"), boolOpt("pin", true)))
	if len(st.feeds) != 1 || strings.Contains(got, "missing") {
		t.Errorf("feeds %+v reply %q", st.feeds, got)
	}
}
