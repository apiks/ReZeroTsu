package commands

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"ReZeroTsu/internal/database"

	"github.com/bwmarrin/discordgo"
)

// recorder is an http.RoundTripper that records the content of every message the session posts.
type recorder struct {
	mu   sync.Mutex
	sent []string
}

func (r *recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method == http.MethodPost && req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		var body struct {
			Content string `json:"content"`
		}
		_ = json.Unmarshal(b, &body)
		r.mu.Lock()
		r.sent = append(r.sent, body.Content)
		r.mu.Unlock()
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"id":"1"}`)), Request: req}, nil
}

func fakeSession(rec *recorder) *discordgo.Session {
	s, _ := discordgo.New("Bot test")
	s.Client = &http.Client{Transport: rec}
	s.MaxRestRetries = 0
	return s
}

func dm(authorID, content string) *discordgo.MessageCreate {
	return &discordgo.MessageCreate{Message: &discordgo.Message{Author: &discordgo.User{ID: authorID}, ChannelID: "c", Content: content}}
}

// registerOwnerCommand registers a stand-in owner command (owner only, DMs only like the real ones) and
// reports whether its handler ran.
func registerOwnerCommand(t *testing.T, name string) *bool {
	t.Helper()
	ran := new(bool)
	AddMessageCommand(&MessageCommand{
		Name:       name,
		Permission: PermOwner,
		Context:    ContextDMOnly,
		MessageHandler: func(context.Context, *database.Client, *discordgo.Session, *discordgo.MessageCreate, string, BotRuntime) {
			*ran = true
		},
	})
	t.Cleanup(func() { delete(messageRegistry, name) })
	return ran
}

func handle(m *discordgo.MessageCreate, rec *recorder) bool {
	return HandleMessageCommand(context.Background(), nil, fakeSession(rec), m, "owner", []string{".", "!"}, nil)
}

func TestPrefixedDMNamingNoOwnerCommandGetsExactlyTheHint(t *testing.T) {
	registerOwnerCommand(t, "ownercmd")
	for _, content := range []string{".schedule", "!sub frieren", ".  remind 5m x", ". ownercmdx"} {
		rec := &recorder{}
		if !handle(dm("user", content), rec) {
			t.Errorf("%q: handled = false, want true", content)
		}
		if len(rec.sent) != 1 || rec.sent[0] != PrefixHint {
			t.Errorf("%q: sent %q, want exactly the hint", content, rec.sent)
		}
	}
	if PrefixHint != "Prefix commands were replaced by slash commands. Type / to see them." {
		t.Errorf("hint = %q", PrefixHint)
	}
}

func TestOwnerCommandsStillWorkAndGetNoHint(t *testing.T) {
	ran := registerOwnerCommand(t, "ownercmd")
	rec := &recorder{}
	if !handle(dm("owner", ".ownercmd"), rec) || !*ran {
		t.Fatalf("owner command did not run")
	}
	if len(rec.sent) != 0 {
		t.Errorf("sent %q, want nothing", rec.sent)
	}
}

func TestOwnerCommandKeepsItsPermissionAndContextGuards(t *testing.T) {
	ran := registerOwnerCommand(t, "ownercmd")

	rec := &recorder{}
	handle(dm("someone", ".ownercmd"), rec)
	if *ran || len(rec.sent) != 1 || rec.sent[0] != "Only the bot owner can use this command." {
		t.Errorf("non-owner: ran=%v sent=%q, want the owner-only refusal", *ran, rec.sent)
	}

	rec = &recorder{}
	m := dm("owner", ".ownercmd")
	m.GuildID = "g"
	handle(m, rec)
	if *ran || len(rec.sent) != 1 || rec.sent[0] != "This command can only be used in DMs." {
		t.Errorf("owner in a server: ran=%v sent=%q, want the DM-only refusal", *ran, rec.sent)
	}
}

func TestHintOnlyFiresForAPrefixedDM(t *testing.T) {
	registerOwnerCommand(t, "ownercmd")
	guild := dm("user", ".schedule")
	guild.GuildID = "g"
	bot := dm("user", ".schedule")
	bot.Author.Bot = true
	cases := map[string]*discordgo.MessageCreate{
		"prefixed message in a server": guild,
		"bot author":                   bot,
		"no prefix":                    dm("user", "schedule"),
		"bare prefix":                  dm("user", "."),
		"empty":                        dm("user", "   "),
	}
	for name, m := range cases {
		rec := &recorder{}
		if handle(m, rec) || len(rec.sent) != 0 {
			t.Errorf("%s: handled or sent %q, want neither", name, rec.sent)
		}
	}
}
