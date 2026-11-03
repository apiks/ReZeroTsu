package bot

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/bwmarrin/discordgo"
)

// pinTransport records "METHOD path" for every REST request. The pinned list holds one older bot pin for the
// subreddit; every other call succeeds with an empty body.
type pinTransport struct {
	mu    sync.Mutex
	calls []string
	fail  bool
}

func (p *pinTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	p.mu.Lock()
	p.calls = append(p.calls, r.Method+" "+r.URL.Path)
	p.mu.Unlock()
	status, body := http.StatusNoContent, ""
	switch {
	case p.fail:
		status, body = http.StatusForbidden, `{"code":50013,"message":"Missing Permissions"}`
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/pins"):
		status = http.StatusOK
		body = `[{"id":"old","author":{"id":"bot"},"embeds":[{"author":{"url":"https://www.reddit.com/r/anime/comments/1"}}]}]`
	}
	return &http.Response{
		StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Request: r,
		Body: io.NopCloser(strings.NewReader(body)),
	}, nil
}

func (p *pinTransport) got() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.calls...)
}

// pinSession is a session whose state grants the bot (@everyone role) the given permissions on text channel c1.
func pinSession(t *testing.T, perms int64, tr *pinTransport) *discordgo.Session {
	t.Helper()
	s, err := discordgo.New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	s.Client = &http.Client{Transport: tr}
	st := discordgo.NewState()
	st.User = &discordgo.User{ID: "bot"}
	g := &discordgo.Guild{
		ID: "g1", OwnerID: "someone-else",
		Roles:   []*discordgo.Role{{ID: "g1", Permissions: perms}},
		Members: []*discordgo.Member{{GuildID: "g1", User: &discordgo.User{ID: "bot"}}},
	}
	if err := st.GuildAdd(g); err != nil {
		t.Fatal(err)
	}
	if err := st.ChannelAdd(&discordgo.Channel{ID: "c1", GuildID: "g1", Type: discordgo.ChannelTypeGuildText}); err != nil {
		t.Fatal(err)
	}
	s.State = st
	return s
}

func TestFeedPinningNeedsPinMessagesFromState(t *testing.T) {
	tests := []struct {
		name     string
		perms    int64
		wantREST bool
	}{
		{"Pin Messages", permissionPinMessages, true},
		{"Manage Messages only", discordgo.PermissionManageMessages, false},
		{"no permissions", 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tr := &pinTransport{}
			s := pinSession(t, tc.perms, tr)
			handleFeedPinning(s, "c1", "new", "anime")
			calls := tr.got()
			if !tc.wantREST {
				if len(calls) != 0 {
					t.Fatalf("REST calls = %v, want none", calls)
				}
				return
			}
			want := []string{"GET /api/v9/channels/c1/pins", "DELETE /api/v9/channels/c1/pins/old", "PUT /api/v9/channels/c1/pins/new"}
			if strings.Join(calls, "|") != strings.Join(want, "|") {
				t.Fatalf("REST calls = %v, want %v", calls, want)
			}
		})
	}
}

// A failed pin or unpin is logged, never panics and never stops the run.
func TestFeedPinningFailuresDoNotPanic(t *testing.T) {
	tr := &pinTransport{fail: true}
	s := pinSession(t, permissionPinMessages, tr)
	handleFeedPinning(s, "c1", "new", "anime")
}
