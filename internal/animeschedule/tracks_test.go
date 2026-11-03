package animeschedule

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func row(name, route string, track Track, ep string, air int64) ShowEntry {
	return ShowEntry{Name: name, Route: route, Track: track, Episode: ep, AirTimeUnix: air}
}

// readWeek lists "name:TRACK:episode" of every row GetDayShows returns under pref across 2026-11-02 to 2026-11-15.
func readWeek(pref PreferredTrack, donghua bool) []string {
	var out []string
	for d := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC); d.Before(time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)); d = d.AddDate(0, 0, 1) {
		for _, e := range GetDayShows(d, donghua, pref) {
			out = append(out, fmt.Sprintf("%s:%s:%s", e.Name, e.Track, e.Episode))
		}
	}
	return out
}

func frierenWeek(t *testing.T) {
	t.Helper()
	t.Cleanup(Seed(map[string][]ShowEntry{
		"2026-11-02": {row("Frieren", "frieren", TrackDub, "Ep 2", 1)},
		"2026-11-06": {row("Frieren", "frieren", TrackRaw, "Ep 5", 2), row("Frieren", "frieren", TrackSub, "Ep 5", 3)},
		"2026-11-13": {row("Frieren", "frieren", TrackRaw, "Ep 6", 4), row("Frieren", "frieren", TrackSub, "Ep 6", 5)},
	}))
}

func TestFrierenReadsPerPreferredTrackAndASkippedDubWeekShowsTheSub(t *testing.T) {
	frierenWeek(t)
	for _, tc := range []struct {
		pref PreferredTrack
		want string
	}{
		{PreferSub, "Frieren:sub:Ep 5,Frieren:sub:Ep 6"},
		{PreferDub, "Frieren:dub:Ep 2,Frieren:sub:Ep 6"},
		{PreferRaw, "Frieren:raw:Ep 5,Frieren:raw:Ep 6"},
	} {
		if got := strings.Join(readWeek(tc.pref, true), ","); got != tc.want {
			t.Errorf("pref %v = %s, want %s", tc.pref, got, tc.want)
		}
	}
}

func TestDubViewFallsBackToRawAndShowsAShowWithoutADubItsSubRows(t *testing.T) {
	t.Cleanup(Seed(map[string][]ShowEntry{
		"2026-11-03": {row("Rawonly", "rawonly", TrackRaw, "Ep 1", 1), row("Subbed", "subbed", TrackRaw, "Ep 1", 2), row("Subbed", "subbed", TrackSub, "Ep 1", 3)},
		"2026-11-04": {row("Dubbed", "dubbed", TrackDub, "Ep 1", 4), row("Subbed", "subbed", TrackSub, "Ep 2", 5)},
	}))
	want := "Rawonly:raw:Ep 1,Subbed:sub:Ep 1,Dubbed:dub:Ep 1,Subbed:sub:Ep 2"
	if got := strings.Join(readWeek(PreferDub, true), ","); got != want {
		t.Errorf("dub view = %s, want %s", got, want)
	}
	if got := strings.Join(readWeek(PreferSub, true), ","); got != "Rawonly:raw:Ep 1,Subbed:sub:Ep 1,Subbed:sub:Ep 2" {
		t.Errorf("sub view = %s, a dub never shows", got)
	}
}

func TestDelayedDubRowStillHidesTheSubAndKeepsItsDelayLabel(t *testing.T) {
	delayed := row("Frieren", "frieren", TrackDub, "Ep 2", 1)
	delayed.Delayed = "Delayed"
	t.Cleanup(Seed(map[string][]ShowEntry{
		"2026-11-02": {delayed},
		"2026-11-06": {row("Frieren", "frieren", TrackSub, "Ep 5", 2)},
	}))
	got := GetDayShows(time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC), true, PreferDub)
	if len(got) != 1 || got[0].Delayed != "Delayed" {
		t.Fatalf("Monday = %+v, want the delayed dub", got)
	}
	if got := GetDayShows(time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC), true, PreferDub); len(got) != 0 {
		t.Errorf("Friday = %+v, want the sub hidden", got)
	}
}

func TestShowsWithoutARouteAreGroupedByTitleMatchKey(t *testing.T) {
	t.Cleanup(Seed(map[string][]ShowEntry{
		"2026-11-02": {row("Frieren Beyond", "", TrackDub, "Ep 2", 1)},
		"2026-11-06": {row("FRIEREN BEYOND", "", TrackSub, "Ep 5", 2)},
	}))
	if got := strings.Join(readWeek(PreferDub, true), ","); got != "Frieren Beyond:dub:Ep 2" {
		t.Errorf("dub view = %s, want the title-grouped sub hidden", got)
	}
}

func TestDonghuaIsHiddenUnderEveryPreference(t *testing.T) {
	soul := row("Soul Land", "soul", TrackDub, "Ep 1", 1)
	soul.Donghua = true
	t.Cleanup(Seed(map[string][]ShowEntry{"2026-11-03": {soul, row("Frieren", "frieren", TrackDub, "Ep 1", 2)}}))
	for _, pref := range []PreferredTrack{PreferSub, PreferDub, PreferRaw} {
		for _, name := range readWeek(pref, false) {
			if strings.HasPrefix(name, "Soul Land") {
				t.Errorf("pref %v shows %s with donghua off", pref, name)
			}
		}
	}
	if got := readWeek(PreferDub, true); len(got) != 2 {
		t.Errorf("donghua on = %v, want both", got)
	}
}

func TestSeedingKeepsRowsApartThatDifferInEpisodeOrAirTime(t *testing.T) {
	t.Cleanup(Seed(map[string][]ShowEntry{
		"2026-11-03": {
			row("A", "a", TrackSub, "Ep 1", 1), row("A", "a", TrackSub, "Ep 2", 1),
			row("B", "b", TrackSub, "Ep 1", 1), row("B", "b", TrackSub, "Ep 1", 2),
		},
	}))
	if got := strings.Join(readWeek(PreferSub, true), ","); got != "A:sub:Ep 1,A:sub:Ep 2,B:sub:Ep 1,B:sub:Ep 1" {
		t.Errorf("sub view = %s, want two episodes at one time and one episode at two times kept as four rows", got)
	}
}

func TestSeedingKeepsIdenticalRowsOnceAndReadsAnUnknownTrackAsRaw(t *testing.T) {
	t.Cleanup(Seed(map[string][]ShowEntry{
		"2026-11-03": {row("A", "a", TrackSub, "Ep 1", 1), row("A", "a", TrackSub, "Ep 1", 1), row("B", "b", "weird", "Ep 1", 2), row("C", "c", "", "Ep 1", 3)},
	}))
	if got := strings.Join(readWeek(PreferRaw, true), ","); got != "B:raw:Ep 1,C:raw:Ep 1" {
		t.Errorf("raw view = %s", got)
	}
	if got := strings.Join(readWeek(PreferSub, true), ","); got != "A:sub:Ep 1,B:raw:Ep 1,C:raw:Ep 1" {
		t.Errorf("sub view = %s, want the repeated row once", got)
	}
}

func TestFetchKeepsEveryTrackAndTheOtherReadersSeeTheSubView(t *testing.T) {
	resetTimetable(t)
	defer at(time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC))()
	rowJSON := func(track, ep, at string) string {
		return `{"title":"Frieren","route":"frieren","airType":"` + track + `","episodeNumber":` + ep + `,"episodeDate":"` + at + `"}`
	}
	newTimetableServer(t, map[string]any{
		"2026-45": "[" + rowJSON("raw", "5", "2026-11-06T15:00:00Z") + "," + rowJSON("sub", "5", "2026-11-06T16:00:00Z") + "," +
			rowJSON("dub", "2", "2026-11-02T20:00:00Z") + "," + rowJSON("sub", "5", "2026-11-06T16:00:00Z") + "]",
	})
	UpdateAnimeSchedule(context.Background(), "key")
	if got := strings.Join(readWeek(PreferDub, true), ","); got != "Frieren:dub:Ep 2" {
		t.Errorf("dub view = %s", got)
	}
	if got := strings.Join(readWeek(PreferRaw, true), ","); got != "Frieren:raw:Ep 5" {
		t.Errorf("raw view = %s", got)
	}
	if all := AllEntries(); len(all) != 1 || all[0].Track != TrackSub {
		t.Errorf("AllEntries = %+v, want the sub row only", all)
	}
	if airing := AiringShows(); len(airing) != 1 || airing[0].Track != TrackSub {
		t.Errorf("AiringShows = %+v, want the sub row", airing)
	}
	if snap := SnapshotWeeks(); len(snap.This) != 1 || snap.This[0].Track != TrackSub {
		t.Errorf("SnapshotWeeks.This = %+v, want the sub row", snap.This)
	}
}
