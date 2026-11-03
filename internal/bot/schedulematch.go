package bot

import (
	"time"

	"ReZeroTsu/internal/animeschedule"
)

// The daily schedule embed has no URL, so the shared description/URL matcher alone could mistake an older
// identical post for today's. Verification for the schedule therefore first narrows the recent messages to
// ones carrying the schedule embed title that were posted on the current UTC day.

// scheduleMessagesToday returns the messages in recent that have an embed titled title and were posted on
// now's UTC date. A message with an unknown (zero) timestamp is kept: the title carries the date.
func scheduleMessagesToday(recent []ChannelMessage, title string, now time.Time) []ChannelMessage {
	today := animeschedule.DateKey(now)
	var out []ChannelMessage
	for _, m := range recent {
		if !m.Timestamp.IsZero() && animeschedule.DateKey(m.Timestamp) != today {
			continue
		}
		for _, e := range m.Embeds {
			if e != nil && e.Title == title {
				out = append(out, m)
				break
			}
		}
	}
	return out
}

// scheduleMatcher is the recentMatcher for a daily schedule post titled title on now's UTC day: a message by
// the bot (or the webhook used) carrying that title, posted today (an unknown timestamp is accepted), whose
// embeds match by URL and description.
func scheduleMatcher(title string, now time.Time) recentMatcher {
	return func(recent []ChannelMessage, botUserID string, webhook *WebhookTarget, msg OutgoingMessage) (string, bool) {
		return matchRecentMessage(scheduleMessagesToday(recent, title, now), botUserID, webhook, msg)
	}
}
