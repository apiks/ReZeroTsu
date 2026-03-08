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
				if err := processDailyScheduleForGuild(ctx, db, s, g, weekday); err != nil && !errors.Is(err, context.Canceled) {
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

func processDailyScheduleForGuild(ctx context.Context, db *database.Client, s *discordgo.Session, g *discordgo.Guild, weekday int) error {
	if g == nil || g.ID == "" {
		return nil
	}
	ap, err := db.Guilds().GetAutopostByType(ctx, g.ID, "dailyschedule")
	if err != nil || ap == nil || ap.ID == "" {
		return nil
	}
	if !canSendToChannel(s, ap.ID) {
		return nil
	}
	settings, err := db.Guilds().GetGuildSettings(ctx, g.ID)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			logger.For("schedule").Error("GetGuildSettings failed", "shard_id", s.ShardID, "guild_id", g.ID, "err", err)
		}
	}
	donghua := true
	if settings != nil {
		donghua = settings.Donghua
	}
	shows := animeschedule.GetDayShows(weekday, donghua)
	emb := schedule.BuildScheduleEmbed(weekday, shows)
	chunk := []*discordgo.MessageEmbed{emb}

	const maxAttempts = 3
	backoff := []time.Duration{0, time.Second, 2 * time.Second}
	backoffTimer := time.NewTimer(backoff[1])
	defer backoffTimer.Stop()
	var sendErr error
	for attempt := range maxAttempts {
		if attempt > 0 {
			if !backoffTimer.Stop() {
				select {
				case <-backoffTimer.C:
				default:
				}
			}
			backoffTimer.Reset(backoff[attempt])
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-backoffTimer.C:
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if globalSendLimiter != nil {
			if err := globalSendLimiter.Acquire(ctx, PrioritySchedule); err != nil {
				return err
			}
		}
		w, threadID, webhookOK := getOrCreateChannelWebhook(s, g.ID, ap.ID)
		usedWebhook := webhookOK && w != nil
		sendErr = SendBatchToChannel(ctx, s, w, threadID, ap.ID, "", chunk, usedWebhook)
		if sendErr != nil && usedWebhook && !discord.IsUnknownChannel(sendErr) {
			invalidateChannelWebhook(g.ID, ap.ID)
		}
		if sendErr != nil {
			sendErr = SendBatchToChannel(ctx, s, nil, "", ap.ID, "", chunk, false)
		}
		if sendErr == nil {
			logger.For("schedule").Debug("daily schedule sent", "shard_id", s.ShardID, "guild_id", g.ID, "channel_id", ap.ID)
			break
		}
		if discord.IsUnknownChannel(sendErr) {
			if setErr := db.Guilds().SetAutopost(ctx, g.ID, "dailyschedule", nil); setErr != nil {
				logger.For("schedule").Error("channel deleted, failed to clear autopost", "shard_id", s.ShardID, "guild_id", g.ID, "channel_id", ap.ID, "err", setErr)
			} else {
				logger.For("schedule").Warn("channel deleted (10003), cleared dailyschedule autopost", "shard_id", s.ShardID, "guild_id", g.ID, "channel_id", ap.ID)
			}
			invalidateChannelWebhook(g.ID, ap.ID)
			break
		}
		if discord.IsChannelNotText(sendErr) {
			if setErr := db.Guilds().SetAutopost(ctx, g.ID, "dailyschedule", nil); setErr != nil {
				logger.For("schedule").Error("channel non-text, failed to clear autopost", "shard_id", s.ShardID, "guild_id", g.ID, "channel_id", ap.ID, "err", setErr)
			} else {
				logger.For("schedule").Warn("channel is non-text, cleared dailyschedule autopost", "shard_id", s.ShardID, "guild_id", g.ID, "channel_id", ap.ID)
			}
			invalidateChannelWebhook(g.ID, ap.ID)
			break
		}
		if discord.IsChannelUnavailable(sendErr) {
			if setErr := db.Guilds().SetAutopost(ctx, g.ID, "dailyschedule", nil); setErr != nil {
				logger.For("schedule").Error("channel inaccessible, failed to clear autopost", "shard_id", s.ShardID, "guild_id", g.ID, "channel_id", ap.ID, "err", setErr)
			} else {
				logger.For("schedule").Warn("channel inaccessible (10003/50001), cleared dailyschedule autopost", "shard_id", s.ShardID, "guild_id", g.ID, "channel_id", ap.ID)
			}
			invalidateChannelWebhook(g.ID, ap.ID)
			break
		}
		if discord.IsMissingPermissions(sendErr) {
			break
		}
		if attempt == maxAttempts-1 {
			logger.For("schedule").Error("DailySchedule post failed", "shard_id", s.ShardID, "channel_id", ap.ID, "err", sendErr)
			break
		}
	}
	return nil
}

// canSendToChannel reports whether the bot can send messages and embeds (View Channel, Send Messages, Embed Links; threads: Send in Threads).
func canSendToChannel(s *discordgo.Session, channelID string) bool {
	ch, err := s.Channel(channelID)
	if err != nil {
		ch, err = s.State.Channel(channelID)
	}
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
