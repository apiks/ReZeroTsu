package discord

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/bwmarrin/discordgo"
)

func restErr(status, code int) error {
	return &discordgo.RESTError{
		Response: &http.Response{StatusCode: status},
		Message:  &discordgo.APIErrorMessage{Code: code, Message: "x"},
	}
}

func TestClassifySend(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want SendOutcome
	}{
		{"nil is sent", nil, Sent},
		{"4xx REST error is rejected", restErr(404, UnknownChannelCode), Rejected},
		{"403 without body is rejected", &discordgo.RESTError{Response: &http.Response{StatusCode: 403}}, Rejected},
		{"wrapped 4xx is rejected", fmt.Errorf("send: %w", restErr(403, MissingPermissionsCode)), Rejected},
		{"5xx REST error is unknown", restErr(502, 0), Unknown},
		{"context cancelled is unknown", context.Canceled, Unknown},
		{"context deadline is unknown", fmt.Errorf("x: %w", context.DeadlineExceeded), Unknown},
		{"URL timeout is unknown", &url.Error{Op: "Post", URL: "https://discord.com", Err: context.DeadlineExceeded}, Unknown},
		{"plain error is unknown", errors.New("boom"), Unknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ClassifySend(tt.err); got != tt.want {
				t.Fatalf("ClassifySend(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
