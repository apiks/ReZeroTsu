package animeschedule

import (
	"fmt"
	"testing"
	"time"
)

// loadUpcomingItem serves one raw /anime item through the upcoming-list fake and returns the show it decodes to.
func loadUpcomingItem(t *testing.T, fields string) UpcomingShow {
	t.Helper()
	f := &upcomingFixture{total: 1, pages: []string{fmt.Sprintf(`[{"title":"Frieren","route":"frieren",%s}]`, fields)}}
	useUpcomingServer(t, f)
	if err := RefreshUpcoming(t.Context(), "key"); err != nil {
		t.Fatal(err)
	}
	shows := UpcomingShows()
	if len(shows) != 1 {
		t.Fatalf("shows = %+v", shows)
	}
	return shows[0]
}

func utc(s string) time.Time {
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return v
}

func TestUpcomingShowDecodesItsPremieresAndClocks(t *testing.T) {
	u := loadUpcomingItem(t, `"premier":"2027-01-10T15:00:00Z","subPremier":"2027-01-11T16:30:00Z","dubPremier":null,`+
		`"jpnTime":"2027-01-10T15:00:00Z","subTime":"2027-01-11T16:30:00Z","dubTime":"0001-01-01T00:00:00Z"`)
	if !u.Premiere.Equal(utc("2027-01-10T15:00:00Z")) || !u.SubPremiere.Equal(utc("2027-01-11T16:30:00Z")) || !u.DubPremiere.IsZero() {
		t.Errorf("premieres = %v %v %v", u.Premiere, u.SubPremiere, u.DubPremiere)
	}
	if !u.JPNTime.Equal(utc("2027-01-10T15:00:00Z")) || !u.SubTime.Equal(utc("2027-01-11T16:30:00Z")) || !u.DubTime.IsZero() {
		t.Errorf("clocks = %v %v %v", u.JPNTime, u.SubTime, u.DubTime)
	}
}

func TestUpcomingTrackExistsByPremiereOrByADifferingClock(t *testing.T) {
	const raw = `"premier":"2027-01-10T15:00:00Z","jpnTime":"2027-01-10T15:00:00Z"`
	for _, tc := range []struct {
		name, fields      string
		sub, dub, rawWant bool
	}{
		{"nothing set", raw, false, false, true},
		{"sub premiere only", raw + `,"subPremier":"2027-02-01T00:00:00Z"`, true, false, true},
		{"dub premiere only", raw + `,"dubPremier":"2027-03-01T00:00:00Z"`, false, true, true},
		{"sub clock differs", raw + `,"subTime":"2027-01-10T16:00:00Z"`, true, false, true},
		{"dub clock differs in minutes", raw + `,"dubTime":"2027-01-10T15:30:00Z"`, false, true, true},
		{"clock equals jpnTime on another date", raw + `,"subTime":"2027-02-14T15:00:00Z","dubTime":"2030-01-01T15:00:00Z"`, false, false, true},
		{"clock at 00:00 UTC means unset", raw + `,"subTime":"2027-01-10T00:00:00Z"`, false, false, true},
		{"clock set with no raw clock", `"subTime":"2027-01-10T16:00:00Z"`, true, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := loadUpcomingItem(t, tc.fields)
			if got := [3]bool{u.HasTrack(TrackSub), u.HasTrack(TrackDub), u.HasTrack(TrackRaw)}; got != [3]bool{tc.sub, tc.dub, tc.rawWant} {
				t.Errorf("sub, dub, raw exist = %v, want %v", got, [3]bool{tc.sub, tc.dub, tc.rawWant})
			}
		})
	}
}

func TestUpcomingTrackPremiereIsItsOwnShiftedOrUnknown(t *testing.T) {
	const raw = `"premier":"2027-01-10T15:00:00Z","jpnTime":"2027-01-10T15:00:00Z"`
	for _, tc := range []struct {
		name, fields string
		track        Track
		want         time.Time
	}{
		{"own premiere wins over the clock", raw + `,"subPremier":"2027-02-01T09:00:00Z","subTime":"2027-01-10T18:00:00Z"`, TrackSub, utc("2027-02-01T09:00:00Z")},
		{"later clock shifts forward", raw + `,"subTime":"2027-01-10T16:30:00Z"`, TrackSub, utc("2027-01-10T16:30:00Z")},
		{"earlier clock shifts back", raw + `,"dubTime":"2027-01-10T13:00:00Z"`, TrackDub, utc("2027-01-10T13:00:00Z")},
		{"shift does not wrap the day", `"premier":"2027-01-10T23:00:00Z","jpnTime":"2027-01-10T23:00:00Z","subTime":"2027-01-10T02:00:00Z"`, TrackSub, utc("2027-01-10T02:00:00Z")},
		{"no raw premiere is unknown", `"jpnTime":"2027-01-10T15:00:00Z","subTime":"2027-01-10T16:00:00Z"`, TrackSub, time.Time{}},
		{"no raw clock is unknown", `"premier":"2027-01-10T15:00:00Z","subTime":"2027-01-10T16:00:00Z"`, TrackSub, time.Time{}},
		{"a track that does not exist is unknown", raw, TrackDub, time.Time{}},
		{"raw is premier", raw, TrackRaw, utc("2027-01-10T15:00:00Z")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := loadUpcomingItem(t, tc.fields).TrackPremiere(tc.track); !got.Equal(tc.want) {
				t.Errorf("premiere = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestUpcomingSetPremiereIsTheEarliestKnownAmongTheSet(t *testing.T) {
	u := loadUpcomingItem(t, `"premier":"2027-01-10T15:00:00Z","jpnTime":"2027-01-10T15:00:00Z",`+
		`"subPremier":"2027-01-12T10:00:00Z","dubPremier":"2027-03-01T10:00:00Z"`)
	for _, tc := range []struct {
		set   SubscriptionTracks
		want  time.Time
		track Track
	}{
		{SubscribeSub, utc("2027-01-12T10:00:00Z"), TrackSub},
		{SubscribeDub, utc("2027-03-01T10:00:00Z"), TrackDub},
		{SubscribeRaw, utc("2027-01-10T15:00:00Z"), TrackRaw},
		{SubscribeSubDub, utc("2027-01-12T10:00:00Z"), TrackSub},
		{SubscribeEvery, utc("2027-01-10T15:00:00Z"), TrackRaw},
	} {
		got, track, ok := u.SetPremiere(tc.set)
		if !ok || !got.Equal(tc.want) || track != tc.track {
			t.Errorf("%v: premiere = %v (%v, known %v), want %v (%v)", tc.set, got, track, ok, tc.want, tc.track)
		}
	}
}

func TestUpcomingSetPremiereOfSubFallsBackToRawAndIsUnknownWhenNothingIsKnown(t *testing.T) {
	u := loadUpcomingItem(t, `"premier":"2027-01-10T15:00:00Z","jpnTime":"2027-01-10T15:00:00Z"`)
	if got, _, ok := u.SetPremiere(SubscribeSub); !ok || !got.Equal(utc("2027-01-10T15:00:00Z")) {
		t.Errorf("Sub = %v, %v; want raw's premiere", got, ok)
	}
	if _, _, ok := u.SetPremiere(SubscribeDub); ok {
		t.Error("Dub with no dub premiere reads as known")
	}
	none := loadUpcomingItem(t, `"dubTime":"2027-01-10T16:00:00Z"`)
	if _, _, ok := none.SetPremiere(SubscribeDub); ok {
		t.Error("an existing dub with no known premiere reads as known")
	}
}

// A set of several tracks never lends raw's premiere to Sub: it belongs to the Raw track and is labelled raw.
func TestUpcomingSetPremiereLabelsRawsPremiereRawInASetOfSeveralTracks(t *testing.T) {
	u := loadUpcomingItem(t, `"premier":"2027-01-10T15:00:00Z","jpnTime":"2027-01-10T15:00:00Z",`+
		`"subTime":"2027-01-10T00:00:00Z","dubTime":"2027-01-10T00:00:00Z"`)
	if got, track, ok := u.SetPremiere(SubscribeSub); !ok || !got.Equal(utc("2027-01-10T15:00:00Z")) || track != TrackSub {
		t.Errorf("plain Sub = %v (%v, known %v), want raw's premiere read as Sub's", got, track, ok)
	}
	if got, track, ok := u.SetPremiere(SubscribeSubDub); ok {
		t.Errorf("Sub and Dub = %v (%v), want none: Sub has no premiere of its own and raw is not in the set", got, track)
	}
	if got, track, ok := u.SetPremiere(SubscribeEvery); !ok || !got.Equal(utc("2027-01-10T15:00:00Z")) || track != TrackRaw {
		t.Errorf("Every track = %v (%v, known %v), want raw's premiere labelled raw", got, track, ok)
	}
}
