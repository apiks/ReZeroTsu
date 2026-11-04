package settings

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"ReZeroTsu/internal/animeschedule"
	"ReZeroTsu/internal/database/guilds"
	"ReZeroTsu/internal/interaction/interactiontest"

	"github.com/bwmarrin/discordgo"
)

func (f *fakeStore) SetPreferredName(_ context.Context, _ string, name animeschedule.PreferredName) error {
	return f.write(func(gs *guilds.GuildSettings) { gs.PreferredName = name.String() })
}

// wantNameLine is the panel's preferred-name line for the value shown on the button.
var wantNameLine = map[string]string{
	"Title":   "**Preferred name:** Title. Used by /schedule and both autoposts.",
	"Romaji":  "**Preferred name:** Romaji (else the title). Used by /schedule and both autoposts.",
	"English": "**Preferred name:** English (else the title). Used by /schedule and both autoposts.",
	"Native":  "**Preferred name:** Native (else the title). Used by /schedule and both autoposts.",
}

// nameButton is the panel's preferred-name button: the fourth on the last row.
func nameButton(t *testing.T, buttons []discordgo.Button) discordgo.Button {
	t.Helper()
	if len(buttons) != 4 {
		t.Fatalf("last row has %d buttons, want 4", len(buttons))
	}
	return buttons[3]
}

// /help lists /settings by its description, so the description names the preferred name.
func TestSettingsDescriptionInHelpMentionsThePreferredName(t *testing.T) {
	if desc := settingsCommand(t).Desc; !strings.Contains(desc, "preferred name") {
		t.Errorf("/settings description %q does not mention the preferred name", desc)
	}
}

func TestPanelShowsThePreferredNameOnItsButtonAndInItsText(t *testing.T) {
	for have, name := range map[string]string{"": "Title", "title": "Title", "romaji": "Romaji", "english": "English", "native": "Native", "junk": "Title"} {
		gs := stored()
		gs.PreferredName = have
		useStore(t, &fakeStore{gs: gs})
		fake := &interactiontest.Fake{}
		c := settingsCommand(t)
		ic := interactiontest.NewCommandContext(fake, c.Visibility, c.Response, discordgo.ApplicationCommandInteractionData{Name: "settings"})
		if err := c.Run(ic); err != nil {
			t.Fatal(err)
		}
		desc, _, buttons := panelOf(t, lastCall(t, fake).Response)
		if !strings.Contains(desc, wantNameLine[name]) {
			t.Errorf("stored %q: panel %q lacks the line %q", have, desc, wantNameLine[name])
		}
		if got := nameButton(t, buttons).Label; got != "Preferred name: "+name {
			t.Errorf("stored %q: button = %q, want %q", have, got, "Preferred name: "+name)
		}
	}
}

func TestPreferredNameButtonCyclesThroughAllFourAndBackAndSavesOnlyThatField(t *testing.T) {
	gs := stored()
	gs.PreferredTrack = "dub"
	st := &fakeStore{gs: gs, autoposts: map[string]*guilds.Autopost{
		postTypeNewEpisodes: {PostType: postTypeNewEpisodes, Name: "eps", ID: "222"},
	}}
	useStore(t, st)
	before := *st.gs
	for i, want := range []string{"romaji", "english", "native", "title"} {
		fake := &interactiontest.Fake{}
		if err := click(t, fake, moderator, idName); err != nil {
			t.Fatal(err)
		}
		if got := st.gs.PreferredName; got != want {
			t.Errorf("press %d: stored preferred name = %q, want %q", i+1, got, want)
		}
		if st.sets != i+1 {
			t.Errorf("press %d: %d writes, want one per press", i+1, st.sets)
		}
		call := lastCall(t, fake)
		if call.Op != "UpdateMessage" {
			t.Fatalf("press %d: last call = %s, want UpdateMessage", i+1, call.Op)
		}
		desc, _, buttons := panelOf(t, call.Response)
		name := strings.ToUpper(want[:1]) + want[1:]
		if !strings.Contains(desc, wantNameLine[name]) || nameButton(t, buttons).Label != "Preferred name: "+name {
			t.Errorf("press %d: panel %q / %q does not show %s", i+1, desc, nameButton(t, buttons).Label, name)
		}
	}
	after := *st.gs
	after.PreferredName = before.PreferredName
	if !reflect.DeepEqual(after, before) {
		t.Errorf("other settings changed: %+v, want %+v", after, before)
	}
	if ap := st.autoposts[postTypeNewEpisodes]; ap == nil || ap.ID != "222" || len(st.deletedSentState) != 0 {
		t.Errorf("autopost or sent state changed: %+v, deleted %v", ap, st.deletedSentState)
	}
}
