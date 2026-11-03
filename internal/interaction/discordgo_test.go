package interaction

import (
	"testing"

	"github.com/bwmarrin/discordgo"
)

// portWithGuild is a port over a session whose state holds one guild: the bot, an @everyone role that grants
// Mention Everyone, a parent text channel that denies it, and a thread under that parent with no overwrites.
func portWithGuild(t *testing.T) *DiscordgoPort {
	t.Helper()
	s := &discordgo.Session{State: discordgo.NewState()}
	s.State.User = &discordgo.User{ID: "bot"}
	g := &discordgo.Guild{
		ID:      "g",
		OwnerID: "owner",
		Roles:   []*discordgo.Role{{ID: "g", Permissions: discordgo.PermissionViewChannel | discordgo.PermissionMentionEveryone}},
		Members: []*discordgo.Member{{GuildID: "g", User: &discordgo.User{ID: "bot"}}},
		Channels: []*discordgo.Channel{
			{ID: "parent", GuildID: "g", Type: discordgo.ChannelTypeGuildText, PermissionOverwrites: []*discordgo.PermissionOverwrite{
				{ID: "g", Type: discordgo.PermissionOverwriteTypeRole, Deny: discordgo.PermissionMentionEveryone},
			}},
			{ID: "thread", GuildID: "g", ParentID: "parent", Type: discordgo.ChannelTypeGuildPublicThread},
		},
	}
	if err := s.State.GuildAdd(g); err != nil {
		t.Fatal(err)
	}
	return NewDiscordgoPort(s, &discordgo.Interaction{})
}

func TestBotChannelPermissionsOfAThreadFollowTheParentChannel(t *testing.T) {
	p := portWithGuild(t)
	parent, err := p.BotChannelPermissions("parent")
	if err != nil {
		t.Fatal(err)
	}
	if parent&discordgo.PermissionMentionEveryone != 0 {
		t.Fatalf("parent permissions %d include Mention Everyone; the fixture is wrong", parent)
	}
	thread, err := p.BotChannelPermissions("thread")
	if err != nil {
		t.Fatal(err)
	}
	if thread&discordgo.PermissionMentionEveryone != 0 {
		t.Errorf("thread permissions %d include Mention Everyone, but its parent denies it", thread)
	}
}
