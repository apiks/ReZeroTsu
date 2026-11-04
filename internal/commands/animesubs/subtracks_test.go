package animesubs

import (
	"strings"
	"testing"
	"time"

	"ReZeroTsu/internal/animeschedule"

	"github.com/bwmarrin/discordgo"
)

func trackOption(value string) *discordgo.ApplicationCommandInteractionDataOption {
	return &discordgo.ApplicationCommandInteractionDataOption{Name: "track", Type: discordgo.ApplicationCommandOptionString, Value: value}
}

func TestAddOffersFiveTrackOptionChoicesAndNeverSaysAll(t *testing.T) {
	var opt *discordgo.ApplicationCommandOption
	for _, o := range subcommand(t, "add").Options {
		if o.Name == "track" {
			opt = o
		}
	}
	if opt == nil {
		t.Fatal("/subscription add has no track option")
	}
	if opt.Required {
		t.Error("the track option is required, want optional")
	}
	var names []string
	for _, c := range opt.Choices {
		names = append(names, c.Name)
	}
	if got, want := strings.Join(names, ","), "Sub,Dub,Raw,Sub and Dub,Every track"; got != want {
		t.Errorf("choices = %s, want %s", got, want)
	}
	if strings.Contains(strings.ToLower(opt.Description+strings.Join(names, " ")), "all") {
		t.Errorf("the option or a choice says \"all\": %q %v", opt.Description, names)
	}
}

func TestAddStoresTheChosenSetAndOmittedMeansSub(t *testing.T) {
	for _, tc := range []struct {
		choice string
		want   animeschedule.SubscriptionTracks
	}{
		{"", animeschedule.SubscribeSub},
		{"sub", animeschedule.SubscribeSub},
		{"dub", animeschedule.SubscribeDub},
		{"raw", animeschedule.SubscribeRaw},
		{"sub_dub", animeschedule.SubscribeSubDub},
		{"every", animeschedule.SubscribeEvery},
	} {
		withLookup(t, "Frieren")
		st := newFakeStore()
		useStore(t, st)
		opts := animeOption("Frieren")
		if tc.choice != "" {
			opts = append(opts, trackOption(tc.choice))
		}
		invoke(t, "add", opts...)
		subscriptions := st.docs["user"]
		if len(subscriptions) != 1 {
			t.Fatalf("%q: subscriptions = %+v", tc.choice, subscriptions)
		}
		if got, _ := animeschedule.ParseSubscriptionTracks(subscriptions[0].Tracks); got != tc.want {
			t.Errorf("%q stored %q, want %v", tc.choice, subscriptions[0].Tracks, tc.want)
		}
	}
}

func TestConfirmationNamesANonSubSetAndAPlainSubReadsAsBefore(t *testing.T) {
	for _, tc := range []struct{ choice, want string }{
		{"", "Subscribed to **Frieren**."},
		{"sub", "Subscribed to **Frieren**."},
		{"dub", "Subscribed to **Frieren** (Dub)."},
		{"raw", "Subscribed to **Frieren** (Raw)."},
		{"sub_dub", "Subscribed to **Frieren** (Sub and Dub)."},
		{"every", "Subscribed to **Frieren** (Every track)."},
	} {
		withLookup(t, "Frieren")
		useStore(t, newFakeStore())
		opts := animeOption("Frieren")
		if tc.choice != "" {
			opts = append(opts, trackOption(tc.choice))
		}
		if got := text(reply(t, invoke(t, "add", opts...))); !strings.Contains(got, tc.want) {
			t.Errorf("%q reply = %q, want it to contain %q", tc.choice, got, tc.want)
		}
	}
}

// seedAiredToday loads rows that aired before noon on a fixed day and returns their air times.
func seedAiredToday(t *testing.T, rows ...animeschedule.ShowEntry) {
	t.Helper()
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	t.Cleanup(animeschedule.SetClock(func() time.Time { return now }))
	t.Cleanup(animeschedule.Seed(map[string][]animeschedule.ShowEntry{animeschedule.DateKey(now): rows}))
}

func TestAddSeedsEachTrackFromItsOwnAiredRowToday(t *testing.T) {
	withLookup(t, "Frieren")
	subAir := time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC).Unix()
	dubAir := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC).Unix()
	seedAiredToday(t,
		animeschedule.ShowEntry{Name: "Frieren", Track: animeschedule.TrackSub, Episode: "Ep 5", AirTimeUnix: subAir},
		animeschedule.ShowEntry{Name: "Frieren", Track: animeschedule.TrackDub, Episode: "Ep 2", AirTimeUnix: dubAir},
	)
	st := newFakeStore()
	useStore(t, st)
	invoke(t, "add", append(animeOption("Frieren"), trackOption("sub_dub"))...)
	s := st.docs["user"][0]
	if air, _ := s.Memory("sub"); air != subAir {
		t.Errorf("sub memory = %d, want %d", air, subAir)
	}
	if air, _ := s.Memory("dub"); air != dubAir {
		t.Errorf("dub memory = %d, want %d", air, dubAir)
	}
	if !s.Notified {
		t.Error("the seeded subscription is not marked notified")
	}
}

func TestAddNeverSeedsADubFromTodaysSubRow(t *testing.T) {
	withLookup(t, "Frieren")
	subAir := time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC).Unix()
	seedAiredToday(t, animeschedule.ShowEntry{Name: "Frieren", Track: animeschedule.TrackSub, Episode: "Ep 5", AirTimeUnix: subAir})
	st := newFakeStore()
	useStore(t, st)
	invoke(t, "add", append(animeOption("Frieren"), trackOption("dub"))...)
	s := st.docs["user"][0]
	if air, _ := s.Memory("dub"); air != 0 {
		t.Errorf("dub memory = %d, want none: no dub aired today", air)
	}
	if air, _ := s.Memory("sub"); air != 0 {
		t.Errorf("sub memory = %d, want none: Sub is not in the set", air)
	}
}

func TestAddSeedsSubFromTodaysRawFallbackRow(t *testing.T) {
	withLookup(t, "Frieren")
	rawAir := time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC).Unix()
	seedAiredToday(t, animeschedule.ShowEntry{Name: "Frieren", Track: animeschedule.TrackRaw, Episode: "Ep 5", AirTimeUnix: rawAir})
	st := newFakeStore()
	useStore(t, st)
	invoke(t, "add", append(animeOption("Frieren"), trackOption("every"))...)
	s := st.docs["user"][0]
	for _, track := range []string{"sub", "raw"} {
		if air, _ := s.Memory(track); air != rawAir {
			t.Errorf("%s memory = %d, want the raw row's %d", track, air, rawAir)
		}
	}
}

func TestAddSeedsEachTrackWithItsLatestAiredRowToday(t *testing.T) {
	withLookup(t, "Frieren")
	first := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC).Unix()
	second := time.Date(2026, 11, 4, 10, 30, 0, 0, time.UTC).Unix()
	later := time.Date(2026, 11, 4, 13, 0, 0, 0, time.UTC).Unix()
	seedAiredToday(t,
		animeschedule.ShowEntry{Name: "Frieren", Track: animeschedule.TrackDub, Episode: "Ep 1", AirTimeUnix: first},
		animeschedule.ShowEntry{Name: "Frieren", Track: animeschedule.TrackDub, Episode: "Ep 2", AirTimeUnix: second},
		animeschedule.ShowEntry{Name: "Frieren", Track: animeschedule.TrackDub, Episode: "Ep 3", AirTimeUnix: later},
	)
	st := newFakeStore()
	useStore(t, st)
	invoke(t, "add", append(animeOption("Frieren"), trackOption("dub"))...)
	if air, _ := st.docs["user"][0].Memory("dub"); air != second {
		t.Errorf("dub memory = %d, want the latest aired row's %d, so Ep 2 is not DMed", air, second)
	}
}

func TestReplaceSeedsANewTrackWithItsLatestAiredRowToday(t *testing.T) {
	withLookup(t, "Frieren")
	first := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC).Unix()
	second := time.Date(2026, 11, 4, 10, 30, 0, 0, time.UTC).Unix()
	seedAiredToday(t,
		animeschedule.ShowEntry{Name: "Frieren", Track: animeschedule.TrackDub, Episode: "Ep 1", AirTimeUnix: first},
		animeschedule.ShowEntry{Name: "Frieren", Track: animeschedule.TrackDub, Episode: "Ep 2", AirTimeUnix: second},
	)
	st := newFakeStore()
	st.with("user", "Frieren")
	useStore(t, st)
	invoke(t, "add", append(animeOption("Frieren"), trackOption("sub_dub"))...)
	if air, _ := st.docs["user"][0].Memory("dub"); air != second {
		t.Errorf("dub memory = %d, want the latest aired row's %d", air, second)
	}
}
