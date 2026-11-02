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
	for _, s := range d.Shows {
		if s != nil && strings.EqualFold(s.Show, showName) {
			s.Notified = true
			s.LastNotifiedAirUnix = airUnix
			s.LastNotifiedEpisode = episodeKey
		}
	}
	return nil
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
