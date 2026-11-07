package bot

import (
	"context"
	"errors"
	"time"

	"ReZeroTsu/internal/database/feed_checks"
	"ReZeroTsu/internal/logger"
	"ReZeroTsu/internal/reddit"
)

// REMOVE on 2026-11-27 with the rest of the Reddit retirement code.

// dailyFeedWarning is the content of the first message a channel gets on a UTC day before the cutoff.
const dailyFeedWarning = "**This Reddit feed stops on 13 November 2026.** Reddit is shutting down the RSS feeds I read, so all my Reddit feeds end that day. Nothing is needed from you."

// feedWarningKey is the key of the feed check that records a channel's warning for the UTC day of now. Its
// prefix is no Reddit item key, so it cannot clash with a post's record.
func feedWarningKey(now time.Time, channelID string) string {
	return "daily-warning:" + now.UTC().Format("2006-01-02") + ":" + channelID
}

// pendingFeedWarning returns the warning content for the next message to channelID and the feed check to write
// with it once that message lands, or nothing when the channel was already warned today (or the lookup failed,
// in which case a missing warning is safer than a repeated one).
func pendingFeedWarning(ctx context.Context, st SentState, guildID, channelID string) (string, []feed_checks.FeedCheck) {
	if reddit.Retired() {
		return "", nil
	}
	key := feedWarningKey(reddit.Now(), channelID)
	existing, err := st.ExistingFeedGUIDs(ctx, guildID, []string{key})
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			logger.For("feeds").Error("ExistingFeedGUIDs failed for the daily warning", "guild_id", guildID, "channel_id", channelID, "err", err)
		}
		return "", nil
	}
	if _, ok := existing[key]; ok {
		return "", nil
	}
	return dailyFeedWarning, []feed_checks.FeedCheck{{
		GUID: key,
		Feed: feed_checks.Feed{GuildID: guildID, ChannelID: channelID},
		Date: time.Now(),
	}}
}
