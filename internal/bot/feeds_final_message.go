package bot

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"ReZeroTsu/internal/database/feed_checks"
	"ReZeroTsu/internal/database/guilds"
	"ReZeroTsu/internal/discord"
	"ReZeroTsu/internal/logger"
)

// REMOVE on 2026-11-27 with the rest of the Reddit retirement code.

const (
	finalMessageLead = "**Reddit feeds have stopped.** Reddit shut down the RSS feeds I read on 13 November 2026, so "
	finalMessageTail = " won't post here any more. Nothing is needed from you."
)

// finalMessageRecordKey is the feed check key that says a channel got its final message. A post's key is its item
// ID, an underscore and the channel ID, and a channel ID has no underscore, so a key with the channel ID after a
// colon and no underscore can never equal a post's key.
func finalMessageRecordKey(channelID string) string {
	return "reddit-final-message:" + channelID
}

// feedFinalPolicy is the policy for the final message: like a feed post, but matched on its text, as it has no embed.
func feedFinalPolicy() sendPolicy {
	return sendPolicy{match: matchRecentMessage, label: "feed send", priority: PriorityFeeds}
}

// joinFeedNames joins names in plain English: "A", "A and B", "A, B and C".
func joinFeedNames(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// finalMessageText is the final message for a channel's feeds: each distinct subreddit and sort once, cut with
// "and N more" when the text would exceed Discord's content limit.
func finalMessageText(feeds []guilds.FeedEntry) string {
	var names []string
	seen := make(map[feedKey]bool)
	for _, f := range feeds {
		k := feedKey{f.Subreddit, f.PostType}
		if seen[k] {
			continue
		}
		seen[k] = true
		names = append(names, "r/"+f.Subreddit+" ("+f.PostType+")")
	}
	text := finalMessageLead + joinFeedNames(names) + finalMessageTail
	for shown := len(names) - 1; len(text) > discord.MaxContentLength && shown >= 1; shown-- {
		cut := append(append([]string{}, names[:shown]...), strconv.Itoa(len(names)-shown)+" more")
		text = finalMessageLead + joinFeedNames(cut) + finalMessageTail
	}
	return text
}

// sendFinalMessages sends each channel of the guild that still has a Reddit feed its one final message, no embeds,
// and records it once it lands. A channel that already has its record is skipped.
func sendFinalMessages(ctx context.Context, d RunOperations, senders *channelSenderFactory, st SentState, guildID string, feeds []guilds.FeedEntry) {
	var channels []string
	byChannel := make(map[string][]guilds.FeedEntry)
	var keys []string
	for _, f := range feeds {
		if _, ok := byChannel[f.ChannelID]; !ok {
			channels = append(channels, f.ChannelID)
			keys = append(keys, finalMessageRecordKey(f.ChannelID))
		}
		byChannel[f.ChannelID] = append(byChannel[f.ChannelID], f)
	}
	if len(channels) == 0 {
		return
	}
	done, err := st.ExistingFeedGUIDs(ctx, guildID, keys)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			logger.For("feeds").Error("ExistingFeedGUIDs failed", "shard_id", d.ShardID(), "guild_id", guildID, "err", err)
		}
		return
	}
	for _, ch := range channels {
		if ctx.Err() != nil {
			return
		}
		key := finalMessageRecordKey(ch)
		if _, ok := done[key]; ok {
			continue
		}
		res := senders.open(guildID, ch, feedFinalPolicy()).post(ctx, OutgoingMessage{Content: finalMessageText(byChannel[ch])})
		switch res.Outcome {
		case PostCancelled:
			return
		case PostRejected, PostMissing:
			handleFeedSendFailure(ctx, d, st, guildID, ch, res)
			continue
		}
		persistFinalMessage(ctx, d, st, guildID, ch, key)
		if !waitFeedCtx(ctx, feedWebhookThrottle) {
			return
		}
	}
}

// persistFinalMessage records that channelID got its final message. The write is detached from shutdown so that a
// delivered message is never sent again on the next run.
func persistFinalMessage(ctx context.Context, d RunOperations, st SentState, guildID, channelID, key string) {
	fc := feed_checks.FeedCheck{GUID: key, Feed: feed_checks.Feed{GuildID: guildID, ChannelID: channelID}, Date: time.Now()}
	err := detachedWrite(ctx, func(wctx context.Context) error {
		return st.SaveFeedChecks(wctx, guildID, []feed_checks.FeedCheck{fc})
	})
	if err != nil {
		logger.For("feeds").Error("Save FeedChecks failed", "shard_id", d.ShardID(), "count", 1, "err", err)
	}
}
