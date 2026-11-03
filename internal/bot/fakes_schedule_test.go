package bot

import (
	"context"
	"time"

	"ReZeroTsu/internal/animeschedule"
	"ReZeroTsu/internal/database/guilds"
)

// Fake additions for the daily-schedule delivery run (ticket #9).

// MarkSchedulePosted sets last_posted_date on the guild's daily-schedule autopost entry.
func (f *fakeSentState) MarkSchedulePosted(_ context.Context, guildID, date string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if ap, ok := f.Autoposts[apKey(guildID, postTypeDailySchedule)]; ok {
		ap.LastPostedDate = date
	}
	return nil
}

// stampedDiscord wraps fakeDiscord so read-back messages carry a post time and optional older messages.
type stampedDiscord struct {
	*fakeDiscord
	stamp time.Time        // Timestamp given to every message the fake recorded
	older []ChannelMessage // extra messages returned after the recorded ones
}

func (s *stampedDiscord) RecentMessages(ctx context.Context, channelID string, limit int) ([]ChannelMessage, error) {
	out, err := s.fakeDiscord.RecentMessages(ctx, channelID, limit)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Timestamp = s.stamp
	}
	return append(out, s.older...), nil
}

// scheduleHarness is a guild "g1" with a daily-schedule channel "sch".
type scheduleHarness struct {
	d   fakeDiscordPort
	fd  *fakeDiscord
	st  *fakeSentState
	now time.Time
	fn  dayShowsFunc
}

func newScheduleHarness(script ...step) *scheduleHarness {
	now := time.Date(2024, 3, 5, 0, 0, 30, 0, time.UTC) // a Tuesday, just after midnight
	shows := []animeschedule.ShowEntry{
		{Name: "Show A", Route: "show-a", Episode: "Episode 1", AirTimeUnix: now.Unix() + 3600, Track: "sub"},
	}
	st := newFakeSentState()
	st.Autoposts[apKey("g1", postTypeDailySchedule)] = &guilds.Autopost{PostType: postTypeDailySchedule, ID: "sch"}
	fd := newFakeDiscord(script...)
	scheduleRetryWait = 0
	return &scheduleHarness{d: fd, fd: fd, st: st, now: now, fn: func(bool, animeschedule.PreferredTrack) []animeschedule.ShowEntry { return shows }}
}

func (h *scheduleHarness) run(ctx context.Context) error {
	return processDailyScheduleForGuild(ctx, h.d, newTestSenders(h.d), h.st, "g1", h.fn, h.now)
}

func (h *scheduleHarness) lastPosted() string {
	ap, _ := h.st.GetAutopost(context.Background(), "g1", postTypeDailySchedule)
	if ap == nil {
		return ""
	}
	return ap.LastPostedDate
}
