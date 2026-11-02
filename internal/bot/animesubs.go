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
			updated, err := cleanupFinishedShows(ctx, db.AnimeSubs(), airing)
			if err != nil && !errors.Is(err, context.Canceled) {
				logger.For("animesubs").Error("finished cleanup IterateUserSubs failed", "err", err)
			}
			if updated > 0 {
				logger.For("animesubs").Debug("finished cleanup", "count", updated)
			}
		}
	}
}

// userSubsStore is the narrow persistence surface of the user-subscription maintenance jobs. It has no
// whole-document rewrite: each change is a single-statement operation on named shows.
type userSubsStore interface {
	IterateUserSubs(ctx context.Context, fn func(*anime_subs.AnimeSubs) error) error
	RemoveShows(ctx context.Context, id string, showNames []string) error
	SetShowAirUnix(ctx context.Context, id, showName string, airUnix int64) error
	// CollapseDuplicateShows drops null entries and every later entry whose show name repeats an earlier one
	// (case-insensitive, surrounding spaces ignored), in a single update statement.
	CollapseDuplicateShows(ctx context.Context, id string) error
}

// cleanupFinishedShows removes, per user document, the shows that are no longer airing, then collapses legacy
// duplicate entries (keeping the first) and null entries among the rest. It returns how many documents were
// changed. Each change is a single-statement update, so shows added or notified concurrently are not lost.
func cleanupFinishedShows(ctx context.Context, st userSubsStore, airing map[string]struct{}) (int, error) {
	var updated int
	err := st.IterateUserSubs(ctx, func(doc *anime_subs.AnimeSubs) error {
		if doc == nil {
			return nil
		}
		var finished []string
		needsCollapse := false
		seen := make(map[string]struct{}, len(doc.Shows))
		for _, sub := range doc.Shows {
			if sub == nil {
				needsCollapse = true
				continue
			}
			key := strings.ToLower(strings.TrimSpace(sub.Show))
			if _, ok := airing[key]; !ok {
				finished = append(finished, sub.Show)
				continue
			}
			if _, dup := seen[key]; dup {
				needsCollapse = true
			}
			seen[key] = struct{}{}
		}
		changed := false
		if len(finished) > 0 {
			if err := st.RemoveShows(ctx, doc.ID, finished); err != nil {
				logger.For("animesubs").Error("finished cleanup RemoveShows failed", "user_id", doc.ID, "err", err)
				return nil
			}
			changed = true
		}
		if needsCollapse {
			if err := st.CollapseDuplicateShows(ctx, doc.ID); err != nil {
				logger.For("animesubs").Error("finished cleanup CollapseDuplicateShows failed", "user_id", doc.ID, "err", err)
			} else {
				changed = true
			}
		}
		if changed {
			updated++
		}
		return nil
	})
	return updated, err
}

// backfillUserSubs sets the last notified air time on notified user shows that lack one, one show at a time,
// and returns how many user documents were changed. aired maps lower-cased show names to their latest air time.
func backfillUserSubs(ctx context.Context, st userSubsStore, aired map[string]int64) (int, error) {
	var updatedUsers int
	err := st.IterateUserSubs(ctx, func(doc *anime_subs.AnimeSubs) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if doc == nil {
			return nil
		}
		updated := false
		for _, sub := range doc.Shows {
			if sub == nil || sub.Guild || sub.LastNotifiedAirUnix != 0 || !sub.Notified {
				continue
			}
			key := strings.ToLower(strings.TrimSpace(sub.Show))
			if key == "" {
				continue
			}
			t, ok := aired[key]
			if !ok || t <= 0 {
				continue
			}
			if err := st.SetShowAirUnix(ctx, doc.ID, sub.Show, t); err != nil {
				logger.For("animesubs").Error("backfill SetShowAirUnix failed", "user_id", doc.ID, "err", err)
				continue
			}
			updated = true
		}
		if updated {
			updatedUsers++
		}
		return nil
	})
	return updatedUsers, err
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
	updatedUsers, err := backfillUserSubs(ctx, db.AnimeSubs(), shows)
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

// weekdayShows returns the shows for a weekday, optionally including donghua.
type weekdayShows func(donghua bool) []animeschedule.ShowEntry

// rolePresence is what the gateway state says about a role: present, definitely absent, or unknown.
type rolePresence int

const (
	// roleUnknown means the state cannot tell: the guild is missing (e.g. removed during an outage), is an
	// unavailable stub, or has no roles loaded. A configured role must not be cleared on it.
	roleUnknown rolePresence = iota
	rolePresent
	// roleAbsent means the guild is available with its roles loaded and the role is not among them.
	roleAbsent
)

func (p rolePresence) String() string {
	switch p {
	case rolePresent:
		return "present"
	case roleAbsent:
		return "absent"
	default:
		return "unknown"
	}
}

// guildRolePresence reports whether roleID is among g's roles. g is nil when the guild is not in the state.
func guildRolePresence(g *discordgo.Guild, roleID string) rolePresence {
	if g == nil || g.Unavailable || len(g.Roles) == 0 {
		return roleUnknown
	}
	for _, r := range g.Roles {
		if r != nil && r.ID == roleID {
			return rolePresent
		}
	}
	return roleAbsent
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
	d := newDiscordgoPort(s)
	st := newMongoSentState(db)
	dayShows := func(donghua bool) []animeschedule.ShowEntry { return animeschedule.GetDayShows(weekday, donghua) }

	// Per-shard guild seed for current weekday (once per day); missing shard entry = not yet seeded.
	lastGuildSeedWeekdayMu.RLock()
	lastSeen, seeded := lastGuildSeedWeekdayByShard[s.ShardID]
	needGuildSeed := !seeded || lastSeen != weekday
	lastGuildSeedWeekdayMu.RUnlock()
	if needGuildSeed {
		guildIDs := make([]string, 0, len(s.State.Guilds))
		for _, g := range s.State.Guilds {
			if g != nil && g.ID != "" {
				guildIDs = append(guildIDs, g.ID)
			}
		}
		seedGuildNewEpisodesForShard(ctx, st, guildIDs, dayShows, now)
		lastGuildSeedWeekdayMu.Lock()
		lastGuildSeedWeekdayByShard[s.ShardID] = weekday
		lastGuildSeedWeekdayMu.Unlock()
	}

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
					if err := processAnimeSubUser(ctx, d, st, doc, todayShows, now); err != nil && !errors.Is(err, context.Canceled) {
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
				processGuildNewEpisodes(ctx, d, st, g.ID, dayShows, now)
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

func processAnimeSubUser(ctx context.Context, d DiscordPort, st SentState, doc *anime_subs.AnimeSubs, todayShows []animeschedule.ShowEntry, now time.Time) error {
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

	var dmID string
	var err error
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
		epKey := episodeKey(show)
		if userShowAlreadyNotified(sub, show, epKey) {
			continue
		}

		if dmID == "" {
			dmID, err = d.OpenDM(ctx, userID)
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
		res := sendWithRules(ctx, d, "", dmID, nil, OutgoingMessage{Embeds: []*discordgo.MessageEmbed{emb}})
		if !res.Delivered {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			args := append(discord.RESTAttrs(res.Err), "user_id", userID, "err", res.Err)
			logger.For("animesubs").Warn("sendEpisodeNotification failed", args...)
			continue
		}
		// Record this one show right away, on a context that survives shutdown.
		if markErr := detachedWrite(ctx, func(wctx context.Context) error {
			return st.MarkUserShowNotified(wctx, userID, sub.Show, show.AirTimeUnix, epKey)
		}); markErr != nil {
			logger.For("animesubs").Error("mark user show notified failed", "user_id", userID, "title", show.Name, "err", markErr)
		}
		sub.LastNotifiedAirUnix = show.AirTimeUnix
		sub.LastNotifiedEpisode = epKey
		sub.Notified = true
		logger.For("animesubs").Debug(
			"episode notification sent",
			"user_id", userID,
			"title", show.Name,
			"air_time_unix", show.AirTimeUnix,
			"episode_key", epKey,
			"presumed", res.Presumed,
		)

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
	return nil
}

// episodeKey identifies one episode of a show: the show route plus the episode label.
func episodeKey(show animeschedule.ShowEntry) string {
	return show.Route + "|" + show.Episode
}

// userShowAlreadyNotified reports whether the subscriber was already notified of this episode: either the
// stored episode key matches, or (legacy documents) the air time is not later than the last-notified one.
func userShowAlreadyNotified(sub *anime_subs.ShowSub, show animeschedule.ShowEntry, epKey string) bool {
	if sub.LastNotifiedEpisode != "" && sub.LastNotifiedEpisode == epKey {
		return true
	}
	return show.AirTimeUnix <= sub.LastNotifiedAirUnix
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
func seedGuildNewEpisodesForShard(ctx context.Context, st SentState, guildIDs []string, shows weekdayShows, now time.Time) {
	today := now.UTC().Format("2006-01-02")

	for _, guildID := range guildIDs {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if guildID == "" {
			continue
		}
		ap, err := st.GetAutopost(ctx, guildID, postTypeNewEpisodes)
		if err != nil || ap == nil || ap.ID == "" {
			continue
		}
		settings, _ := st.GetGuildSettings(ctx, guildID)
		donghua := true
		if settings != nil {
			donghua = settings.Donghua
		}
		dayShows := shows(donghua)
		existing, err := st.GetAnimeSubs(ctx, ap.ID)
		if err != nil {
			logger.For("animesubs").Error("GetByID seed check failed", "channel_id", ap.ID, "err", err)
			continue
		}

		if existing != nil && existing.LastSeedDate == today && len(existing.Shows) > 0 && guildShowNamesMatch(dayShows, existing.Shows) {
			continue
		}

		if existing == nil || existing.LastSeedDate != today {
			// No document yet: start aired shows as notified (no repost on re-enable).
			// Stale document: new UTC day, reset everything to not notified (catch-up is intentional).
			showSubs := newGuildShowSubs(dayShows)
			if existing == nil {
				showSubs = seedGuildShowSubs(dayShows, now)
			}
			if err := st.SeedAnimeSubs(ctx, ap.ID, true, showSubs, today); err != nil {
				logger.For("animesubs").Error("Set guild channel reset failed", "channel_id", ap.ID, "err", err)
			}
			continue
		}

		// Same-day document: append newly listed shows, never rewrite existing entries.
		known := make(map[string]struct{}, len(existing.Shows))
		for _, sub := range existing.Shows {
			if sub != nil {
				known[strings.ToLower(sub.Show)] = struct{}{}
			}
		}
		var added []*anime_subs.ShowSub
		for _, show := range dayShows {
			key := strings.ToLower(show.Name)
			if _, ok := known[key]; ok {
				continue
			}
			known[key] = struct{}{}
			added = append(added, &anime_subs.ShowSub{Show: show.Name, Notified: false, Guild: true})
		}
		if err := st.AppendAnimeSubShows(ctx, ap.ID, added); err != nil {
			logger.For("animesubs").Error("Append guild channel shows failed", "channel_id", ap.ID, "err", err)
		}
	}
}

// newGuildShowSubs returns a guild show list with every show not yet notified.
func newGuildShowSubs(dayShows []animeschedule.ShowEntry) []*anime_subs.ShowSub {
	subs := make([]*anime_subs.ShowSub, 0, len(dayShows))
	for _, show := range dayShows {
		subs = append(subs, &anime_subs.ShowSub{Show: show.Name, Notified: false, Guild: true})
	}
	return subs
}

// seedGuildShowSubs returns the show list for a newly created guild document: shows that have
// already aired and are not delayed start as notified so enabling or moving the autopost does not repost them.
func seedGuildShowSubs(dayShows []animeschedule.ShowEntry, now time.Time) []*anime_subs.ShowSub {
	subs := newGuildShowSubs(dayShows)
	for i, show := range dayShows {
		if show.Delayed == "" && show.AirTimeUnix <= now.Unix() {
			subs[i].Notified = true
		}
	}
	return subs
}

func processGuildNewEpisodes(ctx context.Context, d DiscordPort, st SentState, guildID string, shows weekdayShows, now time.Time) {
	if guildID == "" {
		return
	}
	ap, err := st.GetAutopost(ctx, guildID, postTypeNewEpisodes)
	if err != nil {
		return
	}
	if ap == nil || ap.ID == "" {
		return
	}
	channelID := ap.ID
	roleID := ap.RoleID
	// Only a role the state definitely lacks is cleared: an unknown answer (guild missing or unavailable) keeps it.
	if roleID != "" && d.GuildRole(guildID, roleID) == roleAbsent {
		if err := st.SetAutopost(ctx, guildID, postTypeNewEpisodes, &guilds.Autopost{PostType: postTypeNewEpisodes, Name: ap.Name, ID: ap.ID, RoleID: ""}); err != nil {
			logger.For("animesubs").Error("clear invalid role failed", "guild_id", guildID, "role_id", roleID, "err", err)
		} else {
			logger.For("animesubs").Warn("role no longer exists, cleared from new-episodes autopost", "guild_id", guildID, "role_id", roleID)
		}
		roleID = ""
	}
	settings, _ := st.GetGuildSettings(ctx, guildID)
	donghua := true
	if settings != nil {
		donghua = settings.Donghua
	}
	todayShowsGuild := shows(donghua)
	doc, err := st.GetAnimeSubs(ctx, channelID)
	if err != nil {
		logger.For("animesubs").Error("GetByID failed", "channel_id", channelID, "err", err)
		return
	}
	if doc == nil || len(doc.Shows) == 0 {
		showSubs := seedGuildShowSubs(todayShowsGuild, now)
		if err := st.SeedAnimeSubs(ctx, channelID, true, showSubs, now.UTC().Format("2006-01-02")); err != nil {
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
	sender := newChannelSender(d, guildID, channelID)
	for i := 0; i < len(due); i += discord.MaxEmbedsPerMessage {
		if ctx.Err() != nil {
			return
		}
		end := min(i+discord.MaxEmbedsPerMessage, len(due))
		batch := due[i:end]
		embeds := make([]*discordgo.MessageEmbed, len(batch))
		names := make([]string, len(batch))
		for j, e := range batch {
			embeds[j] = e.emb
			names[j] = e.sub.Show
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
		// After a rejected webhook the sender sends later chunks through the bot account.
		res, _ := sender.send(ctx, OutgoingMessage{Content: content, Embeds: embeds})
		if res.Delivered {
			// A batch we cannot confirm as recorded would be resent next run, so record it right away
			// on a context that survives shutdown.
			markErr := detachedWrite(ctx, func(wctx context.Context) error {
				return st.MarkShowsNotified(wctx, channelID, names)
			})
			if markErr != nil {
				logger.For("animesubs").Error("mark shows notified failed", "channel_id", channelID, "err", markErr)
				return
			}
			logger.For("animesubs").Debug("new-episodes batch sent", "guild_id", guildID, "channel_id", channelID, "count", len(batch))
			continue
		}
		if ctx.Err() != nil {
			return
		}
		handleNewEpisodesRejection(ctx, d, st, guildID, channelID, res.Err)
		return
	}
}

// handleNewEpisodesRejection applies the terminal handling for an undelivered new-episodes batch: a deleted,
// inaccessible or non-text channel drops the channel's sent state and clears the autopost; anything else
// (including missing permissions) leaves both in place.
func handleNewEpisodesRejection(ctx context.Context, d DiscordPort, st SentState, guildID, channelID string, err error) {
	reason := discord.ClassifyChannelTerminal(err)
	switch reason {
	case discord.ChannelDeleted, discord.ChannelInaccessible, discord.ChannelNotText:
	default:
		return
	}
	_ = st.DeleteAnimeSubs(ctx, channelID)
	if setErr := st.SetAutopost(ctx, guildID, postTypeNewEpisodes, nil); setErr != nil {
		logger.For("animesubs").Error(reason.String()+", failed to clear autopost", "guild_id", guildID, "channel_id", channelID, "err", setErr)
	} else if reason == discord.ChannelNotText {
		logger.For("animesubs").Warn(reason.String()+", cleared new-episodes autopost", "guild_id", guildID, "channel_id", channelID)
	}
	d.InvalidateChannelWebhook(guildID, channelID)
}
