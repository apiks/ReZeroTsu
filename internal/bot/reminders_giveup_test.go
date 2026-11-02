package bot

import (
	"testing"
	"time"

	"ReZeroTsu/internal/database/reminders"
)

func TestReminderGivenUp(t *testing.T) {
	tests := []struct {
		name         string
		overdueBy    time.Duration
		failingSince time.Duration // how long ago failing_since was set; 0 means unset
		want         bool
	}{
		{"unmarked, 6 days overdue, kept", 6 * 24 * time.Hour, 0, false},
		{"unmarked, 8 days overdue, dropped", 8 * 24 * time.Hour, 0, true},
		{"marked 23h ago, kept", 72 * time.Hour, 23 * time.Hour, false},
		{"marked 25h ago, dropped", 72 * time.Hour, 25 * time.Hour, true},
		{"marked 23h ago, 8 days overdue, kept", 8 * 24 * time.Hour, 23 * time.Hour, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := reminders.RemindMe{Date: remindNow.Add(-tc.overdueBy)}
			if tc.failingSince > 0 {
				r.FailingSince = remindNow.Add(-tc.failingSince)
			}
			if got := reminderGivenUp(r, remindNow); got != tc.want {
				t.Errorf("reminderGivenUp = %v, want %v", got, tc.want)
			}
		})
	}
}
