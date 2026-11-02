package bot

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"slices"
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
	feedRunInterval        = 30 * time.Second
	feedCleanupEveryRuns   = 10
	feedCheckLifespanDays  = 180
	feedEmbedAuthorIconURL = "https://images-eu.ssl-images-amazon.com/images/I/418PuxYS63L.png"
)

// Pauses between feed sends; variables so tests can remove them.
var (
	feedWebhookThrottle = 200 * time.Millisecond
	feedPinThrottle     = 200 * time.Millisecond
)

const redditFetchIdlePoll = 5 * time.Second

var (
	processFeedKeysMu sync.Mutex
	shardFeedCounts   = make(map[int]map[feedKey]int)
	processFeedCounts = make(map[feedKey]int)
	feedCycleOrder    []feedKey
	feedCycleIndex    int
)

func registerShardFeedCounts(shardID int, counts map[feedKey]int) {
	processFeedKeysMu.Lock()
	shardFeedCounts[shardID] = counts
	processFeedCounts = make(map[feedKey]int)
	for _, shardCounts := range shardFeedCounts {
		for k, n := range shardCounts {
			processFeedCounts[k] += n
		}
	}
	rebuildFeedCycleOrderLocked()
	processFeedKeysMu.Unlock()
}

func rebuildFeedCycleOrderLocked() {
	var cursorKey feedKey
	if feedCycleIndex < len(feedCycleOrder) {
		cursorKey = feedCycleOrder[feedCycleIndex]
	}
	keys := make([]feedKey, 0, len(processFeedCounts))
	for k := range processFeedCounts {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b feedKey) int {
		if na, nb := processFeedCounts[a], processFeedCounts[b]; na != nb {
			return nb - na
		}
		if c := strings.Compare(a.subreddit, b.subreddit); c != 0 {
			return c
		}
		return strings.Compare(a.postType, b.postType)
	})
	feedCycleOrder = keys
	feedCycleIndex = 0
	for i, k := range feedCycleOrder {
		if k == cursorKey {
			feedCycleIndex = i
			break
		}
	}
}

// advanceFeedCycleKey returns the next sub in popularity order (high → low), wrapping after the last.
func advanceFeedCycleKey() (feedKey, bool) {
	processFeedKeysMu.Lock()
	defer processFeedKeysMu.Unlock()
	if len(feedCycleOrder) == 0 {
		return feedKey{}, false
	}
	for range feedCycleOrder {
		key := feedCycleOrder[feedCycleIndex]
		feedCycleIndex++
		if feedCycleIndex >= len(feedCycleOrder) {
			feedCycleIndex = 0
		}
		if processFeedCounts[key] > 0 {
			return key, true
		}
	}
	return feedKey{}, false
}

// RunFeedLoop runs delivery for this shard's guilds on each tick.
func RunFeedLoop(ctx context.Context, db *database.Client, session *discordgo.Session, interval time.Duration) {
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
			runFeedCheck(ctx, db, session, runCount%feedCleanupEveryRuns == 0)
			runCount++
		}
	}
}

// RunRedditFetchLoop fetches one subreddit feed whenever Reddit's rate limit allows.
func RunRedditFetchLoop(ctx context.Context) {
	for {
		if err := ctx.Err(); err != nil {
			return
		}
		if until := reddit.CooldownUntil(); !until.IsZero() && time.Now().Before(until) {
			if !waitFeedCtx(ctx, time.Until(until)) {
				return
			}
			continue
		}
		keys, ok := advanceFeedCycleKey()
		if !ok {
			if !waitFeedCtx(ctx, redditFetchIdlePoll) {
				return
			}
			continue
		}
		feedKeyStr := keys.subreddit + "/" + keys.postType
		_, err := reddit.FetchFresh(ctx, keys.subreddit, keys.postType)
		if err != nil {
			if reddit.IsPermanent(err) {
				logger.For("feeds").Warn("fetchRedditRSS permanent failure", "key", feedKeyStr, "user_agent", httpclient.GetUserAgent(), "err", err)
			} else if !reddit.IsGlobalCooldown(err) {
				logger.For("feeds").Warn("fetchRedditRSS failed", "key", feedKeyStr, "user_agent", httpclient.GetUserAgent(), "err", err)
			}
		}
	}
}

func waitFeedCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return true
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
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

	d := newDiscordgoPort(s)
	st := newMongoSentState(db)

	var guildWork []guildFeeds
	localCounts := make(map[feedKey]int)

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
		guildWork = append(guildWork, guildFeeds{guild: g, feeds: feeds})
		for _, f := range feeds {
			localCounts[feedKey{f.Subreddit, f.PostType}]++
		}
	}

	registerShardFeedCounts(s.ShardID, localCounts)

	parsed := make(map[string]*gofeed.Feed)
	for key := range localCounts {
		feedKeyStr := key.subreddit + "/" + key.postType
		if feed, ok := reddit.CachedFeed(key.subreddit, key.postType); ok {
			parsed[feedKeyStr] = feed
		}
	}

	for _, gw := range guildWork {
		processGuildFeeds(ctx, d, st, gw.guild.ID, gw.feeds, parsed)
	}
}

type feedKey struct {
	subreddit, postType string
}

type guildFeeds struct {
	guild *discordgo.Guild
	feeds []guilds.FeedEntry
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

func removeFeedsForChannel(ctx context.Context, st SentState, guildID, channelID, reason string) {
	if _, err := st.RemoveFeedsByChannel(ctx, guildID, channelID); err != nil {
		if !errors.Is(err, context.Canceled) {
			logger.For("feeds").Error("RemoveFeedsByChannel failed", "channel_id", channelID, "err", err)
		}
		return
	}
	if _, err := st.DeleteFeedChecksByChannel(ctx, guildID, channelID); err != nil {
		if !errors.Is(err, context.Canceled) {
			logger.For("feeds").Error("DeleteByChannelID failed", "channel_id", channelID, "err", err)
		}
		return
	}
	logger.For("feeds").Warn(reason+", removed feeds and feed checks", "channel_id", channelID)
}

// processGuildFeeds delivers the not-yet-sent items of one guild's feeds. Which items were already sent is
// looked up for exactly the candidate GUIDs, so the number of feeds in a guild cannot push live items out of
// the dedupe set; GUIDs queued within this run are tracked so one item goes to a channel only once per run.
func processGuildFeeds(ctx context.Context, d DiscordPort, st SentState, guildID string, feeds []guilds.FeedEntry, parsed map[string]*gofeed.Feed) {
	type candidate struct {
		feed guilds.FeedEntry
		item *gofeed.Item
		guid string
	}
	var cands []candidate
	var guids []string
	for _, feed := range feeds {
		pf := parsed[feed.Subreddit+"/"+feed.PostType]
		if pf == nil {
			continue
		}
		// Iterate items in reverse RSS order so we send oldest first.
		for i := len(pf.Items) - 1; i >= 0; i-- {
			item := pf.Items[i]
			if item == nil || !validateFeedItem(feed, item) {
				continue
			}
			guid := feedItemCompositeGUID(item, feed.ChannelID)
			cands = append(cands, candidate{feed: feed, item: item, guid: guid})
			guids = append(guids, guid)
		}
	}
	if len(cands) == 0 {
		return
	}
	existing, err := st.ExistingFeedGUIDs(ctx, guildID, guids)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			logger.For("feeds").Error("ExistingFeedGUIDs failed", "shard_id", d.ShardID(), "guild_id", guildID, "err", err)
		}
		return
	}

	queued := make(map[string]struct{})
	channelBatches := make(map[string][]sendItem)
	for _, c := range cands {
		if ctx.Err() != nil {
			return
		}
		if _, ok := existing[c.guid]; ok {
			continue
		}
		if _, ok := queued[c.guid]; ok {
			continue
		}
		queued[c.guid] = struct{}{}
		emb := buildFeedEmbed(c.feed, c.item)
		if c.feed.Pin {
			flushChannelBatches(ctx, d, st, guildID, channelBatches)
			sendOneAndPin(ctx, d, st, guildID, c.feed, c.guid, emb)
			if !waitFeedCtx(ctx, feedPinThrottle) {
				return
			}
			continue
		}
		ch := c.feed.ChannelID
		channelBatches[ch] = append(channelBatches[ch], sendItem{embed: emb, feed: c.feed, compositeGUID: c.guid})
		if len(channelBatches[ch]) >= discord.MaxEmbedsPerMessage {
			flushChannel(ctx, d, st, guildID, ch, channelBatches[ch])
			channelBatches[ch] = nil
			if !waitFeedCtx(ctx, feedWebhookThrottle) {
				return
			}
		}
	}
	flushChannelBatches(ctx, d, st, guildID, channelBatches)
}

func flushChannelBatches(ctx context.Context, d DiscordPort, st SentState, guildID string, channelBatches map[string][]sendItem) {
	type batch struct {
		channelID string
		list      []sendItem
	}
	var nonEmpty []batch
	for channelID, list := range channelBatches {
		if len(list) > 0 {
			nonEmpty = append(nonEmpty, batch{channelID, list})
		}
	}
	resetBatches := func() {
		for channelID := range channelBatches {
			channelBatches[channelID] = nil
		}
	}
	if len(nonEmpty) == 0 {
		resetBatches()
		return
	}

	workerCount := min(len(nonEmpty), 8)
	var wg sync.WaitGroup
	wg.Add(workerCount)
	entryCh := make(chan batch)
	for range workerCount {
		go func() {
			defer wg.Done()
			for entry := range entryCh {
				if ctx.Err() != nil {
					return
				}
				flushChannel(ctx, d, st, guildID, entry.channelID, entry.list)
			}
		}()
	}
	for _, entry := range nonEmpty {
		select {
		case <-ctx.Done():
			close(entryCh)
			wg.Wait()
			resetBatches()
			return
		case entryCh <- entry:
		}
	}
	close(entryCh)
	wg.Wait()
	resetBatches()
}

// persistFeedChunk records the items of a delivered chunk. The write is detached from shutdown so that a
// delivered item is never re-sent on the next run.
func persistFeedChunk(ctx context.Context, d DiscordPort, st SentState, guildID string, chunk []sendItem) {
	for _, it := range chunk {
		fc := feed_checks.FeedCheck{
			GUID: it.compositeGUID,
			Feed: feedEntryToFeedChecksFeed(guildID, it.feed),
			Date: time.Now(),
		}
		saveFeedCheckDetached(ctx, d, st, guildID, fc)
	}
}

func saveFeedCheckDetached(ctx context.Context, d DiscordPort, st SentState, guildID string, fc feed_checks.FeedCheck) {
	err := detachedWrite(ctx, func(wctx context.Context) error {
		return st.SaveFeedCheck(wctx, guildID, fc)
	})
	if err != nil {
		logger.For("feeds").Error("Save FeedCheck failed", "shard_id", d.ShardID(), "err", err)
	}
}

// handleFeedSendFailure maps the error of an undelivered feed send to its terminal handling.
func handleFeedSendFailure(ctx context.Context, d DiscordPort, st SentState, guildID, channelID string, err error) {
	if ctx.Err() != nil {
		return
	}
	switch {
	case discord.IsUnknownChannel(err):
		d.InvalidateChannelWebhook(guildID, channelID)
		removeFeedsForChannel(ctx, st, guildID, channelID, "channel deleted (10003)")
	case discord.IsChannelUnavailable(err):
		d.InvalidateChannelWebhook(guildID, channelID)
		removeFeedsForChannel(ctx, st, guildID, channelID, "channel inaccessible (missing access)")
	case discord.IsChannelNotText(err):
		removeFeedsForChannel(ctx, st, guildID, channelID, "channel is non-text (50008)")
	case discord.IsMissingPermissions(err):
		d.InvalidateChannelWebhook(guildID, channelID)
	default:
		logger.For("feeds").Warn("feed send failed", "shard_id", d.ShardID(), "channel_id", channelID, "err", err)
	}
}

func flushChannel(ctx context.Context, d DiscordPort, st SentState, guildID, channelID string, items []sendItem) {
	if len(items) == 0 {
		return
	}
	webhook := d.ChannelWebhook(guildID, channelID)
	for i := 0; i < len(items); i += discord.MaxEmbedsPerMessage {
		end := min(i+discord.MaxEmbedsPerMessage, len(items))
		embeds := make([]*discordgo.MessageEmbed, 0, end-i)
		for _, it := range items[i:end] {
			embeds = append(embeds, it.embed)
		}
		if globalSendLimiter != nil {
			if err := globalSendLimiter.Acquire(ctx, PriorityFeeds); err != nil {
				return
			}
		}
		res, _ := feedSend(ctx, d, guildID, channelID, webhook, OutgoingMessage{Embeds: embeds})
		if res.WebhookGone {
			// The webhook was invalidated: later chunks go through the bot account instead of failing on it again.
			webhook = nil
		}
		if !res.Delivered {
			handleFeedSendFailure(ctx, d, st, guildID, channelID, res.Err)
			return
		}
		persistFeedChunk(ctx, d, st, guildID, items[i:end])
		if !waitFeedCtx(ctx, feedWebhookThrottle) {
			return
		}
	}
	logger.For("feeds").Debug("feed batch sent", "guild_id", guildID, "channel_id", channelID, "shard_id", d.ShardID(), "count", len(items))
}

// sendOneAndPin sends one message through the bot account (a message ID is needed to pin), records it, then pins it.
func sendOneAndPin(ctx context.Context, d DiscordPort, st SentState, guildID string, feed guilds.FeedEntry, compositeGUID string, emb *discordgo.MessageEmbed) {
	if globalSendLimiter != nil {
		if err := globalSendLimiter.Acquire(ctx, PriorityFeeds); err != nil {
			return
		}
	}
	res, msgID := feedSend(ctx, d, guildID, feed.ChannelID, nil, OutgoingMessage{Embeds: []*discordgo.MessageEmbed{emb}})
	if !res.Delivered {
		handleFeedSendFailure(ctx, d, st, guildID, feed.ChannelID, res.Err)
		return
	}
	saveFeedCheckDetached(ctx, d, st, guildID, feed_checks.FeedCheck{
		GUID: compositeGUID,
		Feed: feedEntryToFeedChecksFeed(guildID, feed),
		Date: time.Now(),
	})
	if msgID != "" {
		d.PinReplacing(feed.ChannelID, msgID, feed.Subreddit)
	}
	logger.For("feeds").Debug("feed sent and pinned", "shard_id", d.ShardID(), "guild_id", guildID, "channel_id", feed.ChannelID, "subreddit", feed.Subreddit)
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
