package bot

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/database/feed_checks"
	"ReZeroTsu/internal/database/guilds"
	"ReZeroTsu/internal/discord"
	"ReZeroTsu/internal/httpclient"
	"ReZeroTsu/internal/logger"
	"ReZeroTsu/internal/reddit"

	"github.com/bwmarrin/discordgo"
	"github.com/mmcdole/gofeed"
)

const (
	feedRunInterval        = 15 * time.Second
	feedSubredditDelay     = 2 * time.Second
	feedCheckLimit         = 1000
	feedCleanupEveryRuns   = 10
	feedCheckLifespanDays  = 180
	feedWebhookThrottle    = 200 * time.Millisecond
	feedPinThrottle        = 200 * time.Millisecond
	feedEmbedAuthorIconURL = "https://images-eu.ssl-images-amazon.com/images/I/418PuxYS63L.png"
)

var feedRoundRobin ShardRoundRobin

// RunFeedLoop runs the feed poll loop for this shard (round-robin). Exits when ctx is cancelled.
func RunFeedLoop(ctx context.Context, db *database.Client, session *discordgo.Session, interval time.Duration, getShardCount func() int) {
	if interval <= 0 {
		interval = feedRunInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var runCount int
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if feedRoundRobin.RunWithTurn(session, getShardCount, func() { runFeedCheck(ctx, db, session, runCount%feedCleanupEveryRuns == 0) }) {
				runCount++
			}
		}
	}
}

func runFeedCheck(ctx context.Context, db *database.Client, s *discordgo.Session, doCleanup bool) {
	if doCleanup {
		cutoff := time.Now().Add(-feedCheckLifespanDays * 24 * time.Hour)
		n, err := db.FeedChecks().DeleteOlderThan(ctx, cutoff)
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				logger.For("feeds").Error("DeleteOlderThan failed", "shard_id", s.ShardID, "err", err)
			}
		} else if n > 0 {
			logger.For("feeds").Debug("cleaned up expired feed checks", "shard_id", s.ShardID, "count", n)
		}
	}

	seen := make(map[string]struct{})
	loggedGlobalCooldown := false
	for _, g := range s.State.Guilds {
		if g == nil || g.ID == "" {
			continue
		}
		feeds, err := db.Guilds().GetFeeds(ctx, g.ID)
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				logger.For("feeds").Error("GetFeeds failed", "shard_id", s.ShardID, "guild_id", g.ID, "err", err)
			}
			continue
		}
		if len(feeds) == 0 {
			continue
		}

		guids, err := db.FeedChecks().ListGUIDsByGuildID(ctx, g.ID, feedCheckLimit)
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				logger.For("feeds").Error("ListGUIDsByGuildID failed", "shard_id", s.ShardID, "guild_id", g.ID, "err", err)
			}
			continue
		}
		for _, id := range guids {
			seen[id] = struct{}{}
		}

		keys := uniqueFeedKeys(feeds)
		parsed := make(map[string]*gofeed.Feed)
		for _, key := range keys {
			select {
			case <-ctx.Done():
				return
			default:
			}
			feed, err := reddit.FetchWithRetry(ctx, key.subreddit, key.postType)
			if err != nil {
				if reddit.IsGlobalCooldown(err) {
					if !loggedGlobalCooldown {
						logger.For("feeds").Warn("reddit globally rate-limited; skipping feeds for this run", "shard_id", s.ShardID, "user_agent", httpclient.GetUserAgent(), "err", err)
						loggedGlobalCooldown = true
					}
					continue
				}
				logger.For("feeds").Error("fetchRedditRSS failed", "shard_id", s.ShardID, "key", key.subreddit+"/"+key.postType, "user_agent", httpclient.GetUserAgent(), "err", err)
				if reddit.IsPermanent(err) {
					removeFeedsForSubredditAndPostType(ctx, db, g.ID, key.subreddit, key.postType)
				}
			} else {
				parsed[key.subreddit+"/"+key.postType] = feed
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(feedSubredditDelay):
			}
		}

		channelBatches := make(map[string][]sendItem)
		for _, feed := range feeds {
			key := feed.Subreddit + "/" + feed.PostType
			pf := parsed[key]
			if pf == nil {
				continue
			}
			// Iterate items in reverse RSS order so we send oldest first.
			for i := len(pf.Items) - 1; i >= 0; i-- {
				item := pf.Items[i]
				if item == nil {
					continue
				}
				compositeGUID := feedItemCompositeGUID(item, feed.ChannelID)
				if _, ok := seen[compositeGUID]; ok {
					continue
				}
				if !validateFeedItem(feed, item) {
					continue
				}
				emb := buildFeedEmbed(feed, item)
				si := sendItem{embed: emb, feed: feed, compositeGUID: compositeGUID}
				if feed.Pin {
					flushChannelBatches(ctx, db, s, g.ID, channelBatches, seen)
					sendOneAndPin(ctx, db, s, g.ID, feed, compositeGUID, emb, seen)
					time.Sleep(feedPinThrottle)
					continue
				}
				channelBatches[feed.ChannelID] = append(channelBatches[feed.ChannelID], si)
				if len(channelBatches[feed.ChannelID]) >= discord.MaxEmbedsPerMessage {
					flushChannel(ctx, db, s, g.ID, feed.ChannelID, channelBatches[feed.ChannelID], seen, nil)
					channelBatches[feed.ChannelID] = nil
					time.Sleep(feedWebhookThrottle)
				}
			}
		}
		flushChannelBatches(ctx, db, s, g.ID, channelBatches, seen)

		for k := range seen {
			delete(seen, k)
		}
	}
}

type feedKey struct {
	subreddit, postType string
}

func uniqueFeedKeys(feeds []guilds.FeedEntry) []feedKey {
	m := make(map[feedKey]struct{})
	for _, f := range feeds {
		m[feedKey{f.Subreddit, f.PostType}] = struct{}{}
	}
	out := make([]feedKey, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

type sendItem struct {
	embed         *discordgo.MessageEmbed
	feed          guilds.FeedEntry
	compositeGUID string
}

// feedItemCompositeGUID returns a unique key for dedupe (GUID, Link, Title, or Published).
func feedItemCompositeGUID(item *gofeed.Item, channelID string) string {
	key := item.GUID
	if key == "" {
		key = item.Link
	}
	if key == "" {
		key = item.Title
	}
	if key == "" {
		key = item.Published
	}
	return key + "_" + channelID
}

func removeFeedsForSubredditAndPostType(ctx context.Context, db *database.Client, guildID, subreddit, postType string) {
	if _, err := db.Guilds().RemoveFeedsBySubredditAndPostType(ctx, guildID, subreddit, postType); err != nil {
		if !errors.Is(err, context.Canceled) {
			logger.For("feeds").Error("RemoveFeedsBySubredditAndPostType failed", "subreddit", subreddit, "post_type", postType, "err", err)
		}
		return
	}
	if _, err := db.FeedChecks().DeleteBySubredditAndPostType(ctx, guildID, subreddit, postType); err != nil {
		if !errors.Is(err, context.Canceled) {
			logger.For("feeds").Error("DeleteBySubredditAndPostType failed", "subreddit", subreddit, "post_type", postType, "err", err)
		}
		return
	}
	logger.For("feeds").Warn("subreddit unavailable (404/403), removed feeds and feed checks", "subreddit", subreddit, "post_type", postType)
}

func removeFeedsForChannel(ctx context.Context, db *database.Client, guildID, channelID, reason string) {
	if _, err := db.Guilds().RemoveFeedsByChannel(ctx, guildID, channelID); err != nil {
		if !errors.Is(err, context.Canceled) {
			logger.For("feeds").Error("RemoveFeedsByChannel failed", "channel_id", channelID, "err", err)
		}
		return
	}
	if _, err := db.FeedChecks().DeleteByChannelID(ctx, guildID, channelID); err != nil {
		if !errors.Is(err, context.Canceled) {
			logger.For("feeds").Error("DeleteByChannelID failed", "channel_id", channelID, "err", err)
		}
		return
	}
	logger.For("feeds").Warn(reason+", removed feeds and feed checks", "channel_id", channelID)
}

func flushChannelBatches(ctx context.Context, db *database.Client, s *discordgo.Session, guildID string, channelBatches map[string][]sendItem, seen map[string]struct{}) {
	var nonEmpty []struct {
		channelID string
		list      []sendItem
	}
	for channelID, list := range channelBatches {
		if len(list) > 0 {
			nonEmpty = append(nonEmpty, struct {
				channelID string
				list      []sendItem
			}{channelID, list})
		}
	}
	if len(nonEmpty) == 0 {
		for channelID := range channelBatches {
			channelBatches[channelID] = nil
		}
		return
	}

	var seenMu sync.Mutex
	workerCount := min(len(nonEmpty), 8)
	var wg sync.WaitGroup
	wg.Add(workerCount)
	entryCh := make(chan struct {
		channelID string
		list      []sendItem
	})
	for range workerCount {
		go func() {
			defer wg.Done()
			for entry := range entryCh {
				select {
				case <-ctx.Done():
					return
				default:
				}
				flushChannel(ctx, db, s, guildID, entry.channelID, entry.list, seen, &seenMu)
			}
		}()
	}
	for _, entry := range nonEmpty {
		select {
		case <-ctx.Done():
			close(entryCh)
			wg.Wait()
			for channelID := range channelBatches {
				channelBatches[channelID] = nil
			}
			return
		case entryCh <- entry:
		}
	}
	close(entryCh)
	wg.Wait()
	for channelID := range channelBatches {
		channelBatches[channelID] = nil
	}
}

// persistFeedChunk persists feed_checks for items and adds to seen; call after each sent chunk.
func persistFeedChunk(ctx context.Context, db *database.Client, s *discordgo.Session, guildID string, chunk []sendItem, seen map[string]struct{}, seenMu *sync.Mutex) {
	for _, it := range chunk {
		fc := feed_checks.FeedCheck{
			GUID: it.compositeGUID,
			Feed: feedEntryToFeedChecksFeed(guildID, it.feed),
			Date: time.Now(),
		}
		if err := db.FeedChecks().Save(ctx, guildID, fc); err != nil {
			if !errors.Is(err, context.Canceled) {
				logger.For("feeds").Error("Save FeedCheck failed", "shard_id", s.ShardID, "err", err)
			}
		}
		if seen != nil {
			if seenMu != nil {
				seenMu.Lock()
			}
			seen[it.compositeGUID] = struct{}{}
			if seenMu != nil {
				seenMu.Unlock()
			}
		}
	}
}

func flushChannel(ctx context.Context, db *database.Client, s *discordgo.Session, guildID, channelID string, items []sendItem, seen map[string]struct{}, seenMu *sync.Mutex) {
	if len(items) == 0 {
		return
	}
	embeds := make([]*discordgo.MessageEmbed, len(items))
	for i := range items {
		embeds[i] = items[i].embed
	}
	w, threadID, webhookOK := getOrCreateChannelWebhook(s, guildID, channelID)
	usedWebhook := webhookOK && w != nil
	webhookSucceeded := true
	fallbackStartIndex := 0
	if usedWebhook {
		for i := 0; i < len(embeds); i += discord.MaxEmbedsPerMessage {
			end := min(i+discord.MaxEmbedsPerMessage, len(embeds))
			chunk := embeds[i:end]
			if globalSendLimiter != nil {
				if err := globalSendLimiter.Acquire(ctx, PriorityFeeds); err != nil {
					return
				}
			}
			err := SendBatchToChannel(ctx, s, w, threadID, channelID, "", chunk, true)
			if err != nil {
				if discord.IsUnknownChannel(err) {
					invalidateChannelWebhook(guildID, channelID)
					removeFeedsForChannel(ctx, db, guildID, channelID, "channel deleted (10003)")
					return
				}
				if discord.IsChannelUnavailable(err) {
					invalidateChannelWebhook(guildID, channelID)
					removeFeedsForChannel(ctx, db, guildID, channelID, "channel inaccessible (missing access)")
					return
				}
				if discord.IsMissingPermissions(err) {
					invalidateChannelWebhook(guildID, channelID)
					return
				}
				invalidateChannelWebhook(guildID, channelID)
				webhookSucceeded = false
				fallbackStartIndex = i
				break
			}
			persistFeedChunk(ctx, db, s, guildID, items[i:end], seen, seenMu)
			time.Sleep(feedWebhookThrottle)
		}
	}
	if !webhookSucceeded || !usedWebhook {
		for i := fallbackStartIndex; i < len(embeds); i += discord.MaxEmbedsPerMessage {
			end := min(i+discord.MaxEmbedsPerMessage, len(embeds))
			chunk := embeds[i:end]
			if globalSendLimiter != nil {
				if err := globalSendLimiter.Acquire(ctx, PriorityFeeds); err != nil {
					return
				}
			}
			err := SendBatchToChannel(ctx, s, nil, "", channelID, "", chunk, false)
			if err != nil {
				if discord.IsMissingPermissions(err) {
					return
				}
				if discord.IsUnknownChannel(err) {
					removeFeedsForChannel(ctx, db, guildID, channelID, "channel deleted (10003)")
					return
				}
				if discord.IsChannelNotText(err) {
					removeFeedsForChannel(ctx, db, guildID, channelID, "channel is non-text (50008)")
					return
				}
				if discord.IsChannelUnavailable(err) {
					removeFeedsForChannel(ctx, db, guildID, channelID, "channel inaccessible (missing access)")
					return
				}
				logger.For("feeds").Error("ChannelMessageSendComplex failed", "shard_id", s.ShardID, "channel_id", channelID, "err", err)
				return
			}
			persistFeedChunk(ctx, db, s, guildID, items[i:end], seen, seenMu)
			time.Sleep(feedWebhookThrottle)
		}
	}
	logger.For("feeds").Debug("feed batch sent", "guild_id", guildID, "channel_id", channelID, "shard_id", s.ShardID, "count", len(items))
}

// sendOneAndPin sends one message and pins it; on success adds compositeGUID to seen before Save.
func sendOneAndPin(ctx context.Context, db *database.Client, s *discordgo.Session, guildID string, feed guilds.FeedEntry, compositeGUID string, emb *discordgo.MessageEmbed, seen map[string]struct{}) {
	if globalSendLimiter != nil {
		if err := globalSendLimiter.Acquire(ctx, PriorityFeeds); err != nil {
			return
		}
	}
	msg, err := s.ChannelMessageSendComplex(feed.ChannelID, &discordgo.MessageSend{Embeds: []*discordgo.MessageEmbed{emb}})
	if err != nil {
		if discord.IsMissingPermissions(err) {
			return
		}
		if discord.IsUnknownChannel(err) {
			removeFeedsForChannel(ctx, db, guildID, feed.ChannelID, "channel deleted (10003)")
			return
		}
		if discord.IsChannelNotText(err) {
			removeFeedsForChannel(ctx, db, guildID, feed.ChannelID, "channel is non-text (50008)")
			return
		}
		if discord.IsChannelUnavailable(err) {
			removeFeedsForChannel(ctx, db, guildID, feed.ChannelID, "channel inaccessible (missing access)")
			return
		}
		logger.For("feeds").Error("ChannelMessageSendComplex pin failed", "shard_id", s.ShardID, "channel_id", feed.ChannelID, "err", err)
		return
	}
	handleFeedPinning(s, feed.ChannelID, msg.ID, feed.Subreddit)
	if seen != nil {
		seen[compositeGUID] = struct{}{}
	}
	fc := feed_checks.FeedCheck{
		GUID: compositeGUID,
		Feed: feedEntryToFeedChecksFeed(guildID, feed),
		Date: time.Now(),
	}
	if err := db.FeedChecks().Save(ctx, guildID, fc); err != nil {
		if !errors.Is(err, context.Canceled) {
			logger.For("feeds").Error("Save FeedCheck failed", "shard_id", s.ShardID, "err", err)
		}
	}
	logger.For("feeds").Debug("feed sent and pinned", "shard_id", s.ShardID, "guild_id", guildID, "channel_id", feed.ChannelID, "subreddit", feed.Subreddit)
}

func feedEntryToFeedChecksFeed(guildID string, e guilds.FeedEntry) feed_checks.Feed {
	return feed_checks.Feed{
		GuildID:   guildID,
		Subreddit: e.Subreddit,
		Title:     e.Title,
		Author:    e.Author,
		Pin:       e.Pin,
		PostType:  e.PostType,
		ChannelID: e.ChannelID,
	}
}

func itemAuthor(item *gofeed.Item) *gofeed.Person {
	if len(item.Authors) > 0 && item.Authors[0] != nil {
		return item.Authors[0]
	}
	return nil
}

func validateFeedItem(feed guilds.FeedEntry, item *gofeed.Item) bool {
	if feed.Author != "" {
		author := itemAuthor(item)
		if author == nil {
			return false
		}
		name := strings.TrimPrefix(strings.TrimPrefix(author.Name, "/u/"), "u/")
		name = strings.TrimSpace(strings.ToLower(name))
		expected := strings.ToLower(strings.TrimSpace(feed.Author))
		if name != expected {
			return false
		}
	}
	if feed.Title != "" {
		if !strings.HasPrefix(strings.ToLower(item.Title), feed.Title) {
			return false
		}
	}
	return true
}

// firstHTTPSURL matches the first https:// URL in content (stops at ", space, or <).
var firstHTTPSURL = regexp.MustCompile(`https://[^"\s<>]+`)

func hostIsIReddit(u string) bool {
	parsed, err := url.Parse(u)
	if err != nil {
		return false
	}
	return strings.ToLower(parsed.Host) == "i.redd.it"
}

func parseImageURLFromContent(content string) string {
	urls := firstHTTPSURL.FindAllString(content, -1)
	// First pass: prefer i.redd.it
	for _, u := range urls {
		if isImageURL(u) && hostIsIReddit(u) {
			return html.UnescapeString(u)
		}
	}
	// Second pass: any image-like URL
	for _, u := range urls {
		if isImageURL(u) {
			return html.UnescapeString(u)
		}
	}
	if len(urls) > 0 {
		return html.UnescapeString(urls[0])
	}
	return ""
}

func isImageURL(u string) bool {
	if u == "" {
		return false
	}
	if i := strings.IndexAny(u, "?#"); i != -1 {
		u = u[:i]
	}
	lower := strings.ToLower(u)
	if strings.HasSuffix(lower, ".jpg") ||
		strings.HasSuffix(lower, ".jpeg") ||
		strings.HasSuffix(lower, ".png") ||
		strings.HasSuffix(lower, ".webp") ||
		strings.HasSuffix(lower, ".gifv") ||
		strings.HasSuffix(lower, ".gif") {
		return true
	}
	parsed, err := url.Parse(u)
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Host)
	if host == "i.redd.it" || host == "preview.redd.it" ||
		strings.HasSuffix(host, ".imgur.com") {
		return true
	}
	switch host {
	case "a.thumbs.redditmedia.com", "b.thumbs.redditmedia.com",
		"pbs.twimg.com", "cdn.discordapp.com", "media.discordapp.net",
		"i.ibb.co":
		return true
	}
	return strings.HasSuffix(host, ".redgifs.com")
}

func buildFeedEmbed(feed guilds.FeedEntry, item *gofeed.Item) *discordgo.MessageEmbed {
	title := item.Title
	if len(title) > discord.MaxEmbedAuthorNameLength {
		title = title[:discord.MaxEmbedAuthorNameLength-1] + "…"
	}
	emb := &discordgo.MessageEmbed{
		Author: &discordgo.MessageEmbedAuthor{
			URL:     item.Link,
			Name:    title,
			IconURL: feedEmbedAuthorIconURL,
		},
		Color: discord.EmbedColor,
	}
	if item.Description != "" {
		desc := item.Description
		if len(desc) > discord.MaxEmbedDescriptionLength {
			desc = desc[:discord.MaxEmbedDescriptionLength-1] + "…"
		}
		emb.Description = desc
	}
	var imageLink string
	if item.Content != "" {
		imageLink = parseImageURLFromContent(item.Content)
	}
	if imageLink != "" && isImageURL(imageLink) {
		emb.Image = &discordgo.MessageEmbedImage{URL: imageLink, ProxyURL: imageLink}
	}
	footerText := "r/" + feed.Subreddit + " - " + feed.PostType
	if feed.Author != "" {
		footerText += " - u/" + feed.Author
	}
	if len(footerText) > discord.MaxEmbedFooterLength {
		footerText = footerText[:discord.MaxEmbedFooterLength]
	}
	emb.Footer = &discordgo.MessageEmbedFooter{Text: footerText}
	if item.Published != "" {
		emb.Timestamp = item.Published
	} else if item.PublishedParsed != nil {
		emb.Timestamp = item.PublishedParsed.Format(time.RFC3339)
	}
	return emb
}

func handleFeedPinning(s *discordgo.Session, channelID, messageID, subreddit string) {
	perms, err := s.State.UserChannelPermissions(s.State.User.ID, channelID)
	if err != nil || perms&discordgo.PermissionManageMessages != discordgo.PermissionManageMessages {
		return
	}
	pins, err := s.ChannelMessagesPinned(channelID)
	if err != nil {
		return
	}
	time.Sleep(feedPinThrottle)
	prefix := fmt.Sprintf("https://www.reddit.com/r/%s/", subreddit)
	for _, pin := range pins {
		if pin == nil {
			continue
		}
		if pin.Author == nil || pin.Author.ID != s.State.User.ID || len(pin.Embeds) == 0 || pin.Embeds[0].Author == nil {
			continue
		}
		if strings.HasPrefix(strings.ToLower(pin.Embeds[0].Author.URL), strings.ToLower(prefix)) {
			_ = s.ChannelMessageUnpin(channelID, pin.ID)
			time.Sleep(feedPinThrottle)
		}
	}
	_ = s.ChannelMessagePin(channelID, messageID)
}
