package reddit

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mmcdole/gofeed"
)

func setNow(t *testing.T, now time.Time) {
	t.Helper()
	old := Now
	Now = func() time.Time { return now }
	t.Cleanup(func() { Now = old })
}

func cacheFresh(t *testing.T) {
	t.Helper()
	storeCache("anime", "hot", &gofeed.Feed{Title: "fresh"})
	t.Cleanup(func() {
		mu.Lock()
		feedCache = make(map[string]feedCacheEntry)
		mu.Unlock()
	})
}

func TestFetchesAtOrAfterTheCutoffAreRetiredWithoutARequest(t *testing.T) {
	fetchers := map[string]func(context.Context, string, string) (*gofeed.Feed, error){
		"Fetch": Fetch, "FetchFresh": FetchFresh,
	}
	for _, at := range []time.Time{Cutoff, Cutoff.Add(48 * time.Hour)} {
		for name, fetch := range fetchers {
			t.Run(name+"/"+at.Format(time.RFC3339), func(t *testing.T) {
				setNow(t, at)
				cacheFresh(t)
				feed, err := fetch(context.Background(), "anime", "hot")
				if feed != nil || !errors.Is(err, ErrRetired) {
					t.Errorf("feed %v, err %v, want the retired error", feed, err)
				}
			})
		}
	}
}
