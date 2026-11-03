package bot

import (
	"context"
	"errors"
	"fmt"
	"time"

	"ReZeroTsu/internal/discord"

	"github.com/bwmarrin/discordgo"
)

// Helpers for the send rules applied by the channel sender (channelsender.go).
//
// "sent" state is written on a context detached from shutdown with a short timeout (detachedWrite).

const (
	// verifyMessageLimit is how many recent channel messages are read when verifying an Unknown send.
	verifyMessageLimit = 25
	// verifyTimeout bounds the verification read; it runs detached from shutdown cancellation.
	verifyTimeout = 10 * time.Second
	// sentWriteTimeout bounds "sent" state writes made through detachedWrite.
	sentWriteTimeout = 5 * time.Second
)

// errSendNotAttempted marks a Send error returned before any request was made (the webhook limiter wait in
// SendBatchToChannel ended by the context). Only such errors skip verification; see sendNotAttempted.
var errSendNotAttempted = errors.New("send not attempted")

// notAttempted wraps err (a context error from a wait that precedes the request) as errSendNotAttempted while
// keeping it matchable as the original error.
func notAttempted(err error) error {
	return fmt.Errorf("%w: %w", errSendNotAttempted, err)
}

// sendNotAttempted reports whether a Send error says the request was never made: it is not verified and never
// falls back. Every other error, including a context error from an in-flight request's client timeout (which
// matches context.DeadlineExceeded even during shutdown), may hide a delivered message and stays Unknown.
func sendNotAttempted(err error) bool {
	return errors.Is(err, errSendNotAttempted)
}

// isKnownRejection reports whether err is a Rejected code that callers have terminal handling for.
// Channel-terminal codes come from ClassifyChannelTerminal, so a new terminal code only needs adding there.
func isKnownRejection(err error) bool {
	return discord.ClassifyChannelTerminal(err) != discord.ChannelNotTerminal ||
		discord.IsUnknownRole(err) || discord.IsUnknownMessage(err) || discord.IsCannotDMUser(err) || discord.IsWebhookGone(err)
}

// messageInRecent reports whether recent holds a message authored by the bot user (or the webhook used)
// whose embeds match every embed of msg by URL and description. Embed-less messages match on content.
func messageInRecent(recent []ChannelMessage, botUserID string, webhook *WebhookTarget, msg OutgoingMessage) bool {
	for _, m := range recent {
		if m.AuthorID == "" || !(m.AuthorID == botUserID || (webhook != nil && m.AuthorID == webhook.ID)) {
			continue
		}
		if len(msg.Embeds) == 0 {
			if m.Content == msg.Content {
				return true
			}
			continue
		}
		if embedsMatch(m.Embeds, msg.Embeds) {
			return true
		}
	}
	return false
}

func embedsMatch(have, want []*discordgo.MessageEmbed) bool {
	for _, w := range want {
		if w == nil {
			continue
		}
		found := false
		for _, h := range have {
			if h != nil && h.URL == w.URL && h.Description == w.Description {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// detachedWrite runs fn on a context detached from ctx's cancellation with a short timeout, so that "sent"
// state is still recorded during shutdown.
func detachedWrite(ctx context.Context, fn func(ctx context.Context) error) error {
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sentWriteTimeout)
	defer cancel()
	return fn(wctx)
}
