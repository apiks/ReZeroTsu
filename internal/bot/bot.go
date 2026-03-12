package bot

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"ReZeroTsu/internal/animeschedule"
	"ReZeroTsu/internal/commands"
	_ "ReZeroTsu/internal/commands/about"
	_ "ReZeroTsu/internal/commands/animesubs"
	_ "ReZeroTsu/internal/commands/autopost"
	_ "ReZeroTsu/internal/commands/avatar"
	_ "ReZeroTsu/internal/commands/feeds"
	_ "ReZeroTsu/internal/commands/help"
	_ "ReZeroTsu/internal/commands/invite"
	_ "ReZeroTsu/internal/commands/joke"
	_ "ReZeroTsu/internal/commands/owner"
	_ "ReZeroTsu/internal/commands/pick"
	_ "ReZeroTsu/internal/commands/ping"
	_ "ReZeroTsu/internal/commands/prune"
	_ "ReZeroTsu/internal/commands/raffle"
	_ "ReZeroTsu/internal/commands/react"
	_ "ReZeroTsu/internal/commands/reminders"
	_ "ReZeroTsu/internal/commands/roll"
	_ "ReZeroTsu/internal/commands/say"
	_ "ReZeroTsu/internal/commands/settings"
	"ReZeroTsu/internal/config"
	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/httpclient"
	"ReZeroTsu/internal/logger"

	"github.com/bwmarrin/discordgo"
	"github.com/servusdei2018/shards/v2"
)

const zerotsuBotID = "614495694769618944"

// slashCommandGuildID: guild for slash registration; empty = global (propagation delay up to 1h).
const slashCommandGuildID = ""

var (
	globalSendLimiter        *SendLimiter
	globalSendLimiterOnce    sync.Once
	globalWebhookLimiter     *WebhookLimiter
	globalWebhookLimiterOnce sync.Once
)

type shardRuntime struct {
	mgr   *shards.Manager
	start time.Time
}

func (r *shardRuntime) GuildCount() int       { return r.mgr.GuildCount() }
func (r *shardRuntime) ShardCount() int       { return r.mgr.ShardCount }
func (r *shardRuntime) Uptime() time.Duration { return time.Since(r.start) }

// Run starts the shard manager, registers handlers and slash commands, blocks until ctx is cancelled, then shuts down.
func Run(ctx context.Context, cfg *config.Config, db *database.Client) error {
	commands.SetOwnerID(cfg.OwnerID)
	mgr, err := shards.New("Bot " + cfg.BotToken)
	if err != nil {
		return err
	}

	runtime := &shardRuntime{mgr: mgr, start: time.Now()}

	mgr.AddHandler(onReady(ctx, db, cfg, mgr))
	mgr.AddHandler(onGuildCreate(ctx, db, cfg))
	mgr.AddHandler(onGuildDelete())
	mgr.AddHandler(OnMessageCreate(ctx, db, cfg.OwnerID, cfg.Prefixes, runtime))
	mgr.AddHandler(OnVoiceStateUpdate(ctx, db))
	mgr.AddHandler(OnMessageReactionAdd(ctx, db))
	mgr.AddHandler(OnMessageReactionRemove(ctx, db))
	mgr.AddHandler(func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		commands.OnInteraction(ctx, db, s, i)
	})
	mgr.AddHandler(func(s *discordgo.Session, rl *discordgo.RateLimit) {
		if rl != nil {
			retryAfter := time.Duration(0)
			if rl.TooManyRequests != nil {
				retryAfter = rl.TooManyRequests.RetryAfter
			}
			source := inferRateLimitSource()
			logger.For("discord").Warn("rate limit (429)", "url", rl.URL, "retry_after", retryAfter, "shard_id", s.ShardID, "source", source)
		}
	})

	// Enable Server Members Intent in Discord Developer Portal (Bot → Privileged Gateway Intents).
	mgr.RegisterIntent(discordgo.MakeIntent(discordgo.IntentsAllWithoutPrivileged + discordgo.IntentGuildMembers))

	if err := mgr.Start(); err != nil {
		return err
	}
	logger.PrintAlways("Discord shards started", "shard_count", mgr.ShardCount)

	<-ctx.Done()

	if globalSendLimiter != nil {
		globalSendLimiter.Stop()
	}
	if globalWebhookLimiter != nil {
		globalWebhookLimiter.Stop()
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()
	errCh := make(chan error, 1)
	go func() { errCh <- mgr.Shutdown() }()
	select {
	case err := <-errCh:
		return err
	case <-shutdownCtx.Done():
		logger.For("bot").Error("shutdown exceeded 15s", "err", context.DeadlineExceeded)
		return fmt.Errorf("shutdown: %w", context.DeadlineExceeded)
	}
}

func onReady(ctx context.Context, db *database.Client, cfg *config.Config, mgr *shards.Manager) func(*discordgo.Session, *discordgo.Ready) {
	var startRemindersOnce sync.Once
	var startScheduleLoopOnce sync.Once
	var startAnimeSubCleanupOnce sync.Once
	var startTopGGOnce sync.Once
	var registerSlashCommandsOnce sync.Once
	var readyCount int32
	var shardLoopsOnce sync.Map
	return func(s *discordgo.Session, _ *discordgo.Ready) {
		globalSendLimiterOnce.Do(func() {
			globalSendLimiter = NewSendLimiter()
			globalSendLimiter.Start()
		})
		globalWebhookLimiterOnce.Do(func() {
			globalWebhookLimiter = NewWebhookLimiter()
			globalWebhookLimiter.Start()
		})
		registerSlashCommandsOnce.Do(func() {
			if s.State.User == nil {
				logger.For("bot").Error("slash command registration skipped: State.User is nil")
				return
			}
			createdCommands, err := s.ApplicationCommandBulkOverwrite(s.State.User.ID, slashCommandGuildID, commands.Definitions())
			if err != nil {
				logger.For("bot").Error("ApplicationCommandBulkOverwrite failed", "err", err)
				return
			}
			logger.For("bot").Info("slash commands registered", "guild_id", slashCommandGuildID, "count", len(createdCommands))
		})
		logger.For("bot").Info("shard ready", "shard_id", s.ShardID)
		if n := atomic.AddInt32(&readyCount, 1); n == int32(mgr.ShardCount) {
			logger.PrintAlways("all shards ready")
			startTopGGOnce.Do(func() {
				if s.State.User != nil && s.State.User.ID == zerotsuBotID {
					go RunTopGGPostLoop(ctx, mgr, cfg)
				}
			})
		}
		startRemindersOnce.Do(func() {
			go RunReminderLoop(ctx, db, s, 15*time.Second)
		})
		backfillLastNotifiedOnce.Do(func() {
			go func() {
				if err := backfillLastNotifiedAirUnix(ctx, db, time.Now().UTC()); err != nil && !errors.Is(err, context.Canceled) {
					logger.For("animesubs").Error("backfillLastNotifiedAirUnix failed", "err", err)
				}
			}()
		})
		httpclient.SetUserAgent(config.UserAgent(cfg))
		getShardCount := func() int { return mgr.ShardCount }
		onceVal, _ := shardLoopsOnce.LoadOrStore(s.ShardID, &sync.Once{})
		onceVal.(*sync.Once).Do(func() {
			go RunFeedLoop(ctx, db, s, 15*time.Second, getShardCount)
			if cfg.AnimeScheduleAPIKey != "" {
				animeschedule.SetAPIKey(cfg.AnimeScheduleAPIKey)
				animeschedule.UpdateAnimeSchedule(ctx, cfg.AnimeScheduleAPIKey)
				startScheduleLoopOnce.Do(func() {
					go runScheduleLoop(ctx, cfg.AnimeScheduleAPIKey)
				})
				go RunDailyScheduleLoop(ctx, db, s, cfg.AnimeScheduleAPIKey, getShardCount)
				go RunAnimeSubLoop(ctx, db, s, 15*time.Second, getShardCount)
				startAnimeSubCleanupOnce.Do(func() {
					go RunAnimeSubFinishedCleanupLoop(ctx, db, 24*time.Hour)
				})
			}
		})
		if len(cfg.PlayingMsg) > 0 {
			msg := cfg.PlayingMsg[0]
			if len(cfg.PlayingMsg) > 1 {
				msg = cfg.PlayingMsg[rand.Intn(len(cfg.PlayingMsg))]
			}
			if err := s.UpdateGameStatus(0, msg); err != nil {
				logger.For("bot").Error("UpdateGameStatus failed", "err", err)
			}
		}
	}
}

// onGuildCreate ensures the guild document exists when the bot joins or loads a server.
func onGuildCreate(ctx context.Context, db *database.Client, cfg *config.Config) func(*discordgo.Session, *discordgo.GuildCreate) {
	return func(s *discordgo.Session, g *discordgo.GuildCreate) {
		if g.Unavailable {
			return
		}
		exists, err := db.Guilds().GetGuild(ctx, g.ID)
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				logger.For("bot").Error("GetGuild failed", "guild_id", g.ID, "err", err)
			}
			if err := db.Guilds().EnsureGuild(ctx, g.ID); err != nil && !errors.Is(err, context.Canceled) {
				logger.For("bot").Error("EnsureGuild failed", "guild_id", g.ID, "err", err)
			}
			return
		}
		if exists != nil {
			return
		}
		if err := db.Guilds().EnsureGuild(ctx, g.ID); err != nil {
			if !errors.Is(err, context.Canceled) {
				logger.For("bot").Error("EnsureGuild failed", "guild_id", g.ID, "err", err)
			}
			return
		}
		if cfg.NewGuildLogChannelID != "" && s.State.User != nil && s.State.User.ID == zerotsuBotID {
			if _, err := s.ChannelMessageSend(cfg.NewGuildLogChannelID, fmt.Sprintf("A DB entry has been created for guild: %s", g.Name)); err != nil {
				logger.For("bot").Error("ChannelMessageSend new-guild log failed", "guild_id", g.ID, "err", err)
			}
			time.Sleep(250 * time.Millisecond)
		}
	}
}

// onGuildDelete logs leave and invalidates webhook cache for the guild.
func onGuildDelete() func(*discordgo.Session, *discordgo.GuildDelete) {
	return func(_ *discordgo.Session, g *discordgo.GuildDelete) {
		invalidateGuildWebhooks(g.ID)
		if g.Guild != nil && g.Guild.Name != "" {
			logger.For("bot").Info("left guild", "guild_id", g.ID, "guild_name", g.Guild.Name)
		} else {
			logger.For("bot").Info("left guild", "guild_id", g.ID)
		}
	}
}

// inferRateLimitSource returns a short label for 429 source from call stack (e.g. animesubs, feeds).
func inferRateLimitSource() string {
	stack := string(debug.Stack())
	if strings.Contains(stack, "animesubs.go") {
		return "animesubs"
	}
	if strings.Contains(stack, "feeds.go") {
		return "feeds"
	}
	if strings.Contains(stack, "schedule.go") {
		return "schedule"
	}
	if strings.Contains(stack, "reminders.go") {
		return "reminders"
	}
	return "unknown"
}
