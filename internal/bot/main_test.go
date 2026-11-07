package bot

import (
	"os"
	"testing"
	"time"

	"ReZeroTsu/internal/reddit"
)

// TestMain zeroes the production throttles, so the suite does not sleep through pauses that exist to respect
// Discord's rate limits. A test that asserts a pause sets its own value and restores it.
func TestMain(m *testing.M) {
	animeSubThrottlePerUser = 0
	reminderThrottlePerUser = 0
	reminderThrottlePerMessage = 0
	// The Reddit clock stays before the cutoff, so tests of feed delivery do not depend on the real date. A test that
	// moves the clock uses setRedditNow, which restores this value.
	reddit.Now = func() time.Time { return time.Date(2026, time.November, 7, 9, 0, 0, 0, time.UTC) }
	os.Exit(m.Run())
}
