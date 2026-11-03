package interaction_test

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"ReZeroTsu/internal/discord"
	"ReZeroTsu/internal/interaction"
	"ReZeroTsu/internal/interaction/interactiontest"

	"github.com/bwmarrin/discordgo"
)

func restErr(status, code int) error {
	return &discordgo.RESTError{
		Response: &http.Response{StatusCode: status},
		Message:  &discordgo.APIErrorMessage{Code: code, Message: "raw discord text"},
	}
}

func TestExplainError(t *testing.T) {
	prune := interaction.ErrorContext{
		ChannelID: "123",
		Needs:     discordgo.PermissionViewChannel | discordgo.PermissionReadMessageHistory | discordgo.PermissionManageMessages,
	}
	tests := []struct {
		name  string
		err   error
		at    interaction.ErrorContext
		want  string
		known bool
	}{
		{
			name: "missing permissions names the channel and every needed permission",
			err:  restErr(403, discord.MissingPermissionsCode), at: prune, known: true,
			want: "I can't do that in <#123>. I need View Channel, Read Message History and Manage Messages there.",
		},
		{
			name: "missing permissions with two needs",
			err:  restErr(403, discord.MissingPermissionsCode),
			at:   interaction.ErrorContext{ChannelID: "9", Needs: discordgo.PermissionViewChannel | discordgo.PermissionSendMessages}, known: true,
			want: "I can't do that in <#9>. I need View Channel and Send Messages there.",
		},
		{
			name: "a bare 403 is missing permissions too",
			err:  &discordgo.RESTError{Response: &http.Response{StatusCode: 403}},
			at:   interaction.ErrorContext{ChannelID: "9", Needs: discordgo.PermissionManageMessages}, known: true,
			want: "I can't do that in <#9>. I need Manage Messages there.",
		},
		{
			name: "wrapped error is still recognised",
			err:  fmt.Errorf("delete: %w", restErr(403, discord.MissingPermissionsCode)),
			at:   interaction.ErrorContext{ChannelID: "9", Needs: discordgo.PermissionManageMessages}, known: true,
			want: "I can't do that in <#9>. I need Manage Messages there.",
		},
		{
			name: "missing permissions without needs still names the channel",
			err:  restErr(403, discord.MissingPermissionsCode),
			at:   interaction.ErrorContext{ChannelID: "9"}, known: true,
			want: "I can't do that in <#9>. I am missing a permission there.",
		},
		{
			name: "missing permissions without a channel",
			err:  restErr(403, discord.MissingPermissionsCode), known: true,
			want: "I can't do that here. I am missing a permission.",
		},
		{
			name: "missing access asks for View Channel",
			err:  restErr(403, discord.MissingAccessCode),
			at:   interaction.ErrorContext{ChannelID: "9"}, known: true,
			want: "I can't see <#9>. Give me View Channel there and try again.",
		},
		{
			name: "missing access without a channel",
			err:  restErr(403, discord.MissingAccessCode), known: true,
			want: "I can't see that. Give me access to it and try again.",
		},
		{
			name: "unknown channel", err: restErr(404, discord.UnknownChannelCode), known: true,
			want: "That channel no longer exists. Pick another one.",
		},
		{
			name: "unknown message", err: restErr(404, discord.UnknownMessageCode), known: true,
			want: "That message no longer exists.",
		},
		{
			name: "unknown role", err: restErr(404, discord.UnknownRoleCode), known: true,
			want: "That role no longer exists. Pick another one.",
		},
		{
			name: "cannot DM user", err: restErr(400, discord.CannotDMUserCode), known: true,
			want: "I can't send that person a direct message. They may have direct messages from server members turned off.",
		},
		{
			name: "unexpected REST error falls back and hides the raw text",
			err:  restErr(500, 0), want: "Something went wrong.", known: false,
		},
		{
			name: "plain error falls back", err: errors.New("boom"), want: "Something went wrong.", known: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, known := interaction.ExplainError(tt.err, tt.at)
			if got != tt.want || known != tt.known {
				t.Fatalf("interaction.ExplainError() = %q, %v; want %q, %v", got, known, tt.want, tt.known)
			}
		})
	}
}

func TestFailRefusesPrivatelyInPlainLanguage(t *testing.T) {
	fake := &interactiontest.Fake{}
	ic := interaction.NewContext(fake, &discordgo.Interaction{}, nil, interaction.Public, interaction.Deferred)
	if err := ic.Defer(0); err != nil {
		t.Fatal(err)
	}
	err := ic.Fail(restErr(403, discord.MissingPermissionsCode), interaction.ErrorContext{ChannelID: "7", Needs: discordgo.PermissionManageMessages})
	if err != nil {
		t.Fatal(err)
	}
	calls := fake.Calls()
	// A public placeholder is removed and the sentence follows up privately.
	if got := fake.Ops(); len(got) != 3 || got[1] != "DeleteReply" || got[2] != "FollowUp" {
		t.Fatalf("ops = %v, want Defer, DeleteReply, FollowUp", got)
	}
	last := calls[2]
	if last.Visibility != interaction.Private || last.Response.Content != "I can't do that in <#7>. I need Manage Messages there." {
		t.Fatalf("follow-up = %+v", last)
	}
}

func TestFailUnexpectedErrorSaysSomethingWentWrong(t *testing.T) {
	fake := &interactiontest.Fake{}
	ic := interaction.NewContext(fake, &discordgo.Interaction{}, nil, interaction.Private, interaction.Direct)
	if err := ic.Fail(restErr(500, 0), interaction.ErrorContext{}); err != nil {
		t.Fatal(err)
	}
	calls := fake.Calls()
	if len(calls) != 1 || calls[0].Op != "Reply" || calls[0].Visibility != interaction.Private || calls[0].Response.Content != "Something went wrong." {
		t.Fatalf("calls = %+v", calls)
	}
}
