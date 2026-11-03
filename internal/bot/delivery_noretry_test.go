package bot

import (
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/bwmarrin/discordgo"
)

// statusTransport answers every REST request with one status and counts the requests.
type statusTransport struct {
	status int
	n      atomic.Int32
}

func (c *statusTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c.n.Add(1)
	return &http.Response{
		StatusCode: c.status, Status: http.StatusText(c.status), Header: http.Header{"Content-Type": {"application/json"}}, Request: r,
		Body: io.NopCloser(strings.NewReader(`{}`)),
	}, nil
}

// The pinned discordgo retries a 500, 503 or 504 up to MaxRestRetries times. A delivery send must reach Discord
// once: the send rules own the decision to resend, and a hidden resend after a 504 on a stored message posts a
// duplicate.
func TestDeliverySendsAreNeverRetriedByTheLibrary(t *testing.T) {
	for _, status := range []int{http.StatusInternalServerError, http.StatusServiceUnavailable, http.StatusGatewayTimeout} {
		for _, name := range []string{"channel", "webhook", "webhook thread"} {
			t.Run(http.StatusText(status)+" "+name, func(t *testing.T) {
				s, err := discordgo.New("Bot test")
				if err != nil {
					t.Fatal(err)
				}
				tr := &statusTransport{status: status}
				s.Client = &http.Client{Transport: tr}
				p := newDiscordgoPort(s)
				var target *WebhookTarget
				switch name {
				case "webhook":
					target = &WebhookTarget{ID: "w", Token: "t"}
				case "webhook thread":
					target = &WebhookTarget{ID: "w", Token: "t", ThreadID: "th"}
				}
				if _, err := p.Send(t.Context(), "c1", target, OutgoingMessage{Content: "hi"}); err == nil {
					t.Fatal("want the failure surfaced")
				}
				if got := tr.n.Load(); got != 1 {
					t.Errorf("requests = %d, want exactly 1", got)
				}
			})
		}
	}
}
