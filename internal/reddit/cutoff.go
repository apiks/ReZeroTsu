package reddit

import (
	"errors"
	"time"
)

// Cutoff is when Reddit feeds are retired: Reddit shuts down the RSS feeds the bot reads on 13 November 2026.
// REMOVE with the rest of the Reddit retirement code on 2026-11-27.
var Cutoff = time.Date(2026, time.November, 13, 0, 0, 0, 0, time.UTC)

// Now is the clock the cutoff is judged by; tests move it.
var Now = time.Now

// ErrRetired is what every fetch returns from the cutoff. It is final, never a failure to retry or log per feed.
var ErrRetired = errors.New("reddit feeds are retired")

// Retired reports whether Reddit feeds are retired at the current time.
func Retired() bool {
	return !Now().Before(Cutoff)
}
