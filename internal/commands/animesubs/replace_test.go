package animesubs

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"ReZeroTsu/internal/animeschedule"
	"ReZeroTsu/internal/database/anime_subs"
)

func readd(t *testing.T, st *fakeStore, choice string) string {
	t.Helper()
	opts := animeOption("Frieren")
	if choice != "" {
		opts = append(opts, trackOption(choice))
	}
	r := reply(t, invoke(t, "add", opts...))
	if len(r.Embeds) == 0 || r.Embeds[0].Footer == nil || r.Embeds[0].Footer.Text != msgDMsFooter {
		t.Errorf("reply lacks the DMs footer: %+v", r.Embeds)
	}
	return text(r)
}

func TestReaddWithADifferentSetReplacesTheTracks(t *testing.T) {
	withLookup(t, "Frieren")
	st := newFakeStore()
	useStore(t, st)
	readd(t, st, "")
	got := readd(t, st, "sub_dub")
	if !strings.Contains(got, "**Frieren** now alerts for Sub and Dub.") {
		t.Errorf("reply = %q, want the now-alerts-for line", got)
	}
	subs := st.docs["user"]
	if len(subs) != 1 || subs[0].Tracks != "sub_dub" {
		t.Errorf("stored = %+v, want one subscription with sub_dub", subs)
	}
	if strings.Join(st.calls, ",") != "add,settracks" {
		t.Errorf("store calls = %v, want add then a single settracks", st.calls)
	}
}

func TestReaddWithTheSameSetStaysAlreadySubscribed(t *testing.T) {
	withLookup(t, "Frieren")
	st := newFakeStore()
	useStore(t, st)
	readd(t, st, "sub_dub")
	if got := readd(t, st, "sub_dub"); !strings.Contains(got, "You are already subscribed to `Frieren`.") {
		t.Errorf("reply = %q", got)
	}
	// A subscription stored without tracks is Sub, so re-adding the default is the same set.
	st2 := newFakeStore().with("user", "Frieren")
	useStore(t, st2)
	if got := readd(t, st2, ""); !strings.Contains(got, "already subscribed") {
		t.Errorf("legacy re-add reply = %q", got)
	}
	if len(st2.calls) != 0 {
		t.Errorf("store calls = %v, want none", st2.calls)
	}
}

func TestReplaceKeepsKeptMemoryAndSeedsAddedMembers(t *testing.T) {
	withLookup(t, "Frieren")
	subAir := time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC).Unix()
	dubAir := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC).Unix()
	seedAiredToday(t,
		animeschedule.ShowEntry{Name: "Frieren", Track: animeschedule.TrackSub, Episode: "Ep 5", AirTimeUnix: subAir},
		animeschedule.ShowEntry{Name: "Frieren", Track: animeschedule.TrackDub, Episode: "Ep 2", AirTimeUnix: dubAir},
	)
	st := newFakeStore()
	st.docs["user"] = []*anime_subs.ShowSub{{Show: "Frieren", LastNotifiedAirUnix: 123, LastNotifiedEpisode: "kept"}}
	useStore(t, st)
	readd(t, st, "sub_dub")
	s := st.docs["user"][0]
	if air, ep := s.Memory("sub"); air != 123 || ep != "kept" {
		t.Errorf("sub memory = %d %q, want the kept 123 kept", air, ep)
	}
	if air, _ := s.Memory("dub"); air != dubAir {
		t.Errorf("dub memory = %d, want today's aired dub row %d", air, dubAir)
	}
}

func TestReplaceClearsStaleMemoryOfARejoiningMember(t *testing.T) {
	withLookup(t, "Frieren")
	seedAiredToday(t)
	st := newFakeStore()
	st.docs["user"] = []*anime_subs.ShowSub{{Show: "Frieren", Tracks: "sub", LastNotifiedDubAirUnix: 99, LastNotifiedDubEpisode: "old"}}
	useStore(t, st)
	readd(t, st, "dub")
	if air, ep := st.docs["user"][0].Memory("dub"); air != 0 || ep != "" {
		t.Errorf("dub memory = %d %q, want cleared: no dub aired today", air, ep)
	}
	if st.docs["user"][0].Notified {
		t.Error("clearing a member's memory marked the subscription notified, as Mongo does not")
	}
}

func TestReplaceNeverCountsAgainstTheLimit(t *testing.T) {
	withLookup(t, "Frieren")
	st := newFakeStore()
	for i := 0; i < maxUserSubscriptions-1; i++ {
		st.with("user", fmt.Sprintf("Show %d", i))
	}
	st.with("user", "Frieren")
	useStore(t, st)
	if got := readd(t, st, "dub"); !strings.Contains(got, "now alerts for Dub.") {
		t.Errorf("reply = %q, want the replace at the limit", got)
	}
}

func TestRemoveAfterReplaceRemovesTheWholeShow(t *testing.T) {
	withLookup(t, "Frieren")
	st := newFakeStore()
	useStore(t, st)
	readd(t, st, "")
	readd(t, st, "every")
	invoke(t, "remove", animeOption("Frieren")...)
	if len(st.docs["user"]) != 0 {
		t.Errorf("subscriptions = %v, want none", st.names("user"))
	}
}
