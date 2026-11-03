package bot

import (
	"context"
	"errors"
	"fmt"
	"runtime"
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

const (
	animeSubRunInterval             = 15 * time.Second
	animeSubFinishedCleanupInterval = 24 * time.Hour
	postTypeNewEpisodes             = "newepisodes"
)

// animeSubThrottlePerUser pauses between the DMs of one user; a variable so tests can zero it.
var animeSubThrottlePerUser = 100 * time.Millisecond

var (
	lastGuildSeedDateByShard = make(map[int]string)
	lastGuildSeedDateMu      sync.RWMutex
	animeSubRoundRobin       ShardRoundRobin
)

// RunAnimeSubLoop runs the anime-sub check loop for this shard (round-robin). Exits when ctx is cancelled.
func RunAnimeSubLoop(ctx context.Context, db *database.Client, session *discordgo.Session, ops RunOperations, senders *channelSenderFactory, interval time.Duration, getShardCount func() int) {
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
			if !animeSubRoundRobin.RunWithTurn(session, getShardCount, func() { runAnimeSubCheck(ctx, db, session, ops, senders) }) {
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
			keep, ready := buildKeepRoster()
			if !ready {
				continue
			}
			updated, err := cleanupFinishedShows(ctx, db.AnimeSubs(), keep)
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
	// CollapseDuplicateShows drops null and nameless entries and keeps one entry per anime_subs.CollapseKey (the
	// one with the latest last-notified air time, ties going to the later entry), in a single update statement.
	// It reports whether the document was modified.
	CollapseDuplicateShows(ctx context.Context, id string) (bool, error)
}

// keepRoster is the set of shows whose subscriptions the cleanup keeps: shows on this week's or next week's
// timetable, on the upcoming list, or with a non-final entry last week (a break). Shows are held by route and by
// anime_subs.MatchKey of their title.
type keepRoster struct {
	routes map[string]struct{}
	keys   map[string]struct{}
}

// keeps matches a subscription by its route when it has one, otherwise by its title's match key (ADR-0002).
func (r keepRoster) keeps(sub *anime_subs.ShowSub) bool {
	if sub.Route != "" {
		_, ok := r.routes[sub.Route]
		return ok
	}
	_, ok := r.keys[anime_subs.MatchKey(sub.Show)]
	return ok
}

func (r keepRoster) add(route, title string) {
	if route != "" {
		r.routes[route] = struct{}{}
	}
	if key := anime_subs.MatchKey(title); key != "" {
		r.keys[key] = struct{}{}
	}
}

// buildKeepRoster reads the loaded timetable weeks and the upcoming list. It reports false, and the cleanup must
// delete nothing, unless the upcoming list has loaded and both this week's and next week's timetables have been
// fetched since startup and hold entries: with either missing, a show that airs now could look finished.
func buildKeepRoster() (keepRoster, bool) {
	r := keepRoster{routes: map[string]struct{}{}, keys: map[string]struct{}{}}
	if !animeschedule.UpcomingLoaded() {
		return r, false
	}
	weeks := animeschedule.SnapshotWeeks()
	if !weeks.ThisLoaded || !weeks.NextLoaded {
		return r, false
	}
	for _, e := range weeks.This {
		r.add(e.Route, e.Name)
	}
	for _, e := range weeks.Next {
		r.add(e.Route, e.Name)
	}
	// Last week: a show whose latest entry there was not its final episode may be on a break.
	for _, e := range animeschedule.OnBreak(weeks.Last) {
		r.add(e.Route, e.Name)
	}
	for _, u := range animeschedule.UpcomingShows() {
		r.add(u.Route, u.Name)
	}
	return r, true
}

// cleanupFinishedShows removes, per user document, the subscriptions the keep roster does not keep (see buildKeepRoster), then collapses legacy
// duplicate entries (keeping the most recently notified one) and null or nameless entries among the rest. It
// returns how many documents were changed. Each change is a single-statement update, so shows added or notified concurrently are not lost.
// keep says which subscriptions survive (see keepRoster).
func cleanupFinishedShows(ctx context.Context, st userSubsStore, keep keepRoster) (int, error) {
	var updated int
	err := st.IterateUserSubs(ctx, func(doc *anime_subs.AnimeSubs) error {
		if doc == nil {
			return nil
		}
		var finished []string
		needsCollapse := false
		seen := make(map[string]struct{}, len(doc.Shows))
		for _, sub := range doc.Shows {
			// The duplicate key must be the one the Mongo collapse computes, or a document could be flagged
			// forever with Mongo changing nothing. Entries without a name are removed by the collapse, like nulls.
			var dupKey string
			if sub != nil {
				dupKey = anime_subs.CollapseKey(sub.Show)
			}
			if dupKey == "" {
				needsCollapse = true
				continue
			}
			if !keep.keeps(sub) {
				finished = append(finished, sub.Show)
				continue
			}
			if _, dup := seen[dupKey]; dup {
				needsCollapse = true
			}
			seen[dupKey] = struct{}{}
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
			if modified, err := st.CollapseDuplicateShows(ctx, doc.ID); err != nil {
				logger.For("animesubs").Error("finished cleanup CollapseDuplicateShows failed", "user_id", doc.ID, "err", err)
			} else if modified {
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

func buildEpisodeEmbed(show animeschedule.ShowEntry) *discordgo.MessageEmbed {
	var desc string
	switch {
	case show.MediaType == animeschedule.MediaMovie && show.Track == animeschedule.TrackSub:
		desc = fmt.Sprintf("**%s** is out, subbed!", show.Name)
	case show.MediaType == animeschedule.MediaMovie && show.Track == animeschedule.TrackDub:
		desc = fmt.Sprintf("**%s** is out, dubbed!", show.Name)
	case show.MediaType == animeschedule.MediaMovie:
		desc = fmt.Sprintf("**%s** is out, raw!", show.Name)
	case show.Track == animeschedule.TrackDub:
		desc = fmt.Sprintf("**%s** dubbed is out!", show.Episode)
	case show.Track == animeschedule.TrackSub:
		desc = fmt.Sprintf("**%s** subbed is out!", show.Episode)
	default:
		desc = fmt.Sprintf("**%s** raw is out!", show.Episode)
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

// dayShowsFunc returns the shows of one UTC date, optionally including donghua.
type dayShowsFunc func(donghua bool, pref animeschedule.PreferredTrack) []animeschedule.ShowEntry

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

func runAnimeSubCheck(ctx context.Context, db *database.Client, s *discordgo.Session, d RunOperations, senders *channelSenderFactory) {
	if !animeschedule.APIKeyConfigured() || !animeschedule.HasData() {
		return
	}
	now := animeschedule.Now()
	today := animeschedule.DayOf(now)
	todayKey := animeschedule.DateKey(today)
	todayShows := animeschedule.GetDayShows(today, true, animeschedule.PreferSub)
	if len(todayShows) == 0 {
		return
	}
	st := newMongoSentState(db)
	dayShows := func(donghua bool, pref animeschedule.PreferredTrack) []animeschedule.ShowEntry {
		return animeschedule.GetDayShows(today, donghua, pref)
	}

	// Per-shard guild seed for the current UTC date (once per day); missing shard entry = not yet seeded.
	lastGuildSeedDateMu.RLock()
	lastSeen, seeded := lastGuildSeedDateByShard[s.ShardID]
	needGuildSeed := !seeded || lastSeen != todayKey
	lastGuildSeedDateMu.RUnlock()
	if needGuildSeed {
		guildIDs := make([]string, 0, len(s.State.Guilds))
		for _, g := range s.State.Guilds {
			if g != nil && g.ID != "" {
				guildIDs = append(guildIDs, g.ID)
			}
		}
		seedGuildNewEpisodesForShard(ctx, st, guildIDs, dayShows, now)
		lastGuildSeedDateMu.Lock()
		lastGuildSeedDateByShard[s.ShardID] = todayKey
		lastGuildSeedDateMu.Unlock()
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
					if err := processAnimeSubUser(ctx, d, senders, st, doc, todayShows, now); err != nil && !errors.Is(err, context.Canceled) {
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
				processGuildNewEpisodes(ctx, d, senders, st, g.ID, dayShows, now)
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

func processAnimeSubUser(ctx context.Context, d RunOperations, senders *channelSenderFactory, st SentState, doc *anime_subs.AnimeSubs, todayShows []animeschedule.ShowEntry, now time.Time) error {
	if doc == nil || len(doc.Shows) == 0 {
		return nil
	}
	userID := doc.ID

	// Among duplicate entries of a show, read and update the one a collapse keeps (most recently notified, ties
	// going to the later entry), so no notified state is lost when the duplicates are collapsed.
	// A subscription with a route is matched only by that route; one without is matched by its title's match key
	// (ADR-0002).
	subscriptionByRoute := make(map[string]*anime_subs.ShowSub)
	subscriptionByShow := make(map[string]*anime_subs.ShowSub)
	for _, sub := range doc.Shows {
		if sub == nil {
			continue
		}
		index, key := subscriptionByShow, anime_subs.MatchKey(sub.Show)
		if sub.Route != "" {
			index, key = subscriptionByRoute, sub.Route
		}
		if cur, ok := index[key]; ok && !anime_subs.SupersedesDuplicate(cur, sub) {
			continue
		}
		index[key] = sub
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
		sub, ok := subscriptionByRoute[show.Route]
		if !ok {
			if sub, ok = subscriptionByShow[anime_subs.MatchKey(show.Name)]; !ok {
				continue
			}
			// A title match records the route, so a later rename of the show still matches.
			if show.Route != "" {
				if routeErr := detachedWrite(ctx, func(wctx context.Context) error {
					return st.SetUserShowRoute(wctx, userID, sub.Show, show.Route)
				}); routeErr != nil {
					logger.For("animesubs").Error("save subscription route failed", "user_id", userID, "title", sub.Show, "err", routeErr)
				} else {
					sub.Route = show.Route
					delete(subscriptionByShow, anime_subs.MatchKey(sub.Show))
					subscriptionByRoute[show.Route] = sub
				}
			}
		}
		epKey := episodeKey(show)
		if userShowAlreadyNotified(sub, show, epKey) {
			continue
		}

		if dmID == "" {
			dmID, err = d.OpenDM(ctx, userID)
			if err != nil {
				if cerr := ctx.Err(); cerr != nil {
					// Shutdown aborted the request: nothing to report.
					return cerr
				}
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
		res := senders.open("", dmID, episodeDMPolicy()).post(ctx, OutgoingMessage{Embeds: []*discordgo.MessageEmbed{emb}})
		switch res.Outcome {
		case PostCancelled:
			return ctx.Err()
		case PostRejected, PostMissing:
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
			"presumed", res.Outcome == PostPresumed,
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

// airing identifies one row of the new-episodes autopost's sent state: the show's match key, its track and its
// episode.
type airing struct {
	show    string
	track   animeschedule.Track
	episode string
}

func airingOf(show animeschedule.ShowEntry) airing {
	return airing{show: anime_subs.MatchKey(show.Name), track: show.Track.Known(), episode: show.Episode}
}

func (a airing) entry(name string) *anime_subs.ShowSub {
	return &anime_subs.ShowSub{Show: name, Track: string(a.track), Episode: a.episode, Guild: true}
}

// sentIndex finds the stored sent-state entry of a row. An entry carrying a track and an episode matches the row
// of that identity. An entry from before the tracks release carries neither and matches that show's row of the
// Sub chain whatever its episode.
type sentIndex struct {
	exact  map[airing]*anime_subs.ShowSub
	legacy map[string]*anime_subs.ShowSub
	// subTracks holds, per show key, the tracks of the Sub chain's rows today.
	subTracks map[string]map[animeschedule.Track]bool
}

// newSentIndex indexes entries. Among entries of one identity a notified one wins, so a row any of them marks as
// posted is never reposted; the entry chosen is the one marked after sending. subChain is today's Sub chain rows.
func newSentIndex(entries []*anime_subs.ShowSub, subChain []animeschedule.ShowEntry) sentIndex {
	idx := sentIndex{
		exact:     map[airing]*anime_subs.ShowSub{},
		legacy:    map[string]*anime_subs.ShowSub{},
		subTracks: map[string]map[animeschedule.Track]bool{},
	}
	for _, row := range subChain {
		a := airingOf(row)
		if idx.subTracks[a.show] == nil {
			idx.subTracks[a.show] = map[animeschedule.Track]bool{}
		}
		idx.subTracks[a.show][a.track] = true
	}
	for _, entry := range entries {
		if entry == nil {
			continue
		}
		key := anime_subs.MatchKey(entry.Show)
		if entry.Track == "" && entry.Episode == "" {
			if cur, ok := idx.legacy[key]; !ok || (!cur.Notified && entry.Notified) {
				idx.legacy[key] = entry
			}
			continue
		}
		a := airing{show: key, track: animeschedule.Track(entry.Track).Known(), episode: entry.Episode}
		if cur, ok := idx.exact[a]; !ok || (!cur.Notified && entry.Notified) {
			idx.exact[a] = entry
		}
	}
	return idx
}

func (idx sentIndex) find(show animeschedule.ShowEntry) (*anime_subs.ShowSub, bool) {
	a := airingOf(show)
	if entry, ok := idx.exact[a]; ok {
		return entry, true
	}
	if entry, ok := idx.legacy[a.show]; ok && idx.subTracks[a.show][a.track] {
		return entry, true
	}
	return nil, false
}

// guildSentMatches reports whether the stored entries and today's rows identify the same airings (case-insensitive
// show names); it lets the same-day seeding skip a document that is already complete.
func guildSentMatches(dayShows []animeschedule.ShowEntry, entries []*anime_subs.ShowSub, subChain []animeschedule.ShowEntry) bool {
	idx := newSentIndex(entries, subChain)
	found := make(map[*anime_subs.ShowSub]struct{}, len(dayShows))
	for _, show := range dayShows {
		entry, ok := idx.find(show)
		if !ok {
			return false
		}
		found[entry] = struct{}{}
	}
	return len(found) == len(idx.exact)+len(idx.legacy)
}

// seedGuildNewEpisodesForShard seeds this shard's guild new-episodes docs with today's shows.
func seedGuildNewEpisodesForShard(ctx context.Context, st SentState, guildIDs []string, shows dayShowsFunc, now time.Time) {
	today := animeschedule.DateKey(now)

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
		donghua, pref := guildShowSettings(ctx, st, guildID)
		dayShows := shows(donghua, pref)
		existing, err := st.GetAnimeSubs(ctx, ap.ID)
		if err != nil {
			logger.For("animesubs").Error("GetByID seed check failed", "channel_id", ap.ID, "err", err)
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

		subChain := subChainOf(shows, dayShows, donghua, pref)
		if len(existing.Shows) > 0 && guildSentMatches(dayShows, existing.Shows, subChain) {
			continue
		}

		// Same-day document: append newly listed rows, never rewrite existing entries.
		idx := newSentIndex(existing.Shows, subChain)
		var added []*anime_subs.ShowSub
		for _, show := range dayShows {
			if _, ok := idx.find(show); ok {
				continue
			}
			sub := airingOf(show).entry(show.Name)
			idx.exact[airingOf(show)] = sub
			added = append(added, sub)
		}
		if err := st.AppendAnimeSubShows(ctx, ap.ID, added); err != nil {
			logger.For("animesubs").Error("Append guild channel shows failed", "channel_id", ap.ID, "err", err)
		}
	}
}

// guildShowSettings is the donghua setting and preferred track a server's new-episodes autopost reads: donghua on
// and Sub when the server has no settings.
func guildShowSettings(ctx context.Context, st SentState, guildID string) (donghua bool, pref animeschedule.PreferredTrack) {
	donghua, pref = true, animeschedule.PreferSub
	if settings, _ := st.GetGuildSettings(ctx, guildID); settings != nil {
		donghua, pref = settings.Donghua, settings.Preferred()
	}
	return donghua, pref
}

// subChainOf is today's Sub chain rows: dayShows when pref is already Sub, else a read under Sub.
func subChainOf(shows dayShowsFunc, dayShows []animeschedule.ShowEntry, donghua bool, pref animeschedule.PreferredTrack) []animeschedule.ShowEntry {
	if pref == animeschedule.PreferSub {
		return dayShows
	}
	return shows(donghua, animeschedule.PreferSub)
}

// recordMissingRows adds to the channel's document, and to idx, an entry for each of today's rows idx does not
// find: one that aired before it was added starts as posted, so a switch of track never reposts the day's earlier
// airings, and one still to air starts unnotified. It reports false when the entries could not be stored; the
// caller must then deliver nothing, or a row posted now could never be marked.
func recordMissingRows(ctx context.Context, st SentState, channelID string, idx sentIndex, dayShows []animeschedule.ShowEntry, now time.Time) bool {
	var added []*anime_subs.ShowSub
	for _, show := range dayShows {
		if _, ok := idx.find(show); ok {
			continue
		}
		entry := airingOf(show).entry(show.Name)
		entry.Notified = show.Delayed == "" && show.AirTimeUnix <= now.Unix()
		idx.exact[airingOf(show)] = entry
		added = append(added, entry)
	}
	if len(added) == 0 {
		return true
	}
	if err := st.AppendAnimeSubShows(ctx, channelID, added); err != nil {
		logger.For("animesubs").Error("Append guild channel shows failed", "channel_id", channelID, "err", err)
		return false
	}
	return true
}

// newGuildShowSubs returns a guild show list with every row not yet notified.
func newGuildShowSubs(dayShows []animeschedule.ShowEntry) []*anime_subs.ShowSub {
	subs := make([]*anime_subs.ShowSub, 0, len(dayShows))
	for _, show := range dayShows {
		subs = append(subs, airingOf(show).entry(show.Name))
	}
	return subs
}

// seedGuildShowSubs returns the show list for a newly created guild document: rows that have
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

func processGuildNewEpisodes(ctx context.Context, d RunOperations, senders *channelSenderFactory, st SentState, guildID string, shows dayShowsFunc, now time.Time) {
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
	donghua, pref := guildShowSettings(ctx, st, guildID)
	todayShowsGuild := shows(donghua, pref)
	doc, err := st.GetAnimeSubs(ctx, channelID)
	if err != nil {
		logger.For("animesubs").Error("GetByID failed", "channel_id", channelID, "err", err)
		return
	}
	if doc == nil || len(doc.Shows) == 0 {
		showSubs := seedGuildShowSubs(todayShowsGuild, now)
		if err := st.SeedAnimeSubs(ctx, channelID, true, showSubs, animeschedule.DateKey(now)); err != nil {
			logger.For("animesubs").Error("Set guild channel seed failed", "channel_id", channelID, "err", err)
		}
		doc = &anime_subs.AnimeSubs{ID: channelID, IsGuild: true, Shows: showSubs}
	}
	subChain := subChainOf(shows, todayShowsGuild, donghua, pref)
	sent := newSentIndex(doc.Shows, subChain)
	// A server that changed its preferred track today has no entries for the new chain's rows yet.
	if !recordMissingRows(ctx, st, channelID, sent, todayShowsGuild, now) {
		return
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
		sub, ok := sent.find(show)
		if !ok || sub.Notified {
			continue
		}
		due = append(due, dueEntry{show: show, sub: sub, emb: buildEpisodeEmbed(show)})
	}
	if len(due) == 0 {
		return
	}
	sender := senders.open(guildID, channelID, newEpisodesPolicy())
	for i := 0; i < len(due); i += discord.MaxEmbedsPerMessage {
		if ctx.Err() != nil {
			return
		}
		end := min(i+discord.MaxEmbedsPerMessage, len(due))
		batch := due[i:end]
		embeds := make([]*discordgo.MessageEmbed, len(batch))
		marks := make([]*anime_subs.ShowSub, len(batch))
		for j, e := range batch {
			embeds[j] = e.emb
			marks[j] = e.sub
		}
		content := ""
		if i == 0 && roleID != "" {
			content = "<@&" + roleID + ">"
		}
		// After a rejected webhook the sender sends later chunks through the bot account.
		res := sender.post(ctx, OutgoingMessage{Content: content, Embeds: embeds})
		switch res.Outcome {
		case PostDelivered, PostPresumed:
			// A batch we cannot confirm as recorded would be resent next run, so record it right away
			// on a context that survives shutdown.
			markErr := detachedWrite(ctx, func(wctx context.Context) error {
				return st.MarkShowsNotified(wctx, channelID, marks)
			})
			if markErr != nil {
				logger.For("animesubs").Error("mark shows notified failed", "channel_id", channelID, "err", markErr)
				return
			}
			logger.For("animesubs").Debug("new-episodes batch sent", "guild_id", guildID, "channel_id", channelID, "count", len(batch))
			continue
		case PostRejected:
			handleNewEpisodesRejection(ctx, st, guildID, channelID, res.Terminal)
		}
		// Rejected, missing or cancelled: stop here, the next run sends what is still due.
		return
	}
}

// handleNewEpisodesRejection applies the terminal handling for a rejected new-episodes batch: a deleted,
// inaccessible or non-text channel drops the channel's sent state and clears the autopost; anything else
// (including missing permissions) leaves both in place.
func handleNewEpisodesRejection(ctx context.Context, st SentState, guildID, channelID string, reason discord.ChannelTerminalReason) {
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
}
