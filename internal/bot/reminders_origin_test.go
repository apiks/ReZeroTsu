package bot

import (
	"context"
	"strings"
	"testing"

	"ReZeroTsu/internal/database/reminders"
)

func TestProcessReminderSlice_DMSaysWhereTheReminderWasSet(t *testing.T) {
	tests := []struct {
		name     string
		origin   reminders.Origin
		wantLine string // empty: the DM must carry no origin line
	}{
		{name: "set in a server channel", origin: reminders.OriginServer, wantLine: "Set in <#chan-9>"},
		{name: "set in DMs", origin: reminders.OriginDM, wantLine: "Set in DMs"},
		{name: "legacy reminder without an origin gets no line", origin: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := newFakeDiscord()
			r := dueReminder(1, "water the plants")
			r.CommandChannel = "chan-9"
			r.Origin = tc.origin
			st := stateWith("u1", r)
			if err := runSlice(t, context.Background(), d, st, "u1"); err != nil {
				t.Fatal(err)
			}
			if len(d.Delivered) != 1 || len(d.Delivered[0].Msg.Embeds) != 1 {
				t.Fatalf("delivered = %+v, want one embed", d.Delivered)
			}
			desc := d.Delivered[0].Msg.Embeds[0].Description
			if !strings.Contains(desc, "water the plants") {
				t.Errorf("description = %q, lost the message", desc)
			}
			if tc.wantLine == "" {
				if strings.Contains(desc, "Set in") {
					t.Errorf("description = %q, want no origin line", desc)
				}
				return
			}
			if !strings.Contains(desc, tc.wantLine) {
				t.Errorf("description = %q, want it to contain %q", desc, tc.wantLine)
			}
		})
	}
}
