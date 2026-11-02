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
	if err := st.ChannelAdd(&discordgo.Channel{ID: "c1", GuildID: "g1", Type: discordgo.ChannelTypeGuildPublicThread, ParentID: "p1"}); err != nil {
		t.Fatal(err)
	}
	s := &discordgo.Session{State: st}

	got, err := channelStateFirst(s, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Type != discordgo.ChannelTypeGuildPublicThread || got.ParentID != "p1" {
		t.Fatalf("copy = %+v, want the cached channel's type and parent", got)
	}

	// An in-place update of the cache must not show through the copy.
	if err := st.ChannelAdd(&discordgo.Channel{ID: "c1", GuildID: "g1", Type: discordgo.ChannelTypeGuildVoice}); err != nil {
		t.Fatal(err)
	}
	if got.Type != discordgo.ChannelTypeGuildPublicThread || got.ParentID != "p1" {
		t.Errorf("copy changed with the cache: %+v", got)
	}
}

// Reading a cached channel while the gateway updates it in place must not race (run with -race).
func TestChannelStateFirstConcurrentWithChannelAdd(t *testing.T) {
	st := discordgo.NewState()
	if err := st.GuildAdd(&discordgo.Guild{ID: "g1"}); err != nil {
		t.Fatal(err)
	}
	if err := st.ChannelAdd(&discordgo.Channel{ID: "c1", GuildID: "g1", Type: discordgo.ChannelTypeGuildText}); err != nil {
		t.Fatal(err)
	}
	s := &discordgo.Session{State: st}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			_ = st.ChannelAdd(&discordgo.Channel{ID: "c1", GuildID: "g1", Type: discordgo.ChannelTypeGuildText, ParentID: "p"})
		}
	}()
	for i := 0; i < 200; i++ {
		if _, err := channelStateFirst(s, "c1"); err != nil {
			t.Fatal(err)
		}
	}
	<-done
}
