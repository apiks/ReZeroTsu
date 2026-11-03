package schedule

import (
	"context"
	"strings"
	"testing"

	"ReZeroTsu/internal/animeschedule"
	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/interaction"
	"ReZeroTsu/internal/interaction/interactiontest"

	"github.com/bwmarrin/discordgo"
)

// seedFrieren seeds Wednesday 2026-11-04 with a raw, a sub and a dub row of one show (the dub skips next week).
func seedFrieren(t *testing.T) {
	t.Helper()
	setClock(t)
	row := func(track animeschedule.Track, ep string, air int64) animeschedule.ShowEntry {
		return animeschedule.ShowEntry{Name: "Frieren", Route: "frieren", Episode: ep, AirType: track, AirTimeUnix: air}
	}
	t.Cleanup(animeschedule.Seed(map[string][]animeschedule.ShowEntry{
		"2026-11-03": {row(animeschedule.TrackDub, "Ep 2", 1700000000)},
		"2026-11-04": {row(animeschedule.TrackRaw, "Ep 5", 1700000100), row(animeschedule.TrackSub, "Ep 5", 1700000200)},
	}))
	animeschedule.SetAPIKey("key")
	t.Cleanup(func() { animeschedule.SetAPIKey("") })
}

func runWithTrack(t *testing.T, day, track string) interaction.Response {
	t.Helper()
	fake := &interactiontest.Fake{}
	data := discordgo.ApplicationCommandInteractionData{Name: "schedule"}
	if day != "" {
		data.Options = append(data.Options, &discordgo.ApplicationCommandInteractionDataOption{Name: "day", Type: discordgo.ApplicationCommandOptionString, Value: day})
	}
	if track != "" {
		data.Options = append(data.Options, &discordgo.ApplicationCommandInteractionDataOption{Name: "track", Type: discordgo.ApplicationCommandOptionString, Value: track})
	}
	i := &discordgo.Interaction{Type: discordgo.InteractionApplicationCommand, ChannelID: "chan", Data: data, User: &discordgo.User{ID: "user"}}
	ic := interaction.NewContext(fake, i, (*database.Client)(nil), interaction.Public, interaction.Deferred)
	ic.Ctx = context.Background()
	if err := ic.Defer(interaction.Public); err != nil {
		t.Fatal(err)
	}
	if err := handleSchedule(ic); err != nil {
		t.Fatal(err)
	}
	calls := fake.Calls()
	return calls[len(calls)-1].Response
}

func TestScheduleTrackOptionPicksTheViewLabelsLinesAndNamesThePreferenceInTheFooter(t *testing.T) {
	seedFrieren(t)
	for _, tc := range []struct {
		track, day, wantLine, wantFooter string
	}{
		{"", "tuesday", "", "0 shows · days run on UTC, times show in your time zone"},
		{"", "wednesday", "Ep 5 SUB", "1 show · days run on UTC, times show in your time zone"},
		{"sub", "wednesday", "Ep 5 SUB", "1 show · days run on UTC, times show in your time zone"},
		{"dub", "2026-11-03", "Ep 2 DUB", "1 show · Dub preferred · days run on UTC, times show in your time zone"},
		{"dub", "wednesday", "", "0 shows · Dub preferred · days run on UTC, times show in your time zone"},
		{"raw", "wednesday", "Ep 5 RAW", "1 show · Raw preferred · days run on UTC, times show in your time zone"},
	} {
		emb := runWithTrack(t, tc.day, tc.track).Embeds[0]
		if tc.wantLine != "" && !strings.Contains(emb.Description, tc.wantLine) {
			t.Errorf("track %q day %s: description %q lacks %q", tc.track, tc.day, emb.Description, tc.wantLine)
		}
		if emb.Footer.Text != tc.wantFooter {
			t.Errorf("track %q day %s: footer = %q, want %q", tc.track, tc.day, emb.Footer.Text, tc.wantFooter)
		}
	}
}

func TestScheduleTrackOptionIsOptionalWithSubDubAndRawChoices(t *testing.T) {
	var opt *discordgo.ApplicationCommandOption
	for _, o := range findCommand(t).Options {
		if o.Name == "track" {
			opt = o
		}
	}
	if opt == nil || opt.Required || len(opt.Choices) != 3 {
		t.Fatalf("track option = %+v, want optional with three choices", opt)
	}
	for i, want := range []string{"Sub", "Dub", "Raw"} {
		if opt.Choices[i].Name != want {
			t.Errorf("choice %d = %q, want %q", i, opt.Choices[i].Name, want)
		}
	}
}

func TestPageControlsKeepTheChosenTrack(t *testing.T) {
	seedFrieren(t)
	r := runWithTrack(t, "wednesday", "dub")
	picker := pickerOf(t, r)
	if picker.CustomID != "schedpick:user:dub" {
		t.Errorf("picker custom ID = %q", picker.CustomID)
	}
	prev, next := buttons(t, r)
	if prev.CustomID != "sched:2026-11-03:user:dub" || next.CustomID != "sched:2026-11-05:user:dub" {
		t.Errorf("buttons = %q, %q, want the dub track carried", prev.CustomID, next.CustomID)
	}

	// A press renders the target day under the carried track, for the author and for another member's copy.
	for _, user := range []string{"user", "stranger"} {
		calls := pressNav(t, prev.CustomID, user, publicMsg())
		resp := calls[0].Response
		if !strings.Contains(resp.Embeds[0].Description, "Ep 2 DUB") || !strings.Contains(resp.Embeds[0].Footer.Text, "Dub preferred") {
			t.Errorf("%s press = %q / %q, want the dub view", user, resp.Embeds[0].Description, resp.Embeds[0].Footer.Text)
		}
		_, n := buttons(t, resp)
		if !strings.HasSuffix(n.CustomID, ":dub") {
			t.Errorf("%s's page buttons %q lost the track", user, n.CustomID)
		}
	}

	picked := choosePick(t, picker.CustomID, "2026-11-03", "user", publicMsg())
	if emb := picked[0].Response.Embeds[0]; !strings.Contains(emb.Description, "Ep 2 DUB") {
		t.Errorf("picker choice = %q, want the dub view", emb.Description)
	}
	if !strings.HasSuffix(pickerOf(t, picked[0].Response).CustomID, ":dub") {
		t.Errorf("picker lost the track")
	}
}

func TestControlsFromBeforeTracksOpenTheServersPreferredTrack(t *testing.T) {
	seedFrieren(t)
	calls := pressNav(t, "sched:2026-11-04:user", "user", publicMsg())
	emb := calls[0].Response.Embeds[0]
	if !strings.Contains(emb.Description, "Ep 5 SUB") || strings.Contains(emb.Footer.Text, "preferred") {
		t.Errorf("old button = %q / %q, want the Sub view", emb.Description, emb.Footer.Text)
	}
	_, next := buttons(t, calls[0].Response)
	if !strings.HasSuffix(next.CustomID, ":sub") {
		t.Errorf("rebuilt buttons %q should now carry the track", next.CustomID)
	}
	picked := choosePick(t, "schedpick:user", "2026-11-04", "user", publicMsg())
	if !strings.Contains(picked[0].Response.Embeds[0].Description, "Ep 5 SUB") {
		t.Errorf("old picker = %q, want the Sub view", picked[0].Response.Embeds[0].Description)
	}
}

func TestTrackedControlsFollowThePressRuleAndKeepTheRefusal(t *testing.T) {
	seedFrieren(t)
	id := "sched:2026-11-03:author:dub"

	own := pressNav(t, id, "author", publicMsg())
	if len(own) != 1 || own[0].Op != "UpdateMessage" || !strings.Contains(own[0].Response.Embeds[0].Description, "Ep 2 DUB") {
		t.Errorf("author's dub press = %+v, want the message replaced with the dub view", own)
	}
	other := pressNav(t, id, "stranger", publicMsg())
	if len(other) != 1 || other[0].Op != "Reply" || other[0].Visibility != interaction.Private || !strings.Contains(other[0].Response.Embeds[0].Description, "Ep 2 DUB") {
		t.Errorf("other member's dub press = %+v, want a private dub copy", other)
	}
	copyPick := choosePick(t, "schedpick:author:dub", "2026-11-03", "stranger", publicMsg())
	if len(copyPick) != 1 || copyPick[0].Op != "Reply" || copyPick[0].Visibility != interaction.Private || !strings.Contains(copyPick[0].Response.Embeds[0].Description, "Ep 2 DUB") {
		t.Errorf("other member's dub pick = %+v, want a private dub copy", copyPick)
	}

	for _, refused := range [][]interactiontest.Call{
		pressNav(t, "sched:2030-01-01:author:dub", "author", publicMsg()),
		choosePick(t, "schedpick:author:dub", "2030-01-01", "author", publicMsg()),
	} {
		last := refused[len(refused)-1]
		if last.Visibility != interaction.Private || last.Response.Content != msgDayNotLoaded {
			t.Errorf("unloaded day = %+v, want the private refusal %q", refused, msgDayNotLoaded)
		}
	}
}
