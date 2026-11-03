package commands

import (
	"encoding/json"
	"strings"
	"testing"

	"ReZeroTsu/internal/interaction"
	"ReZeroTsu/internal/interaction/interactiontest"

	"github.com/bwmarrin/discordgo"
)

// modalSubmitJSON is a form submission as Discord sends it: a text box and a role picker, each wrapped in a Label.
const modalSubmitJSON = `{
  "id": "1", "application_id": "2", "type": 5, "guild_id": "g", "channel_id": "chan",
  "member": {"user": {"id": "u"}},
  "data": {
    "custom_id": "t-real:1",
    "components": [
      {"type": 18, "id": 1, "component": {"type": 4, "id": 2, "custom_id": "body", "value": "hello there"}},
      {"type": 18, "id": 3, "component": {"type": 6, "id": 4, "custom_id": "role", "values": ["111", "222"]}}
    ]
  }
}`

func TestRealLabelWrappedSubmissionReachesTheHandler(t *testing.T) {
	var i discordgo.InteractionCreate
	if err := json.Unmarshal([]byte(modalSubmitJSON), &i); err != nil {
		t.Fatalf("discordgo cannot decode a Label-wrapped submission: %v", err)
	}

	var text string
	var picked []string
	registerComponent(t, "t-real:", func(ic *interaction.Context, _ *discordgo.Session) error {
		text, picked = ic.FormValue("body"), ic.FormValues("role")
		return nil
	})
	dispatchPress(t, &interactiontest.Fake{}, &i)

	if text != "hello there" {
		t.Errorf("text box = %q, want %q", text, "hello there")
	}
	if strings.Join(picked, ",") != "111,222" {
		t.Errorf("picker values = %v, want 111,222", picked)
	}
}
