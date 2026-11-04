package bot

import (
	"context"
	"strings"
	"testing"
	"time"

	"ReZeroTsu/internal/animeschedule"
	"ReZeroTsu/internal/commands/schedule"
	"ReZeroTsu/internal/database/guilds"
)

// The autopost reads the server's preferred track: the page it builds and its footer follow it, the title does not.
func TestDailyScheduleBodyAndFooterFollowTheServersPreferredTrack(t *testing.T) {
	for _, tc := range []struct {
		stored string
		want   animeschedule.PreferredTrack
		footer string
	}{
		{"", animeschedule.PreferSub, ""},
		{"dub", animeschedule.PreferDub, "Dub preferred"},
		{"raw", animeschedule.PreferRaw, "Raw preferred"},
	} {
		h := newScheduleHarness()
		t.Cleanup(animeschedule.SetClock(func() time.Time { return h.now }))
		h.st.Settings = map[string]*guilds.GuildSettings{"g1": {Donghua: true, PreferredTrack: tc.stored}}
		var asked []animeschedule.PreferredTrack
		inner := h.fn
		h.fn = func(donghua bool, pref animeschedule.PreferredTrack) []animeschedule.ShowEntry {
			asked = append(asked, pref)
			return inner(donghua, pref)
		}
		if err := h.run(context.Background()); err != nil {
			t.Fatal(err)
		}
		if len(asked) != 1 || asked[0] != tc.want {
			t.Errorf("stored %q: day read under %v, want %v", tc.stored, asked, tc.want)
		}
		got := h.fd.deliveredTo("sch")
		if len(got) != 1 {
			t.Fatalf("stored %q: delivered %d posts, want 1", tc.stored, len(got))
		}
		emb := got[0].Msg.Embeds[0]
		want := schedule.BuildScheduleEmbed(h.now, h.now, inner(true, tc.want), tc.want, animeschedule.PreferTitle)
		if emb.Footer.Text != want.Footer.Text || emb.Description != want.Description || emb.Title != want.Title {
			t.Errorf("stored %q: posted %q / %q, want %q / %q", tc.stored, emb.Description, emb.Footer.Text, want.Description, want.Footer.Text)
		}
		if tc.footer != "" && !strings.Contains(emb.Footer.Text, tc.footer) {
			t.Errorf("stored %q: footer %q lacks %q", tc.stored, emb.Footer.Text, tc.footer)
		}
		if tc.footer == "" && strings.Contains(emb.Footer.Text, "preferred") {
			t.Errorf("stored %q: footer %q names a preference", tc.stored, emb.Footer.Text)
		}
	}
}
