package bot

import (
	"context"
	"strings"

	"ReZeroTsu/internal/database/anime_subs"
)

// Fake side of the ticket #7 SentState additions.

func (f *fakeSentState) MarkUserShowNotified(_ context.Context, id, showName string, airUnix int64, episodeKey string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.AnimeSubs[id]
	if !ok {
		return nil
	}
	// Exact name match, like the production array filter.
	for _, s := range d.Shows {
		if s != nil && s.Show == showName {
			s.Notified = true
			s.LastNotifiedAirUnix = airUnix
			s.LastNotifiedEpisode = episodeKey
		}
	}
	return nil
}

func (f *fakeSentState) SetUserShowRoute(_ context.Context, id, showName, route string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.AnimeSubs[id]
	if !ok {
		return nil
	}
	// Exact name match, only where no route is stored, like the production array filter.
	for _, s := range d.Shows {
		if s != nil && s.Show == showName && s.Route == "" {
			s.Route = route
		}
	}
	return nil
}

// collapseUserShows applies to the stored document the collapse documented on
// anime_subs.Repo.CollapseDuplicateShows: nil and nameless entries are dropped and, per CollapseKey, the entry
// with the greatest last-notified air time is kept (ties going to the later entry) at the first one's position.
func (f *fakeSentState) collapseUserShows(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.AnimeSubs[id]
	if !ok {
		return
	}
	var out []*anime_subs.ShowSub
	at := map[string]int{}
	for _, s := range d.Shows {
		if s == nil || anime_subs.CollapseKey(s.Show) == "" {
			continue
		}
		k := anime_subs.CollapseKey(s.Show)
		i, seen := at[k]
		if !seen {
			at[k] = len(out)
			out = append(out, s)
			continue
		}
		if s.LastNotifiedAirUnix >= out[i].LastNotifiedAirUnix {
			out[i] = s
		}
	}
	d.Shows = out
}

// userShow returns the stored subscription of user id for show, or nil.
func (f *fakeSentState) userShow(id, show string) *anime_subs.ShowSub {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.AnimeSubs[id]
	if !ok {
		return nil
	}
	for _, s := range d.Shows {
		if s != nil && strings.EqualFold(s.Show, show) {
			c := *s
			return &c
		}
	}
	return nil
}
