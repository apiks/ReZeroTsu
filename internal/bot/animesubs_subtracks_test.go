package bot

import (
	"context"
	"reflect"
	"testing"

	"ReZeroTsu/internal/animeschedule"
	"ReZeroTsu/internal/database/anime_subs"
)

// subRows is a selection that serves the rows as the Sub member's, whatever the set: the rows of a test that
// predates subscription tracks.
func subRows(rows []animeschedule.ShowEntry) []animeschedule.SelectedRow {
	var sel []animeschedule.SelectedRow
	for _, r := range rows {
		sel = append(sel, animeschedule.SelectedRow{ShowEntry: r, Members: []animeschedule.Track{animeschedule.TrackSub}})
	}
	return sel
}

// seedToday loads the rows as the timetable of trackedNow's date and returns the selection the notifier reads.
func seedToday(t *testing.T, rows ...animeschedule.ShowEntry) []animeschedule.SelectedRow {
	t.Helper()
	t.Cleanup(animeschedule.Seed(map[string][]animeschedule.ShowEntry{animeschedule.DateKey(trackedNow): rows}))
	return animeschedule.SelectRows(animeschedule.DayOf(trackedNow), animeschedule.SubscribeEvery)
}

// trackedAt is a row of the tracked show that aired the given seconds before trackedNow.
func trackedAt(ep string, track animeschedule.Track, secondsAgo int64) animeschedule.ShowEntry {
	row := trackedRow("Frieren", ep, track)
	row.AirTimeUnix = trackedNow.Unix() - secondsAgo
	return row
}

// dmTexts runs the notifier for one user as many times as given and returns the DM descriptions in order.
func dmTexts(t *testing.T, sub *anime_subs.ShowSub, sel []animeschedule.SelectedRow, runs int) ([]string, *fakeSentState) {
	t.Helper()
	const user = "u1"
	st := newFakeSentState()
	st.AnimeSubs[user] = &anime_subs.AnimeSubs{ID: user, Shows: []*anime_subs.ShowSub{sub}}
	d := newFakeDiscord()
	for range runs {
		doc, _ := st.GetAnimeSubs(context.Background(), user)
		_ = processAnimeSubUser(context.Background(), d, newTestSenders(d), st, doc, sel, trackedNow)
	}
	var out []string
	for _, m := range d.deliveredTo("dm-" + user) {
		out = append(out, m.Msg.Embeds[0].Description)
	}
	return out, st
}

func TestSubAndDubSendsADubEp2AfterASubEp5AndTheReverseOnceEach(t *testing.T) {
	for _, tc := range []struct {
		name string
		rows []animeschedule.ShowEntry
		want []string
	}{
		{"sub first", []animeschedule.ShowEntry{trackedAt("Ep 5", animeschedule.TrackSub, 300), trackedAt("Ep 2", animeschedule.TrackDub, 600)},
			[]string{"**Ep 5** subbed is out!", "**Ep 2** dubbed is out!"}},
		{"dub first", []animeschedule.ShowEntry{trackedAt("Ep 2", animeschedule.TrackDub, 300), trackedAt("Ep 5", animeschedule.TrackSub, 600)},
			[]string{"**Ep 2** dubbed is out!", "**Ep 5** subbed is out!"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sel := seedToday(t, tc.rows...)
			got, _ := dmTexts(t, &anime_subs.ShowSub{Show: "Frieren", Tracks: "sub_dub"}, sel, 2)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("DMs = %q, want %q, a rerun sending nothing", got, tc.want)
			}
		})
	}
}

func TestADubSubscriptionGetsNoSubOrRawDM(t *testing.T) {
	sel := seedToday(t, trackedAt("Ep 5", animeschedule.TrackRaw, 700), trackedAt("Ep 5", animeschedule.TrackSub, 600), trackedAt("Ep 2", animeschedule.TrackDub, 300))
	got, _ := dmTexts(t, &anime_subs.ShowSub{Show: "Frieren", Tracks: "dub"}, sel, 1)
	if want := []string{"**Ep 2** dubbed is out!"}; !reflect.DeepEqual(got, want) {
		t.Errorf("DMs = %q, want %q", got, want)
	}
}

func TestARowTwoMembersSelectIsSentOnceAndBothMembersRememberIt(t *testing.T) {
	sel := seedToday(t, trackedAt("Ep 1", animeschedule.TrackRaw, 600))
	got, st := dmTexts(t, &anime_subs.ShowSub{Show: "Frieren", Tracks: "every"}, sel, 2)
	if len(got) != 1 {
		t.Fatalf("DMs = %q, want one", got)
	}
	s := st.userShow("u1", "Frieren")
	for _, track := range []string{"sub", "raw"} {
		if air, ep := s.Memory(track); air != trackedNow.Unix()-600 || ep == "" {
			t.Errorf("%s memory = %d %q, want the row recorded", track, air, ep)
		}
	}
	if air, ep := s.Memory("dub"); air != 0 || ep != "" {
		t.Errorf("dub memory = %d %q, want none", air, ep)
	}
}

func TestALegacyEntryKeepsItsMemoryAndNeverGetsADub(t *testing.T) {
	aired := trackedNow.Unix() - 600
	sub := trackedAt("Ep 5", animeschedule.TrackSub, 600)
	sel := seedToday(t, sub, trackedAt("Ep 2", animeschedule.TrackDub, 300), trackedAt("Ep 6", animeschedule.TrackSub, 100))
	got, _ := dmTexts(t, &anime_subs.ShowSub{Show: "Frieren", Notified: true, LastNotifiedAirUnix: aired, LastNotifiedEpisode: episodeKey(sub)}, sel, 1)
	if want := []string{"**Ep 6** subbed is out!"}; !reflect.DeepEqual(got, want) {
		t.Errorf("DMs = %q, want only the new sub row %q", got, want)
	}
}
