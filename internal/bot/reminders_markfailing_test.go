package bot

import (
	"context"
	"testing"
	"time"
)

// Several rejected reminders of one user are marked in a single write, not one write per reminder.
func TestProcessReminderSlice_RejectedRemindersMarkedInOneWrite(t *testing.T) {
	captureLogs(t)
	d := newFakeDiscord(rejectWith(0), rejectWith(0), rejectWith(0))
	st := stateWith("u1",
		overdueReminder(1, "a", 72*time.Hour),
		overdueReminder(2, "b", 72*time.Hour),
		overdueReminder(3, "c", 72*time.Hour))
	if err := runSlice(t, context.Background(), d, st, "u1"); err != nil {
		t.Fatal(err)
	}
	if st.MarkFailingCalls != 1 {
		t.Errorf("MarkRemindersFailing calls = %d, want 1", st.MarkFailingCalls)
	}
	for id := 1; id <= 3; id++ {
		if got := st.failingSince("u1", id); !got.Equal(remindNow) {
			t.Errorf("reminder %d failing_since = %v, want %v", id, got, remindNow)
		}
	}
}
