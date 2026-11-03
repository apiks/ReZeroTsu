package animeschedule

import "time"

// clockMinutes is the UTC time of day of an air clock in minutes, and false when the clock is unset (the zero time,
// or 00:00 UTC, which AnimeSchedule uses for "no time").
func clockMinutes(t time.Time) (int, bool) {
	t = t.UTC()
	m := t.Hour()*60 + t.Minute()
	return m, m != 0
}

// trackClock is the air clock and the own premiere of an upcoming show's sub or dub track.
func (u UpcomingShow) trackClock(t Track) (clock, premiere time.Time) {
	switch t {
	case TrackSub:
		return u.SubTime, u.SubPremiere
	case TrackDub:
		return u.DubTime, u.DubPremiere
	}
	return u.JPNTime, u.Premiere
}

// differingClock reports whether the track has an air clock that differs from raw's in UTC hour and minute.
func (u UpcomingShow) differingClock(t Track) bool {
	clock, _ := u.trackClock(t)
	m, set := clockMinutes(clock)
	if !set {
		return false
	}
	raw, _ := clockMinutes(u.JPNTime)
	return m != raw
}

// HasTrack reports whether the upcoming show has the track by AnimeSchedule's own rule: raw always exists, and a
// sub or dub track exists when its premiere is set, or its clock is set and differs from raw's in UTC hour and
// minute. The API hides the per-track TBD flag, so a TBD track counts as existing.
func (u UpcomingShow) HasTrack(t Track) bool {
	if t == TrackRaw {
		return true
	}
	_, premiere := u.trackClock(t)
	return !premiere.IsZero() || u.differingClock(t)
}

// TrackPremiere is the premiere of the upcoming show's track; the zero time when unknown. A track's own premiere
// wins. Otherwise a track that exists by its clock premieres at raw's premiere shifted by the signed clock
// difference in minutes, with no day wrap, provided raw's premiere and clock are both set.
func (u UpcomingShow) TrackPremiere(t Track) time.Time {
	_, own := u.trackClock(t)
	if !own.IsZero() || t == TrackRaw {
		return own
	}
	if u.Premiere.IsZero() || !u.differingClock(t) {
		return time.Time{}
	}
	raw, ok := clockMinutes(u.JPNTime)
	if !ok {
		return time.Time{}
	}
	clock, _ := u.trackClock(t)
	m, _ := clockMinutes(clock)
	return u.Premiere.Add(time.Duration(m-raw) * time.Minute)
}

// SetPremiere is the earliest known premiere among the set's tracks, with the track that has it. A plain Sub
// subscription reads raw's premiere when the sub has none, as Sub falls back to raw; in a set of several tracks raw's
// premiere belongs to the Raw track alone. ok is false when no premiere is known.
func (u UpcomingShow) SetPremiere(set SubscriptionTracks) (premiere time.Time, member Track, ok bool) {
	for _, m := range set.Members() {
		p := u.TrackPremiere(m)
		if p.IsZero() && m == TrackSub && set == SubscribeSub {
			p = u.Premiere
		}
		if !p.IsZero() && (!ok || p.Before(premiere)) {
			premiere, member, ok = p, m, true
		}
	}
	return premiere, member, ok
}
