package bot

import (
	"os"
	"testing"
)

// TestMain zeroes the production throttles, so the suite does not sleep through pauses that exist to respect
// Discord's rate limits. A test that asserts a pause sets its own value and restores it.
func TestMain(m *testing.M) {
	animeSubThrottlePerUser = 0
	reminderThrottlePerUser = 0
	reminderThrottlePerMessage = 0
	os.Exit(m.Run())
}
