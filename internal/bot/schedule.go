package bot

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"time"

	"ReZeroTsu/internal/animeschedule"
	"ReZeroTsu/internal/commands/schedule"
	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/discord"
	"ReZeroTsu/internal/logger"

	"github.com/bwmarrin/discordgo"
)

const (
	dailyScheduleStaggerPerShard = 10 * time.Second
	postTypeDailySchedule        = "dailyschedule"
)

// runScheduleLoop refreshes AnimeSchedule cache every 15 min until ctx is cancelled.
func runScheduleLoop(ctx context.Context, apiKey string) {
	ticker := time.NewTicker(15 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			animeschedule.UpdateAnimeSchedule(ctx, apiKey)
		}
	}
}

// RunDailyScheduleLoop posts today's schedule to each guild's dailyschedule channel once per day (00:00 UTC); shards staggered by ID.
func RunDailyScheduleLoop(ctx context.Context, db *database.Client, session *discordgo.Session, apiKey string, getShardCount func() int) {
	if apiKey == "" {
		return
	}
	// Sleep until next 00:00 UTC
	now := time.Now().UTC()
	next := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
	d := time.Until(next)
	if d > 24*time.Hour {
		d = 0
	}
	initialWait := time.NewTimer(d)
	defer initialWait.Stop()
	select {
	case <-ctx.Done():
		return
	case <-initialWait.C:
	}
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	staggerTimer := time.NewTimer(0)
	defer staggerTimer.Stop()
	for {
		if session.ShardID > 0 {
			stagger := time.Duration(session.ShardID) * dailyScheduleStaggerPerShard
			if !staggerTimer.Stop() {
				select {
				case <-staggerTimer.C:
				default:
				}
			}
			staggerTimer.Reset(stagger)
			select {
			case <-ctx.Done():
				return
			case <-staggerTimer.C:
			}
		}
		runDailySchedulePost(ctx, db, session)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func runDailySchedulePost(ctx context.Context, db *database.Client, s *discordgo.Session) {
	if !animeschedule.HasData() {
		return
	}
	weekday := int(time.Now().UTC().Weekday())
	d := newDiscordgoPort(s)
	st := newMongoSentState(db)
	dayShows := func(donghua bool) []animeschedule.ShowEntry { return animeschedule.GetDayShows(weekday, donghua) }
	guilds := make([]*discordgo.Guild, 0, len(s.State.Guilds))
	for _, g := range s.State.Guilds {
		if g == nil || g.ID == "" {
			continue
		}
		guilds = append(guilds, g)
	}
	if len(guilds) == 0 {
		return
	}

	guildCh := make(chan *discordgo.Guild)
	workerCount := max(min(runtime.NumCPU()*2, len(guilds)), 1)

	var wg sync.WaitGroup
	wg.Add(workerCount)
	for range workerCount {
		go func() {
			defer wg.Done()
			for g := range guildCh {
				if g == nil || g.ID == "" {
					continue
				}
				if err := processDailyScheduleForGuild(ctx, d, st, g.ID, weekday, dayShows, time.Now().UTC()); err != nil && !errors.Is(err, context.Canceled) {
					logger.For("schedule").Error("processDailyScheduleForGuild failed", "shard_id", s.ShardID, "guild_id", g.ID, "err", err)
				}
			}
		}()
	}

	for _, g := range guilds {
		select {
		case <-ctx.Done():
			close(guildCh)
			wg.Wait()
			return
		case guildCh <- g:
		}
	}
	close(guildCh)
	wg.Wait()
}

// scheduleRetryWait is the pause before the single retry of a verifiably missing schedule post.
var scheduleRetryWait = 3 * time.Second

// processDailyScheduleForGuild posts the daily schedule to one guild at most once per UTC day: one attempt
// following the shared send rules, then at most one retry when the post is verifiably missing (or verification
// itself failed, which is the opposite of the default policy). Rejected outcomes are never retried.
func processDailyScheduleForGuild(ctx context.Context, d DiscordPort, st SentState, guildID string, weekday int, dayShows weekdayShows, now time.Time) error {
	if guildID == "" {
		return nil
	}
	shardID := d.ShardID()
	today := now.UTC().Format("2006-01-02")
	ap, err := st.GetAutopost(ctx, guildID, postTypeDailySchedule)
	if err != nil || ap == nil || ap.ID == "" {
		return nil
	}
	if ap.LastPostedDate == today {
		return nil
	}
	if !d.CanSend(ap.ID) {
		return nil
	}
	settings, err := st.GetGuildSettings(ctx, guildID)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			logger.For("schedule").Error("GetGuildSettings failed", "shard_id", shardID, "guild_id", guildID, "err", err)
		}
	}
	donghua := true
	if settings != nil {
		donghua = settings.Donghua
	}
	emb := schedule.BuildScheduleEmbed(weekday, dayShows(donghua))
	msg := OutgoingMessage{Embeds: []*discordgo.MessageEmbed{emb}}
	vd := scheduleVerifyPort{DiscordPort: d, title: emb.Title, now: now}

	var res SendResult
	for attempt := range 2 {
		if attempt > 0 {
			t := time.NewTimer(scheduleRetryWait)
			select {
			case <-ctx.Done():
				t.Stop()
				return ctx.Err()
			case <-t.C:
			}
		}
		if globalSendLimiter != nil {
			if err := globalSendLimiter.Acquire(ctx, PrioritySchedule); err != nil {
				return err
			}
		}
		res = sendWithRules(ctx, vd, guildID, ap.ID, d.ChannelWebhook(guildID, ap.ID), msg)
		if res.Delivered && !res.Presumed {
			break
		}
		if res.Presumed {
			res.Err = errors.New("delivery could not be verified")
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if discord.ClassifySend(res.Err) == discord.Rejected {
			handleScheduleRejection(ctx, d, st, guildID, ap.ID, shardID, res.Err)
			return nil
		}
	}
	if !res.Delivered || res.Presumed {
		logger.For("schedule").Error("DailySchedule post failed", "shard_id", shardID, "guild_id", guildID, "channel_id", ap.ID, "err", res.Err)
		return nil
	}
	if err := detachedWrite(ctx, func(wctx context.Context) error { return st.MarkSchedulePosted(wctx, guildID, today) }); err != nil {
		logger.For("schedule").Error("failed to record last_posted_date", "shard_id", shardID, "guild_id", guildID, "err", err)
	}
	logger.For("schedule").Debug("daily schedule sent", "shard_id", shardID, "guild_id", guildID, "channel_id", ap.ID)
	return nil
}

// handleScheduleRejection applies the terminal handling for a Rejected schedule post: a deleted, non-text or
// inaccessible channel clears the autopost; other rejections are only logged.
func handleScheduleRejection(ctx context.Context, d DiscordPort, st SentState, guildID, channelID string, shardID int, err error) {
	var reason string
	switch {
	case discord.IsUnknownChannel(err):
		reason = "channel deleted (10003)"
	case discord.IsChannelNotText(err):
		reason = "channel is non-text"
	case discord.IsChannelUnavailable(err):
		reason = "channel inaccessible (10003/50001)"
	case discord.IsMissingPermissions(err):
		return
	default:
		logger.For("schedule").Error("DailySchedule post rejected", "shard_id", shardID, "guild_id", guildID, "channel_id", channelID, "err", err)
		return
	}
	if setErr := st.SetAutopost(ctx, guildID, postTypeDailySchedule, nil); setErr != nil {
		logger.For("schedule").Error(reason+", failed to clear autopost", "shard_id", shardID, "guild_id", guildID, "channel_id", channelID, "err", setErr)
	} else {
		logger.For("schedule").Warn(reason+", cleared dailyschedule autopost", "shard_id", shardID, "guild_id", guildID, "channel_id", channelID)
	}
	d.InvalidateChannelWebhook(guildID, channelID)
}

// channelStateFirst looks the channel up in the gateway state cache and only falls back to REST on a miss.
func channelStateFirst(s *discordgo.Session, channelID string) (*discordgo.Channel, error) {
	if s.State != nil {
		if ch, err := s.State.Channel(channelID); err == nil && ch != nil {
			return ch, nil
		}
	}
	return s.Channel(channelID)
}

// canSendToChannel reports whether the bot can send messages and embeds (View Channel, Send Messages, Embed Links; threads: Send in Threads).
func canSendToChannel(s *discordgo.Session, channelID string) bool {
	ch, err := channelStateFirst(s, channelID)
	if err != nil || ch == nil {
		return false
	}
	if s.State.User == nil {
		return false
	}
	perms, err := s.State.UserChannelPermissions(s.State.User.ID, channelID)
	if err != nil {
		return false
	}
	required := int64(discordgo.PermissionViewChannel | discordgo.PermissionSendMessages | discordgo.PermissionEmbedLinks)
	isThread := ch.Type == discordgo.ChannelTypeGuildPublicThread || ch.Type == discordgo.ChannelTypeGuildPrivateThread
	if isThread {
		required |= discordgo.PermissionSendMessagesInThreads
	}
	return perms&required == required
}
