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
func RunFeedLoop(ctx context.Context, db *database.Client, session *discordgo.Session, ops RunOperations, senders *channelSenderFactory, interval time.Duration) {
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
			runFeedCheck(ctx, db, session, ops, senders, runCount%feedCleanupEveryRuns == 0)
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

func runFeedCheck(ctx context.Context, db *database.Client, s *discordgo.Session, d RunOperations, senders *channelSenderFactory, doCleanup bool) {
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
		processGuildFeeds(ctx, d, senders, st, gw.guild.ID, gw.feeds, parsed)
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
func processGuildFeeds(ctx context.Context, d RunOperations, senders *channelSenderFactory, st SentState, guildID string, feeds []guilds.FeedEntry, parsed map[string]*gofeed.Feed) {
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
			flushChannelBatches(ctx, d, senders, st, guildID, channelBatches)
			sendOneAndPin(ctx, d, senders, st, guildID, c.feed, c.guid, emb)
			if !waitFeedCtx(ctx, feedPinThrottle) {
				return
			}
			continue
		}
		ch := c.feed.ChannelID
		channelBatches[ch] = append(channelBatches[ch], sendItem{embed: emb, feed: c.feed, compositeGUID: c.guid})
		if len(channelBatches[ch]) >= discord.MaxEmbedsPerMessage {
			flushChannel(ctx, d, senders, st, guildID, ch, channelBatches[ch])
			channelBatches[ch] = nil
			if !waitFeedCtx(ctx, feedWebhookThrottle) {
				return
			}
		}
	}
	flushChannelBatches(ctx, d, senders, st, guildID, channelBatches)
}

func flushChannelBatches(ctx context.Context, d RunOperations, senders *channelSenderFactory, st SentState, guildID string, channelBatches map[string][]sendItem) {
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
				flushChannel(ctx, d, senders, st, guildID, entry.channelID, entry.list)
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

// persistFeedChunk records the items of a delivered chunk in one write. The write is detached from shutdown so
// that a delivered item is never re-sent on the next run.
func persistFeedChunk(ctx context.Context, d RunOperations, st SentState, guildID string, chunk []sendItem) {
	now := time.Now()
	fcs := make([]feed_checks.FeedCheck, 0, len(chunk))
	for _, it := range chunk {
		fcs = append(fcs, feed_checks.FeedCheck{
			GUID: it.compositeGUID,
			Feed: feedEntryToFeedChecksFeed(guildID, it.feed),
			Date: now,
		})
	}
	err := detachedWrite(ctx, func(wctx context.Context) error {
		return st.SaveFeedChecks(wctx, guildID, fcs)
	})
	if err != nil {
		logger.For("feeds").Error("Save FeedChecks failed", "shard_id", d.ShardID(), "count", len(fcs), "err", err)
	}
}

// handleFeedSendFailure maps an undelivered feed post (rejected or missing) to its terminal handling. A cancelled
// post is not a failure: the caller returns before it gets here.
func handleFeedSendFailure(ctx context.Context, d RunOperations, st SentState, guildID, channelID string, res PostResult) {
	switch reason := res.Terminal; reason {
	case discord.ChannelDeleted, discord.ChannelInaccessible, discord.ChannelNotText:
		removeFeedsForChannel(ctx, st, guildID, channelID, reason.String())
	case discord.ChannelMissingPermissions:
	default:
		logger.For("feeds").Warn("feed send failed", "shard_id", d.ShardID(), "channel_id", channelID, "err", res.Err)
	}
}

func flushChannel(ctx context.Context, d RunOperations, senders *channelSenderFactory, st SentState, guildID, channelID string, items []sendItem) {
	if len(items) == 0 {
		return
	}
	sender := senders.open(guildID, channelID, feedPolicy())
	for i := 0; i < len(items); i += discord.MaxEmbedsPerMessage {
		end := min(i+discord.MaxEmbedsPerMessage, len(items))
		embeds := make([]*discordgo.MessageEmbed, 0, end-i)
		for _, it := range items[i:end] {
			embeds = append(embeds, it.embed)
		}
		// After a rejected webhook the sender sends later chunks through the bot account.
		res := sender.post(ctx, OutgoingMessage{Embeds: embeds})
		switch res.Outcome {
		case PostCancelled:
			return
		case PostRejected, PostMissing:
			handleFeedSendFailure(ctx, d, st, guildID, channelID, res)
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
func sendOneAndPin(ctx context.Context, d RunOperations, senders *channelSenderFactory, st SentState, guildID string, feed guilds.FeedEntry, compositeGUID string, emb *discordgo.MessageEmbed) {
	res := senders.open(guildID, feed.ChannelID, feedPinPolicy()).post(ctx, OutgoingMessage{Embeds: []*discordgo.MessageEmbed{emb}})
	msgID := res.MessageID
	switch res.Outcome {
	case PostCancelled:
		return
	case PostRejected, PostMissing:
		handleFeedSendFailure(ctx, d, st, guildID, feed.ChannelID, res)
		return
	}
	persistFeedChunk(ctx, d, st, guildID, []sendItem{{feed: feed, compositeGUID: compositeGUID}})
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

// handleFeedPinning pins messageID after unpinning the bot's older pins for the subreddit. It does nothing unless
// the gateway state says the bot has Pin Messages on the channel (the parent channel for a thread, since threads
// carry no overwrites of their own). REST failures are logged, never returned.
func handleFeedPinning(s *discordgo.Session, channelID, messageID, subreddit string) {
	if s.State == nil || s.State.User == nil {
		return
	}
	ch, err := s.State.Channel(channelID)
	if err != nil {
		logger.For("feeds").Debug("feed pin skipped: channel not in state", "channel_id", channelID, "subreddit", subreddit, "err", err)
		return
	}
	s.State.RLock()
	guildID, permChannelID := ch.GuildID, channelID
	if (ch.Type == discordgo.ChannelTypeGuildPublicThread || ch.Type == discordgo.ChannelTypeGuildPrivateThread) && ch.ParentID != "" {
		permChannelID = ch.ParentID
	}
	s.State.RUnlock()
	perms, err := s.State.UserChannelPermissions(s.State.User.ID, permChannelID)
	if err != nil {
		logger.For("feeds").Debug("feed pin skipped: permissions not in state", "channel_id", channelID, "subreddit", subreddit, "err", err)
		return
	}
	// discordgo grants Administrator every bit it knows, which excludes Pin Messages.
	if perms&discordgo.PermissionAdministrator == 0 && perms&discord.PermissionPinMessages != discord.PermissionPinMessages {
		return
	}
	logPinFailure := func(msg string, err error) {
		logger.For("feeds").Warn(msg, append([]any{"guild_id", guildID, "channel_id", channelID, "subreddit", subreddit, "err", err}, discord.RESTAttrs(err)...)...)
	}
	pins, err := s.ChannelMessagesPinned(channelID)
	if err != nil {
		logPinFailure("feed pin: could not list pinned messages", err)
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
			if err := s.ChannelMessageUnpin(channelID, pin.ID); err != nil {
				logPinFailure("feed unpin failed", err)
			}
			time.Sleep(feedPinThrottle)
		}
	}
	if err := s.ChannelMessagePin(channelID, messageID); err != nil {
		logPinFailure("feed pin failed", err)
	}
}
