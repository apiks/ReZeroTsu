package bot

import (
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/bwmarrin/discordgo"
)

// countingTransport answers every REST request with a cached-looking text channel and counts the requests.
type countingTransport struct{ n atomic.Int32 }

func (c *countingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c.n.Add(1)
	return &http.Response{
		StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Request: r,
		Body: io.NopCloser(strings.NewReader(`{"id":"c1","guild_id":"g1","type":0}`)),
	}, nil
}

// restSession is a session whose REST calls go to a countingTransport, with the given state.
func restSession(t *testing.T, st *discordgo.State) (*discordgo.Session, *countingTransport) {
	t.Helper()
	s, err := discordgo.New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	tr := &countingTransport{}
	s.Client = &http.Client{Transport: tr}
	s.State = st
	return s, tr
}

// stateWithChannel is a state holding guild g1 and its text channel c1, with the given bot user (may be nil).
func stateWithChannel(t *testing.T, user *discordgo.User) *discordgo.State {
	t.Helper()
	st := discordgo.NewState()
	st.User = user
	if err := st.GuildAdd(&discordgo.Guild{ID: "g1"}); err != nil {
		t.Fatal(err)
	}
	if err := st.ChannelAdd(&discordgo.Channel{ID: "c1", GuildID: "g1", Type: discordgo.ChannelTypeGuildText}); err != nil {
		t.Fatal(err)
	}
	return st
}

// The permission checks behind canSendToChannel and resolveChannelWebhook read the state cache, so a channel
// missing from it is unusable straight away: no REST lookup is spent on it. A nil state or a state without the bot
// user is unusable too, and never panics.
func TestChannelUsabilityNeedsStateAndNoREST(t *testing.T) {
	tests := []struct {
		name  string
		state func(t *testing.T) *discordgo.State
	}{
		{"nil state", func(*testing.T) *discordgo.State { return nil }},
		{"channel missing from state", func(*testing.T) *discordgo.State { return discordgo.NewState() }},
		{"state without the bot user", func(t *testing.T) *discordgo.State { return stateWithChannel(t, nil) }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, tr := restSession(t, tc.state(t))
			if canSendToChannel(s, "c1") {
				t.Errorf("canSendToChannel = true, want false")
			}
			if w := resolveChannelWebhook(s, "c1"); w != nil {
				t.Errorf("resolveChannelWebhook = %v, want nil", w)
			}
			if n := tr.n.Load(); n != 0 {
				t.Errorf("REST requests = %d, want 0", n)
			}
		})
	}
}

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
