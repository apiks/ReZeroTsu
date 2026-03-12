package bot

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"time"

	"ReZeroTsu/internal/animeschedule"
	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/database/anime_subs"
	"ReZeroTsu/internal/database/guilds"
	"ReZeroTsu/internal/discord"
	"ReZeroTsu/internal/logger"

	"github.com/bwmarrin/discordgo"
)

var backfillLastNotifiedOnce sync.Once

const (
	animeSubRunInterval             = 15 * time.Second
	animeSubThrottlePerUser         = 100 * time.Millisecond
	animeSubFinishedCleanupInterval = 24 * time.Hour
	postTypeNewEpisodes             = "newepisodes"
)

var (
	lastGuildSeedWeekdayByShard = make(map[int]int)
	lastGuildSeedWeekdayMu      sync.RWMutex
	animeSubRoundRobin          ShardRoundRobin
)

// RunAnimeSubLoop runs the anime-sub check loop for this shard (round-robin). Exits when ctx is cancelled.
func RunAnimeSubLoop(ctx context.Context, db *database.Client, session *discordgo.Session, interval time.Duration, getShardCount func() int) {
	if interval <= 0 {
		interval = animeSubRunInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !animeSubRoundRobin.RunWithTurn(session, getShardCount, func() { runAnimeSubCheck(ctx, db, session) }) {
				continue
			}
		}
	}
}

// RunAnimeSubFinishedCleanupLoop removes finished-airing shows from user subs. Exits when ctx is cancelled.
func RunAnimeSubFinishedCleanupLoop(ctx context.Context, db *database.Client, interval time.Duration) {
	if interval <= 0 {
		interval = animeSubFinishedCleanupInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !animeschedule.APIKeyConfigured() || !animeschedule.HasData() {
				continue
			}
			airing := animeschedule.GetAiringShowNames()
			if len(airing) == 0 {
				continue
			}
			var updated int
			err := db.AnimeSubs().IterateUserSubs(ctx, func(doc *anime_subs.AnimeSubs) error {
				if len(doc.Shows) == 0 {
					return nil
				}
				var kept []*anime_subs.ShowSub
				seen := make(map[string]struct{})
				for _, sub := range doc.Shows {
					if sub != nil {
						key := strings.ToLower(strings.TrimSpace(sub.Show))
						if _, ok := airing[key]; ok {
							if _, dup := seen[key]; !dup {
								seen[key] = struct{}{}
								kept = append(kept, sub)
							}
						}
					}
				}
				if len(kept) < len(doc.Shows) {
					if err := db.AnimeSubs().Set(ctx, doc.ID, false, kept); err != nil {
						logger.For("animesubs").Error("finished cleanup Set failed", "channel_id", doc.ID, "err", err)
						return nil
					}
					updated++
				}
				return nil
			})
			if err != nil && !errors.Is(err, context.Canceled) {
				logger.For("animesubs").Error("finished cleanup IterateUserSubs failed", "err", err)
			}
			if updated > 0 {
				logger.For("animesubs").Debug("finished cleanup", "count", updated)
			}
		}
	}
}

func backfillLastNotifiedAirUnix(ctx context.Context, db *database.Client, now time.Time) error {
	if !animeschedule.APIKeyConfigured() || !animeschedule.HasData() {
		logger.For("animesubs").Debug("backfill skipped: no schedule data")
		return nil
	}
	shows := make(map[string]int64)
	nowUnix := now.Unix()
	for day := 0; day <= 6; day++ {
		for _, show := range animeschedule.GetDayShows(day, true) {
			if show.AirTimeUnix <= 0 || show.AirTimeUnix > nowUnix {
				continue
			}
			key := strings.ToLower(strings.TrimSpace(show.Name))
			if key == "" {
				continue
			}
			if show.AirTimeUnix > shows[key] {
				shows[key] = show.AirTimeUnix
			}
		}
	}
	var updatedUsers int
	err := db.AnimeSubs().IterateUserSubs(ctx, func(doc *anime_subs.AnimeSubs) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if doc == nil || len(doc.Shows) == 0 {
			return nil
		}
		updated := false
		for _, sub := range doc.Shows {
			if sub == nil || sub.Guild {
				continue
			}
			if sub.LastNotifiedAirUnix != 0 {
				continue
			}
			if !sub.Notified {
				continue
			}
			key := strings.ToLower(strings.TrimSpace(sub.Show))
			if key == "" {
				continue
			}
			if t, ok := shows[key]; ok && t > 0 {
				sub.LastNotifiedAirUnix = t
				updated = true
			}
		}
		if !updated {
			return nil
		}
		if err := db.AnimeSubs().Set(ctx, doc.ID, false, doc.Shows); err != nil {
			logger.For("animesubs").Error("backfill LastNotifiedAirUnix Set failed", "user_id", doc.ID, "err", err)
			return nil
		}
		updatedUsers++
		return nil
	})
	if err != nil {
		return err
	}
	if updatedUsers > 0 {
		logger.For("animesubs").Debug("backfill LastNotifiedAirUnix", "count", updatedUsers)
	}
	return nil
}

func buildEpisodeEmbed(show animeschedule.ShowEntry) *discordgo.MessageEmbed {
	desc := fmt.Sprintf("**%s** raw is out!", show.Episode)
	if show.AirType == "sub" {
		desc = fmt.Sprintf("**%s** subbed is out!", show.Episode)
	}
	url := animeschedule.BaseURL + "/anime/" + show.Route
	airTime := time.Unix(show.AirTimeUnix, 0).UTC()
	emb := &discordgo.MessageEmbed{
		Title:       show.Name,
		Description: desc,
		URL:         url,
		Color:       discord.EmbedColor,
		Timestamp:   airTime.Format(time.RFC3339),
		Author: &discordgo.MessageEmbedAuthor{
			URL:     "https://AnimeSchedule.net",
			Name:    "AnimeSchedule.net",
			IconURL: "https://cdn.animeschedule.net/production/assets/public/img/logos/as-logo-855bacd96c.png",
		},
	}
	if imgURL := show.ImageURL(); imgURL != "" {
		emb.Image = &discordgo.MessageEmbedImage{URL: imgURL}
	}
	return emb
}

// sendEpisodeNotification sends the episode embed to the channel (DM or guild new-episodes).
func sendEpisodeNotification(s *discordgo.Session, channelID string, emb *discordgo.MessageEmbed) error {
	_, err := s.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{
		Embeds: []*discordgo.MessageEmbed{emb},
	})
	return err
}

func roleExistsInGuild(g *discordgo.Guild, roleID string) bool {
	if g == nil || roleID == "" {
		return false
	}
	for _, r := range g.Roles {
		if r != nil && r.ID == roleID {
			return true
		}
	}
	return false
}

func runAnimeSubCheck(ctx context.Context, db *database.Client, s *discordgo.Session) {
	if !animeschedule.APIKeyConfigured() || !animeschedule.HasData() {
		return
	}
	now := time.Now().UTC()
	weekday := int(now.Weekday())
	todayShows := animeschedule.GetDayShows(weekday, true)
	if len(todayShows) == 0 {
		return
	}

	// Per-shard guild seed for current weekday (once per day); missing shard entry = not yet seeded.
	lastGuildSeedWeekdayMu.RLock()
	lastSeen, seeded := lastGuildSeedWeekdayByShard[s.ShardID]
	needGuildSeed := !seeded || lastSeen != weekday
	lastGuildSeedWeekdayMu.RUnlock()
	if needGuildSeed {
		seedGuildNewEpisodesForShard(ctx, db, s, weekday)
		lastGuildSeedWeekdayMu.Lock()
		lastGuildSeedWeekdayByShard[s.ShardID] = weekday
		lastGuildSeedWeekdayMu.Unlock()
	}

	// User DM delivery runs only on shard 0; ensure only one instance processes user subs per DB.
	if s.ShardID == 0 {
		userCh := make(chan *anime_subs.AnimeSubs)
		workerCount := max(runtime.NumCPU()*2, 1)
		var wg sync.WaitGroup
		wg.Add(workerCount)
		for range workerCount {
			go func() {
				defer wg.Done()
				for doc := range userCh {
					if doc == nil {
						continue
					}
					if err := processAnimeSubUser(ctx, db, s, doc, todayShows, now); err != nil && !errors.Is(err, context.Canceled) {
						logger.For("animesubs").Error("processAnimeSubUser failed", "user_id", doc.ID, "err", err)
					}
				}
			}()
		}

		err := db.AnimeSubs().IterateUserSubs(ctx, func(doc *anime_subs.AnimeSubs) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			if doc == nil || len(doc.Shows) == 0 {
				return nil
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case userCh <- doc:
				return nil
			}
		})
		close(userCh)
		wg.Wait()
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				logger.For("animesubs").Error("IterateUserSubs failed", "err", err)
			}
			return
		}
	}

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
	guildWorkers := max(min(runtime.NumCPU()*2, len(guilds)), 1)
	var gwg sync.WaitGroup
	gwg.Add(guildWorkers)
	for range guildWorkers {
		go func() {
			defer gwg.Done()
			for g := range guildCh {
				if g == nil || g.ID == "" {
					continue
				}
				select {
				case <-ctx.Done():
					return
				default:
				}
				processGuildNewEpisodes(ctx, db, s, g, weekday, now)
			}
		}()
	}
	for _, g := range guilds {
		select {
		case <-ctx.Done():
			close(guildCh)
			gwg.Wait()
			return
		case guildCh <- g:
		}
	}
	close(guildCh)
	gwg.Wait()
}

func processAnimeSubUser(ctx context.Context, db *database.Client, s *discordgo.Session, doc *anime_subs.AnimeSubs, todayShows []animeschedule.ShowEntry, now time.Time) error {
	if doc == nil || len(doc.Shows) == 0 {
		return nil
	}
	userID := doc.ID

	subByShow := make(map[string]*anime_subs.ShowSub)
	for _, sub := range doc.Shows {
		if sub != nil {
			subByShow[strings.ToLower(sub.Show)] = sub
		}
	}

	var dm *discordgo.Channel
	var err error
	updated := false
	throttleTimer := time.NewTimer(animeSubThrottlePerUser)
	defer throttleTimer.Stop()

	for _, show := range todayShows {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if show.Delayed != "" {
			continue
		}
		if show.AirTimeUnix > now.Unix() {
			continue
		}
		sub, ok := subByShow[strings.ToLower(show.Name)]
		if !ok {
			continue
		}
		if show.AirTimeUnix <= sub.LastNotifiedAirUnix {
			continue
		}

		if dm == nil {
			dm, err = s.UserChannelCreate(userID)
			if err != nil {
				logger.For("animesubs").Error("UserChannelCreate failed", "user_id", userID, "err", err)
				throttle := time.NewTimer(animeSubThrottlePerUser)
				defer throttle.Stop()
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-throttle.C:
				}
				return nil
			}
		}

		emb := buildEpisodeEmbed(show)
		if globalSendLimiter != nil {
			if acqErr := globalSendLimiter.Acquire(ctx, PriorityAnimeSubs); acqErr != nil {
				return acqErr
			}
		}
		if err := sendEpisodeNotification(s, dm.ID, emb); err != nil {
			args := append(discord.RESTAttrs(err), "user_id", userID, "err", err)
			if discord.IsCannotDMUser(err) {
				logger.For("animesubs").Warn("sendEpisodeNotification failed", args...)
			} else {
				logger.For("animesubs").Warn("sendEpisodeNotification failed", args...)
			}
			continue
		}
		sub.LastNotifiedAirUnix = show.AirTimeUnix
		sub.Notified = true
		logger.For("animesubs").Debug(
			"episode notification sent",
			"user_id", userID,
			"title", show.Name,
			"air_time_unix", show.AirTimeUnix,
			"last_notified_air_unix", sub.LastNotifiedAirUnix,
		)
		updated = true

		if !throttleTimer.Stop() {
			select {
			case <-throttleTimer.C:
			default:
			}
		}
		throttleTimer.Reset(animeSubThrottlePerUser)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-throttleTimer.C:
		}
	}

	if updated {
		if err := db.AnimeSubs().Set(ctx, userID, false, doc.Shows); err != nil {
			logger.For("animesubs").Error("Set user subs failed", "user_id", userID, "err", err)
		}
	}
	return nil
}

// guildShowNamesMatch reports whether dayShows and subs have the same show names (case-insensitive); used to preserve Notified across restarts.
func guildShowNamesMatch(dayShows []animeschedule.ShowEntry, subs []*anime_subs.ShowSub) bool {
	daySet := make(map[string]struct{}, len(dayShows))
	for _, show := range dayShows {
		daySet[strings.ToLower(show.Name)] = struct{}{}
	}
	subSet := make(map[string]struct{}, len(subs))
	for _, sub := range subs {
		if sub != nil {
			subSet[strings.ToLower(sub.Show)] = struct{}{}
		}
	}
	if len(daySet) != len(subSet) {
		return false
	}
	for k := range daySet {
		if _, ok := subSet[k]; !ok {
			return false
		}
	}
	return true
}

// seedGuildNewEpisodesForShard seeds this shard's guild new-episodes docs with today's shows.
func seedGuildNewEpisodesForShard(ctx context.Context, db *database.Client, s *discordgo.Session, weekday int) {
	today := time.Now().UTC().Format("2006-01-02")

	for _, g := range s.State.Guilds {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if g == nil || g.ID == "" {
			continue
		}
		ap, err := db.Guilds().GetAutopostByType(ctx, g.ID, postTypeNewEpisodes)
		if err != nil || ap == nil || ap.ID == "" {
			continue
		}
		settings, _ := db.Guilds().GetGuildSettings(ctx, g.ID)
		donghua := true
		if settings != nil {
			donghua = settings.Donghua
		}
		dayShows := animeschedule.GetDayShows(weekday, donghua)
		existing, err := db.AnimeSubs().GetByID(ctx, ap.ID)
		if err != nil {
			logger.For("animesubs").Error("GetByID seed check failed", "channel_id", ap.ID, "err", err)
			continue
		}

		if existing != nil && existing.LastSeedDate == today && len(existing.Shows) > 0 && guildShowNamesMatch(dayShows, existing.Shows) {
			continue
		}

		// Determine if this is a new day (reset all) or mid-day update (preserve states)
		isNewDay := existing == nil || existing.LastSeedDate != today

		existingStates := make(map[string]bool)
		if !isNewDay && existing != nil {
			for _, show := range existing.Shows {
				existingStates[show.Show] = show.Notified
			}
		}

		showSubs := make([]*anime_subs.ShowSub, 0, len(dayShows))
		for _, show := range dayShows {
			notified := false
			if !isNewDay {
				// Preserve state for mid-day updates, defaults to false for new shows
				notified = existingStates[show.Name]
			}
			showSubs = append(showSubs, &anime_subs.ShowSub{
				Show:     show.Name,
				Notified: notified,
				Guild:    true,
			})
		}

		if err := db.AnimeSubs().Set(ctx, ap.ID, true, showSubs); err != nil {
			logger.For("animesubs").Error("Set guild channel reset failed", "channel_id", ap.ID, "err", err)
		}
	}
}

func processGuildNewEpisodes(ctx context.Context, db *database.Client, s *discordgo.Session, g *discordgo.Guild, weekday int, now time.Time) {
	if g == nil || g.ID == "" {
		return
	}
	ap, err := db.Guilds().GetAutopostByType(ctx, g.ID, postTypeNewEpisodes)
	if err != nil {
		return
	}
	if ap == nil || ap.ID == "" {
		return
	}
	channelID := ap.ID
	roleID := ap.RoleID
	if roleID != "" && !roleExistsInGuild(g, roleID) {
		if err := db.Guilds().SetAutopost(ctx, g.ID, postTypeNewEpisodes, &guilds.Autopost{PostType: postTypeNewEpisodes, Name: ap.Name, ID: ap.ID, RoleID: ""}); err != nil {
			logger.For("animesubs").Error("clear invalid role failed", "guild_id", g.ID, "role_id", roleID, "err", err)
		} else {
			logger.For("animesubs").Warn("role no longer exists, cleared from new-episodes autopost", "guild_id", g.ID, "role_id", roleID)
		}
		roleID = ""
	}
	settings, _ := db.Guilds().GetGuildSettings(ctx, g.ID)
	donghua := true
	if settings != nil {
		donghua = settings.Donghua
	}
	todayShowsGuild := animeschedule.GetDayShows(weekday, donghua)
	doc, err := db.AnimeSubs().GetByID(ctx, channelID)
	if err != nil {
		logger.For("animesubs").Error("GetByID failed", "channel_id", channelID, "err", err)
		return
	}
	if doc == nil || len(doc.Shows) == 0 {
		showSubs := make([]*anime_subs.ShowSub, 0, len(todayShowsGuild))
		for _, show := range todayShowsGuild {
			showSubs = append(showSubs, &anime_subs.ShowSub{Show: show.Name, Notified: false, Guild: true})
		}
		if err := db.AnimeSubs().Set(ctx, channelID, true, showSubs); err != nil {
			logger.For("animesubs").Error("Set guild channel seed failed", "channel_id", channelID, "err", err)
		}
		doc = &anime_subs.AnimeSubs{ID: channelID, IsGuild: true, Shows: showSubs}
	}
	subByShow := make(map[string]*anime_subs.ShowSub)
	for _, sub := range doc.Shows {
		if sub != nil {
			subByShow[strings.ToLower(sub.Show)] = sub
		}
	}
	type dueEntry struct {
		show animeschedule.ShowEntry
		sub  *anime_subs.ShowSub
		emb  *discordgo.MessageEmbed
	}
	var due []dueEntry
	for _, show := range todayShowsGuild {
		if show.Delayed != "" {
			continue
		}
		if show.AirTimeUnix > now.Unix() {
			continue
		}
		if !donghua && show.Donghua {
			continue
		}
		sub, ok := subByShow[strings.ToLower(show.Name)]
		if !ok || sub.Notified {
			continue
		}
		due = append(due, dueEntry{show: show, sub: sub, emb: buildEpisodeEmbed(show)})
	}
	if len(due) == 0 {
		return
	}
	w, threadID, webhookOK := getOrCreateChannelWebhook(s, g.ID, channelID)
	updated := false
	for i := 0; i < len(due); i += discord.MaxEmbedsPerMessage {
		select {
		case <-ctx.Done():
			return
		default:
		}
		end := min(i+discord.MaxEmbedsPerMessage, len(due))
		batch := due[i:end]
		embeds := make([]*discordgo.MessageEmbed, len(batch))
		for j, e := range batch {
			embeds[j] = e.emb
		}
		content := ""
		if i == 0 && roleID != "" {
			content = "<@&" + roleID + ">"
		}
		if globalSendLimiter != nil {
			if err := globalSendLimiter.Acquire(ctx, PriorityAnimeSubs); err != nil {
				return
			}
		}
		usedWebhook := webhookOK && w != nil
		sendErr := SendBatchToChannel(ctx, s, w, threadID, channelID, content, embeds, usedWebhook)
		if sendErr != nil && usedWebhook && !discord.IsUnknownChannel(sendErr) {
			invalidateChannelWebhook(g.ID, channelID)
		}
		if sendErr != nil {
			sendErr = SendBatchToChannel(ctx, s, nil, "", channelID, content, embeds, false)
		}
		if sendErr != nil {
			if updated {
				if err := db.AnimeSubs().Set(ctx, channelID, true, doc.Shows); err != nil {
					logger.For("animesubs").Error("Set guild before break failed", "channel_id", channelID, "err", err)
				}
			}
			if discord.IsUnknownChannel(sendErr) || discord.IsChannelUnavailable(sendErr) {
				_ = db.AnimeSubs().DeleteByID(ctx, channelID)
				if setErr := db.Guilds().SetAutopost(ctx, g.ID, postTypeNewEpisodes, nil); setErr != nil {
					logger.For("animesubs").Error("channel deleted or inaccessible, failed to clear autopost", "guild_id", g.ID, "channel_id", channelID, "err", setErr)
				}
				invalidateChannelWebhook(g.ID, channelID)
				return
			}
			if discord.IsChannelNotText(sendErr) {
				_ = db.AnimeSubs().DeleteByID(ctx, channelID)
				if setErr := db.Guilds().SetAutopost(ctx, g.ID, postTypeNewEpisodes, nil); setErr != nil {
					logger.For("animesubs").Error("channel non-text, failed to clear autopost", "guild_id", g.ID, "channel_id", channelID, "err", setErr)
				}
				invalidateChannelWebhook(g.ID, channelID)
				logger.For("animesubs").Warn("channel is non-text, cleared new-episodes autopost", "guild_id", g.ID, "channel_id", channelID)
				return
			}
			if discord.IsMissingPermissions(sendErr) {
				return
			}
			return
		}
		logger.For("animesubs").Debug("new-episodes batch sent", "guild_id", g.ID, "channel_id", channelID, "count", len(batch))
		for _, e := range batch {
			e.sub.Notified = true
		}
		updated = true
	}
	if updated {
		if err := db.AnimeSubs().Set(ctx, channelID, true, doc.Shows); err != nil {
			logger.For("animesubs").Error("Set guild failed", "channel_id", channelID, "err", err)
		}
	}
}
