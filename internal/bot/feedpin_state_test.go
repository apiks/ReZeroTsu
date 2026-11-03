package bot

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"ReZeroTsu/internal/discord"
	"github.com/bwmarrin/discordgo"
)

// pinTransport records "METHOD path" for every REST request. The pinned list holds one older bot pin for the
// subreddit; every other call succeeds with an empty body.
type pinTransport struct {
	mu        sync.Mutex
	calls     []string
	failUnpin bool
	putErr    error
}

func (p *pinTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	p.mu.Lock()
	p.calls = append(p.calls, r.Method+" "+r.URL.Path)
	p.mu.Unlock()
	status, body := http.StatusNoContent, ""
	switch {
	case p.putErr != nil && r.Method == http.MethodPut:
		return nil, p.putErr
	case p.failUnpin && r.Method == http.MethodDelete:
		status, body = http.StatusForbidden, `{"code":50013,"message":"Missing Permissions"}`
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/pins"):
		status = http.StatusOK
		body = `{"items":[{"message":{"id":"old","author":{"id":"bot"},"embeds":[{"author":{"url":"https://www.reddit.com/r/anime/comments/1"}}]}}],"has_more":false}`
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
		{"Pin Messages", discord.PermissionPinMessages, true},
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
			want := []string{"GET /api/v9/channels/c1/messages/pins", "DELETE /api/v9/channels/c1/messages/pins/old", "PUT /api/v9/channels/c1/messages/pins/new"}
			if strings.Join(calls, "|") != strings.Join(want, "|") {
				t.Fatalf("REST calls = %v, want %v", calls, want)
			}
		})
	}
}

func TestFeedPinningAdministratorBotPins(t *testing.T) {
	tr := &pinTransport{}
	s := pinSession(t, discordgo.PermissionAdministrator, tr)
	handleFeedPinning(s, "c1", "new", "anime")
	if got := tr.got(); len(got) != 3 || got[2] != "PUT /api/v9/channels/c1/messages/pins/new" {
		t.Fatalf("REST calls = %v, want list, unpin and pin", got)
	}
}

// pinThreadSession adds parent text channel p1 carrying the given @everyone overwrite and thread t1 under it.
func pinThreadSession(t *testing.T, base int64, ow discordgo.PermissionOverwrite, tr *pinTransport) *discordgo.Session {
	t.Helper()
	s := pinSession(t, base, tr)
	ow.ID, ow.Type = "g1", discordgo.PermissionOverwriteTypeRole
	if err := s.State.ChannelAdd(&discordgo.Channel{
		ID: "p1", GuildID: "g1", Type: discordgo.ChannelTypeGuildText,
		PermissionOverwrites: []*discordgo.PermissionOverwrite{&ow},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.State.ChannelAdd(&discordgo.Channel{ID: "t1", GuildID: "g1", ParentID: "p1", Type: discordgo.ChannelTypeGuildPublicThread}); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestFeedPinningInThreadUsesParentOverwrites(t *testing.T) {
	t.Run("parent grants Pin Messages", func(t *testing.T) {
		tr := &pinTransport{}
		s := pinThreadSession(t, 0, discordgo.PermissionOverwrite{Allow: discord.PermissionPinMessages}, tr)
		handleFeedPinning(s, "t1", "new", "anime")
		if got := tr.got(); len(got) != 3 || got[2] != "PUT /api/v9/channels/t1/messages/pins/new" {
			t.Fatalf("REST calls = %v, want list, unpin and pin in the thread", got)
		}
	})
	t.Run("parent denies Pin Messages", func(t *testing.T) {
		tr := &pinTransport{}
		s := pinThreadSession(t, discord.PermissionPinMessages, discordgo.PermissionOverwrite{Deny: discord.PermissionPinMessages}, tr)
		handleFeedPinning(s, "t1", "new", "anime")
		if got := tr.got(); len(got) != 0 {
			t.Fatalf("REST calls = %v, want none", got)
		}
	})
	t.Run("thread missing from state", func(t *testing.T) {
		lb := captureLogs(t)
		tr := &pinTransport{}
		s := pinSession(t, discord.PermissionPinMessages, tr)
		handleFeedPinning(s, "gone", "new", "anime")
		if got := tr.got(); len(got) != 0 {
			t.Fatalf("REST calls = %v, want none", got)
		}
		if lb.countContaining("DEBUG", "feed pin skipped") != 1 {
			t.Fatalf("want one debug skip log, logs:\n%s", lb.buf.String())
		}
	})
}

// A failed unpin is logged with its error and the pin is still attempted; a failed pin with a non-REST error logs
// its cause too. Neither panics or returns an error to the delivery run.
func TestFeedPinningFailuresAreLoggedAndPinStillAttempted(t *testing.T) {
	lb := captureLogs(t)
	tr := &pinTransport{failUnpin: true, putErr: errors.New("pin timed out")}
	s := pinSession(t, discord.PermissionPinMessages, tr)
	handleFeedPinning(s, "c1", "new", "anime")
	want := []string{"GET /api/v9/channels/c1/messages/pins", "DELETE /api/v9/channels/c1/messages/pins/old", "PUT /api/v9/channels/c1/messages/pins/new"}
	if strings.Join(tr.got(), "|") != strings.Join(want, "|") {
		t.Fatalf("REST calls = %v, want %v", tr.got(), want)
	}
	if lb.countContaining("WARN", "feed unpin failed") != 1 {
		t.Fatalf("want one unpin warning, logs:\n%s", lb.buf.String())
	}
	if lb.countContaining("WARN", "pin timed out") != 1 {
		t.Fatalf("want the pin warning to carry the timeout cause, logs:\n%s", lb.buf.String())
	}
}
