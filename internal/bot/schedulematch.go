package bot

import (
	"context"
	"time"
)

// The daily schedule embed has no URL, so the shared description/URL matcher alone could mistake an older
// identical post for today's. Verification for the schedule therefore first narrows the recent messages to
// ones carrying the schedule embed title that were posted on the current UTC day.

// scheduleMessagesToday returns the messages in recent that have an embed titled title and were posted on
// now's UTC date. A message with an unknown (zero) timestamp is kept: the title carries the date.
func scheduleMessagesToday(recent []ChannelMessage, title string, now time.Time) []ChannelMessage {
	today := now.UTC().Format("2006-01-02")
	var out []ChannelMessage
	for _, m := range recent {
		if !m.Timestamp.IsZero() && m.Timestamp.UTC().Format("2006-01-02") != today {
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

// scheduleVerifyPort narrows RecentMessages to today's schedule posts so that sendWithRules' verification
// of an Unknown outcome uses the schedule matching rules.
type scheduleVerifyPort struct {
	DiscordPort
	title string
	now   time.Time
}

func (p scheduleVerifyPort) RecentMessages(ctx context.Context, channelID string, limit int) ([]ChannelMessage, error) {
	recent, err := p.DiscordPort.RecentMessages(ctx, channelID, limit)
	if err != nil {
		return nil, err
	}
	return scheduleMessagesToday(recent, p.title, p.now), nil
}
