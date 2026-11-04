package settings

import (
	"context"
	"strings"
	"testing"

	"ReZeroTsu/internal/animeschedule"
	"ReZeroTsu/internal/database/guilds"
	"ReZeroTsu/internal/interaction/interactiontest"

	"github.com/bwmarrin/discordgo"
)

func (f *fakeStore) SetPreferredTrack(_ context.Context, _ string, pref animeschedule.PreferredTrack) error {
	return f.write(func(gs *guilds.GuildSettings) { gs.PreferredTrack = pref.String() })
}

var wantFallback = map[string]string{"Sub": "(sub, else raw).", "Dub": "(dub, else sub, else raw).", "Raw": "(raw only)."}

func TestPanelShowsThePreferredTrackOnItsButtonAndInItsText(t *testing.T) {
	for have, name := range map[string]string{"": "Sub", "sub": "Sub", "dub": "Dub", "raw": "Raw"} {
		line := "**Preferred track:** " + name + " " + wantFallback[name] + " Used by /schedule and both autoposts."
		gs := stored()
		gs.PreferredTrack = have
		useStore(t, &fakeStore{gs: gs})
		fake := &interactiontest.Fake{}
		c := settingsCommand(t)
		ic := interactiontest.NewCommandContext(fake, c.Visibility, c.Response, discordgo.ApplicationCommandInteractionData{Name: "settings"})
		if err := c.Run(ic); err != nil {
			t.Fatal(err)
		}
		desc, _, buttons := panelOf(t, lastCall(t, fake).Response)
		if !strings.Contains(desc, line) {
			t.Errorf("stored %q: panel %q lacks the line %q", have, desc, line)
		}
		if len(buttons) != 4 || buttons[0].Label == "" || buttons[1].Label != "Preferred track: "+name || buttons[2].Label != "Edit ping message" {
			t.Errorf("stored %q: buttons = %+v, want Donghua, Preferred track: %s, Edit ping message, Preferred name", have, buttons, name)
		}
	}
}

func TestPreferredTrackButtonCyclesSubDubRawSubAndSavesOnlyThatField(t *testing.T) {
	st := &fakeStore{gs: stored()}
	useStore(t, st)
	before := *st.gs
	for i, want := range []string{"dub", "raw", "sub"} {
		fake := &interactiontest.Fake{}
		if err := click(t, fake, moderator, idTrack); err != nil {
			t.Fatal(err)
		}
		got := st.gs.PreferredTrack
		if (want == "sub" && got != "sub" && got != "") || (want != "sub" && got != want) {
			t.Errorf("press %d: stored track = %q, want %q", i+1, got, want)
		}
		if st.sets != i+1 {
			t.Errorf("press %d: %d writes, want one per press", i+1, st.sets)
		}
		call := lastCall(t, fake)
		if call.Op != "UpdateMessage" {
			t.Fatalf("last call = %s, want UpdateMessage", call.Op)
		}
		desc, _, buttons := panelOf(t, call.Response)
		name := strings.ToUpper(want[:1]) + want[1:]
		if !strings.Contains(desc, "**Preferred track:** "+name+" "+wantFallback[name]+" Used by /schedule and both autoposts.") || buttons[1].Label != "Preferred track: "+name {
			t.Errorf("press %d: panel %q / %q does not show %s", i+1, desc, buttons[1].Label, name)
		}
	}
	after := *st.gs
	after.PreferredTrack = before.PreferredTrack
	if after.Donghua != before.Donghua || after.PingMessage != before.PingMessage || after.BotLogID != before.BotLogID {
		t.Errorf("other settings changed: %+v", st.gs)
	}
}
