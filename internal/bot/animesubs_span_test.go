package bot

import (
	"context"
	"testing"

	"ReZeroTsu/internal/animeschedule"
	"ReZeroTsu/internal/database/anime_subs"
)

func spanRows(row animeschedule.ShowEntry) dayShowsFunc {
	return func(bool, animeschedule.PreferredTrack) []animeschedule.ShowEntry {
		return []animeschedule.ShowEntry{row}
	}
}

// A channel that got "Ep 12F" before the deploy keeps that entry; the same airing now reads "Ep 1-12F".
func TestSpanRowOfAnEpisodeTheChannelAlreadyGotIsNotPostedAgainOnTheReleaseDay(t *testing.T) {
	span := trackedRow("Frieren", "Ep 1-12F", animeschedule.TrackDub)
	for _, restart := range []bool{false, true} {
		name := "without a restart"
		if restart {
			name = "with a restart between seeding and delivery"
		}
		t.Run(name, func(t *testing.T) {
			st := trackedState("dub", &anime_subs.ShowSub{Show: "Frieren", Track: "dub", Episode: "Ep 12F", Guild: true, Notified: true})
			d := newFakeDiscord()
			fn := spanRows(span)
			if restart {
				seedGuildNewEpisodesForShard(context.Background(), st, []string{"g1"}, fn, trackedNow)
			}
			for i := 0; i < 2; i++ {
				processGuildNewEpisodes(context.Background(), d, newTestSenders(d), st, "g1", fn, trackedNow)
			}
			if got := descriptions(d); len(got) != 0 {
				t.Errorf("reposted %q", got)
			}
		})
	}
}

func TestSeedingRecordsAnAiredRowAsPostedAndALaterRowAsUnnotified(t *testing.T) {
	later := trackedRow("Dandadan", "Ep 3", animeschedule.TrackSub)
	later.AirTimeUnix = trackedNow.Unix() + 300
	delayed := trackedRow("Delayed", "Ep 3", animeschedule.TrackSub)
	delayed.Delayed = "delayed"
	rows := []animeschedule.ShowEntry{trackedRow("Frieren", "Ep 1-12F", animeschedule.TrackDub), later, delayed}
	fn := func(bool, animeschedule.PreferredTrack) []animeschedule.ShowEntry { return rows }
	st := trackedState("", &anime_subs.ShowSub{Show: "Other", Track: "sub", Episode: "Ep 1", Guild: true, Notified: true})
	seedGuildNewEpisodesForShard(context.Background(), st, []string{"g1"}, fn, trackedNow)
	doc, _ := st.GetAnimeSubs(context.Background(), "c1")
	want := map[string]bool{"Frieren": true, "Dandadan": false, "Delayed": false}
	for _, s := range doc.Shows[1:] {
		if s.Notified != want[s.Show] {
			t.Errorf("%s notified = %v, want %v", s.Show, s.Notified, want[s.Show])
		}
	}
	// The row still to air posts once it airs, after the seeding.
	d := newFakeDiscord()
	for i := 0; i < 2; i++ {
		processGuildNewEpisodes(context.Background(), d, newTestSenders(d), st, "g1", fn, trackedNow.Add(10*60*1e9))
	}
	got := descriptions(d)
	if len(got) != 1 || got[0] != "**Ep 3** subbed is out!" {
		t.Errorf("posted %q, want the later row once", got)
	}
}

func TestSpanAlertReachesTheNewEpisodesChannel(t *testing.T) {
	st := trackedState("dub")
	st.AnimeSubs["c1"] = &anime_subs.AnimeSubs{ID: "c1", IsGuild: true, LastSeedDate: "2023-11-13"}
	fn := spanRows(trackedRow("Frieren", "Ep 1-12F", animeschedule.TrackDub))
	d := newFakeDiscord()
	seedGuildNewEpisodesForShard(context.Background(), st, []string{"g1"}, fn, trackedNow)
	processGuildNewEpisodes(context.Background(), d, newTestSenders(d), st, "g1", fn, trackedNow)
	got := descriptions(d)
	if len(got) != 1 || got[0] != "**Ep 1-12F** dubbed is out!" {
		t.Errorf("posted %q, want one span alert", got)
	}
}

func TestSpanAlertReachesASubscribersDMOnce(t *testing.T) {
	const user = "u1"
	st := newFakeSentState()
	st.AnimeSubs[user] = &anime_subs.AnimeSubs{ID: user, Shows: []*anime_subs.ShowSub{{Show: "Frieren"}}}
	show := animeschedule.ShowEntry{Name: "Frieren", Route: "frieren", Episode: "Ep 1-12F", AirTimeUnix: trackedNow.Unix() - 600, Track: "dub"}
	d := newFakeDiscord()
	for i := 0; i < 2; i++ {
		doc, _ := st.GetAnimeSubs(context.Background(), user)
		_ = processAnimeSubUser(context.Background(), d, newTestSenders(d), st, doc, subRows([]animeschedule.ShowEntry{show}), trackedNow)
	}
	msgs := d.deliveredTo("dm-" + user)
	if len(msgs) != 1 || len(msgs[0].Msg.Embeds) != 1 || msgs[0].Msg.Embeds[0].Description != "**Ep 1-12F** dubbed is out!" {
		t.Fatalf("DMs = %+v, want one span alert", msgs)
	}
}

func TestSpanRowAlreadyDMedUnderItsOldLabelIsNotDMedAgain(t *testing.T) {
	const user = "u1"
	aired := trackedNow.Unix() - 600
	old := animeschedule.ShowEntry{Name: "Frieren", Route: "frieren", Episode: "Ep 12F", AirTimeUnix: aired, Track: "sub"}
	st := newFakeSentState()
	st.AnimeSubs[user] = &anime_subs.AnimeSubs{ID: user, Shows: []*anime_subs.ShowSub{
		{Show: "Frieren", Notified: true, LastNotifiedAirUnix: aired, LastNotifiedEpisode: episodeKey(old)},
	}}
	span := old
	span.Episode = "Ep 1-12F"
	d := newFakeDiscord()
	doc, _ := st.GetAnimeSubs(context.Background(), user)
	_ = processAnimeSubUser(context.Background(), d, newTestSenders(d), st, doc, subRows([]animeschedule.ShowEntry{span}), trackedNow)
	if n := len(d.deliveredTo("dm-" + user)); n != 0 {
		t.Errorf("DMs = %d, want 0", n)
	}
}
