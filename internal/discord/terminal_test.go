package discord

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
)

func TestClassifyChannelTerminal(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want ChannelTerminalReason
	}{
		{"nil", nil, ChannelNotTerminal},
		{"plain error", errors.New("boom"), ChannelNotTerminal},
		{"unknown channel", restErr(http.StatusNotFound, UnknownChannelCode), ChannelDeleted},
		{"unknown channel sent with 403", restErr(http.StatusForbidden, UnknownChannelCode), ChannelDeleted},
		{"missing access", restErr(http.StatusForbidden, MissingAccessCode), ChannelInaccessible},
		{"non-text channel", restErr(http.StatusBadRequest, ChannelNotTextCode), ChannelNotText},
		{"non-text channel sent with 403", restErr(http.StatusForbidden, ChannelNotTextCode), ChannelNotText},
		{"missing permissions", restErr(http.StatusForbidden, MissingPermissionsCode), ChannelMissingPermissions},
		{"any other 403", restErr(http.StatusForbidden, 99999), ChannelMissingPermissions},
		{"wrapped missing access", fmt.Errorf("send: %w", restErr(http.StatusForbidden, MissingAccessCode)), ChannelInaccessible},
		{"unknown webhook", restErr(http.StatusNotFound, UnknownWebhookCode), ChannelNotTerminal},
		{"other 400", restErr(http.StatusBadRequest, 99999), ChannelNotTerminal},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyChannelTerminal(tc.err); got != tc.want {
				t.Errorf("ClassifyChannelTerminal = %v, want %v", got, tc.want)
			}
		})
	}
}
