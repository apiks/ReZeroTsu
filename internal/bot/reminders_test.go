package bot

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"ReZeroTsu/internal/database/reminders"
	"ReZeroTsu/internal/discord"

	"github.com/bwmarrin/discordgo"
)

var remindNow = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

func dueReminder(id int, msg string) reminders.RemindMe {
	return reminders.RemindMe{Message: msg, Date: remindNow.Add(-time.Minute), RemindID: id, CreatedAt: remindNow.Add(-time.Hour)}
}

func futureReminder(id int, msg string) reminders.RemindMe {
	return reminders.RemindMe{Message: msg, Date: remindNow.Add(time.Hour), RemindID: id}
}

func stateWith(userID string, rs ...reminders.RemindMe) *fakeSentState {
	st := newFakeSentState()
	st.Reminders[userID] = &reminders.RemindMeSlice{ID: userID, Reminders: rs}
	return st
}

// captureLogs routes slog.Default to a buffer for the duration of the test.
type logBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *logBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *logBuf) count(level string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, line := range strings.Split(l.buf.String(), "\n") {
		if strings.Contains(line, "level="+level) {
			n++
		}
	}
	return n
}

func captureLogs(t *testing.T) *logBuf {
	t.Helper()
	lb := &logBuf{}
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(lb, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(old) })
	return lb
}

func runSlice(t *testing.T, ctx context.Context, d DiscordPort, st *fakeSentState, userID string) error {
	t.Helper()
	slice := st.snapshotReminders(userID)
	return processReminderSlice(ctx, d, st, slice, remindNow)
}

func TestProcessReminderSlice_Outcomes(t *testing.T) {
	tests := []struct {
		name      string
		script    []step
		recentErr error
		stored    []reminders.RemindMe
		wantLeft  []int
		wantSends int
	}{
		{
			name:      "delivered reminders are removed individually, future ones stay",
			stored:    []reminders.RemindMe{dueReminder(1, "a"), futureReminder(2, "b"), dueReminder(3, "c")},
			wantLeft:  []int{2},
			wantSends: 2,
		},
		{
			name:      "emptied document is deleted",
			stored:    []reminders.RemindMe{dueReminder(1, "a")},
			wantLeft:  nil,
			wantSends: 1,
		},
		{
			name:      "closed DMs drop the reminder after one attempt",
			script:    []step{rejectWith(discord.CannotDMUserCode)},
			stored:    []reminders.RemindMe{dueReminder(1, "a")},
			wantLeft:  nil,
			wantSends: 1,
		},
		{
			name:      "other rejection drops only that reminder",
			script:    []step{rejectWith(discord.MissingPermissionsCode), succeed()},
			stored:    []reminders.RemindMe{dueReminder(1, "a"), dueReminder(2, "b")},
			wantLeft:  nil,
			wantSends: 2,
		},
		{
			name:      "timeout after delivery is verified and removed",
			script:    []step{timeoutAfterDelivery()},
			stored:    []reminders.RemindMe{dueReminder(1, "a")},
			wantLeft:  nil,
			wantSends: 1,
		},
		{
			name:      "timeout without delivery is verified missing and kept for retry",
			script:    []step{timeoutWithoutDelivery()},
			stored:    []reminders.RemindMe{dueReminder(1, "a")},
			wantLeft:  []int{1},
			wantSends: 1,
		},
		{
			name:      "verification failure counts as delivered",
			script:    []step{timeoutWithoutDelivery()},
			recentErr: errors.New("boom"),
			stored:    []reminders.RemindMe{dueReminder(1, "a")},
			wantLeft:  nil,
			wantSends: 1,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			captureLogs(t)
			d := newFakeDiscord(tc.script...)
			d.RecentErr = tc.recentErr
			st := stateWith("u1", tc.stored...)
			if err := runSlice(t, context.Background(), d, st, "u1"); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := st.reminderIDs("u1"); !reflect.DeepEqual(got, tc.wantLeft) {
				t.Errorf("remaining reminders = %v, want %v", got, tc.wantLeft)
			}
			if d.SendCalls != tc.wantSends {
				t.Errorf("send calls = %d, want %d", d.SendCalls, tc.wantSends)
			}
		})
	}
}

func TestProcessReminderSlice_ClosedDMsLogOneWarningAndNeverRetry(t *testing.T) {
	logs := captureLogs(t)
	d := newFakeDiscord(rejectWith(discord.CannotDMUserCode))
	st := stateWith("u1", dueReminder(1, "a"))

	if err := runSlice(t, context.Background(), d, st, "u1"); err != nil {
		t.Fatal(err)
	}
	if got := logs.count("WARN"); got != 1 {
		t.Errorf("warning logs = %d, want 1", got)
	}
	if got := logs.count("ERROR"); got != 0 {
		t.Errorf("error logs = %d, want 0", got)
	}
	// A later run finds nothing due, so there is no retry.
	runDueRemindersForTest(t, d, st)
	if d.SendCalls != 1 {
		t.Errorf("send calls after second run = %d, want 1", d.SendCalls)
	}
}

func runDueRemindersForTest(t *testing.T, d DiscordPort, st *fakeSentState) {
	t.Helper()
	deliverDueReminders(context.Background(), d, st, remindNow)
}

func TestProcessReminderSlice_OpenDMFailure(t *testing.T) {
	rejected := &discordgo.RESTError{
		Response: &http.Response{StatusCode: http.StatusForbidden},
		Message:  &discordgo.APIErrorMessage{Code: discord.CannotDMUserCode, Message: "no"},
	}
	tests := []struct {
		name     string
		dmErr    error
		wantLeft []int
	}{
		{"rejected DM channel creation removes the user's due reminders", rejected, []int{3}},
		{"transient DM channel creation failure keeps them", errors.New("network"), []int{1, 2, 3}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			captureLogs(t)
			d := newFakeDiscord()
			d.DMError = tc.dmErr
			st := stateWith("u1", dueReminder(1, "a"), dueReminder(2, "b"), futureReminder(3, "c"))
			if err := runSlice(t, context.Background(), d, st, "u1"); err != nil {
				t.Fatal(err)
			}
			if got := st.reminderIDs("u1"); !reflect.DeepEqual(got, tc.wantLeft) {
				t.Errorf("remaining = %v, want %v", got, tc.wantLeft)
			}
			if d.SendCalls != 0 {
				t.Errorf("send calls = %d, want 0", d.SendCalls)
			}
		})
	}
}

func TestProcessReminderSlice_ShutdownRightAfterSendStillRecordsDelivery(t *testing.T) {
	captureLogs(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := &hookDiscord{fakeDiscord: newFakeDiscord(), afterSend: cancel}
	st := stateWith("u1", dueReminder(1, "a"), dueReminder(2, "b"))

	err := runSlice(t, ctx, d, st, "u1")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if got := st.reminderIDs("u1"); !reflect.DeepEqual(got, []int{2}) {
		t.Errorf("remaining = %v, want only the undelivered [2]", got)
	}
	// After restart the delivered reminder is not sent again.
	d2 := newFakeDiscord()
	deliverDueReminders(context.Background(), d2, st, remindNow)
	if d2.SendCalls != 1 || !strings.Contains(d2.Delivered[0].Msg.Embeds[0].Description, "b") {
		t.Errorf("after restart delivered %+v, want only reminder b", d2.Delivered)
	}
}

func TestProcessReminderSlice_RemindDuringDeliveryKeepsBoth(t *testing.T) {
	captureLogs(t)
	st := stateWith("u1", dueReminder(1, "a"))
	d := &hookDiscord{fakeDiscord: newFakeDiscord()}
	d.afterSend = func() {
		st.appendReminderDirect("u1", futureReminder(2, "new"))
	}
	if err := runSlice(t, context.Background(), d, st, "u1"); err != nil {
		t.Fatal(err)
	}
	if got := st.reminderIDs("u1"); !reflect.DeepEqual(got, []int{2}) {
		t.Errorf("remaining = %v, want the new reminder [2] and delivered one gone", got)
	}
}

func TestDeliverDueReminders_OnlyDueDocumentsAreProcessed(t *testing.T) {
	captureLogs(t)
	st := newFakeSentState()
	st.Reminders["u1"] = &reminders.RemindMeSlice{ID: "u1", Reminders: []reminders.RemindMe{dueReminder(1, "a")}}
	st.Reminders["u2"] = &reminders.RemindMeSlice{ID: "u2", Reminders: []reminders.RemindMe{futureReminder(1, "b")}}
	d := newFakeDiscord()

	deliverDueReminders(context.Background(), d, st, remindNow)

	if got := d.deliveredTo("dm-u1"); len(got) != 1 {
		t.Errorf("u1 deliveries = %d, want 1", len(got))
	}
	if got := d.deliveredTo("dm-u2"); len(got) != 0 {
		t.Errorf("u2 deliveries = %d, want 0", len(got))
	}
	if st.reminderIDs("u1") != nil || !reflect.DeepEqual(st.reminderIDs("u2"), []int{1}) {
		t.Errorf("state after run: u1=%v u2=%v", st.reminderIDs("u1"), st.reminderIDs("u2"))
	}
}
