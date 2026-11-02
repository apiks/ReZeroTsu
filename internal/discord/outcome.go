package discord

import (
	"errors"
	"net/http"

	"github.com/bwmarrin/discordgo"
)

// SendOutcome is what a send error tells us about whether Discord accepted the message.
type SendOutcome int

const (
	// Sent means the send succeeded (nil error).
	Sent SendOutcome = iota
	// Rejected means Discord returned a 4xx: nothing was delivered.
	Rejected
	// Unknown means we cannot tell whether the message was delivered: context
	// cancellation or deadline, network errors, client timeouts, 5xx responses,
	// 429s and discordgo's exhausted-retry errors.
	Unknown
)

func (o SendOutcome) String() string {
	switch o {
	case Sent:
		return "sent"
	case Rejected:
		return "rejected"
	default:
		return "unknown"
	}
}

// ClassifySend maps any send error to exactly one SendOutcome.
// Only an explicit Discord 4xx response (other than 429) is Rejected; everything else that is not nil is Unknown.
func ClassifySend(err error) SendOutcome {
	if err == nil {
		return Sent
	}
	var restErr *discordgo.RESTError
	if errors.As(err, &restErr) && restErr.Response != nil {
		status := restErr.Response.StatusCode
		if status >= 400 && status < 500 && status != http.StatusTooManyRequests {
			return Rejected
		}
	}
	return Unknown
}
