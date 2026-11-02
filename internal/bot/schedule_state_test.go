package bot

import (
	"testing"

	"github.com/bwmarrin/discordgo"
)

// channelStateFirst must hand out a private copy: discordgo's ChannelAdd overwrites the cached struct in place
// under the state lock, so callers reading the cached pointer without that lock race with it.
func TestChannelStateFirstReturnsPrivateCopy(t *testing.T) {
	st := discordgo.NewState()
	if err := st.GuildAdd(&discordgo.Guild{ID: "g1"}); err != nil {
		t.Fatal(err)
	}
	ow := &discordgo.PermissionOverwrite{ID: "r1", Allow: 1}
	if err := st.ChannelAdd(&discordgo.Channel{ID: "c1", GuildID: "g1", Type: discordgo.ChannelTypeGuildText, PermissionOverwrites: []*discordgo.PermissionOverwrite{ow}}); err != nil {
		t.Fatal(err)
	}
	s := &discordgo.Session{State: st}

	got, err := channelStateFirst(s, "c1")
	if err != nil {
		t.Fatal(err)
	}
	cached, _ := st.Channel("c1")
	if got == cached {
		t.Fatal("returned the state-owned channel pointer")
	}
	if got.Type != discordgo.ChannelTypeGuildText || len(got.PermissionOverwrites) != 1 || got.PermissionOverwrites[0].Allow != 1 {
		t.Fatalf("copy = %+v, want the cached channel's fields", got)
	}
	if got.PermissionOverwrites[0] == cached.PermissionOverwrites[0] {
		t.Error("permission overwrites are shared with the state cache")
	}

	// An in-place update of the cache must not show through the copy.
	if err := st.ChannelAdd(&discordgo.Channel{ID: "c1", GuildID: "g1", Type: discordgo.ChannelTypeGuildVoice}); err != nil {
		t.Fatal(err)
	}
	if got.Type != discordgo.ChannelTypeGuildText {
		t.Errorf("copy changed with the cache: type = %v", got.Type)
	}
}
