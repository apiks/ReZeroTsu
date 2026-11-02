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

func restErr(status, code int) error {
	return &discordgo.RESTError{
		Response: &http.Response{StatusCode: status},
		Message:  &discordgo.APIErrorMessage{Code: code, Message: "err"},
	}
}

func dueReminder(id int, msg string) reminders.RemindMe {
	return reminders.RemindMe{Message: msg, Date: remindNow.Add(-time.Minute), RemindID: id, CreatedAt: remindNow.Add(-time.Hour)}
}

// overdueReminder is a reminder that was due by long before remindNow.
func overdueReminder(id int, msg string, by time.Duration) reminders.RemindMe {
	return reminders.RemindMe{Message: msg, Date: remindNow.Add(-by), RemindID: id, CreatedAt: remindNow.Add(-by - time.Hour)}
}

// failingReminder is a reminder three days overdue whose delivery Discord has been rejecting for failingFor.
func failingReminder(id int, msg string, failingFor time.Duration) reminders.RemindMe {
	r := overdueReminder(id, msg, 72*time.Hour)
	r.FailingSince = remindNow.Add(-failingFor)
	return r
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

// countContaining counts the log lines at level that contain text.
func (l *logBuf) countContaining(level, text string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, line := range strings.Split(l.buf.String(), "\n") {
		if strings.Contains(line, "level="+level) && strings.Contains(line, text) {
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
			name:      "unknown user drops the reminder after one attempt",
			script:    []step{rejectWith(discord.UnknownUserCode)},
			stored:    []reminders.RemindMe{dueReminder(1, "a")},
			wantLeft:  nil,
			wantSends: 1,
		},
		{
			name:      "other rejection keeps the reminder for retry",
			script:    []step{rejectWith(discord.MissingPermissionsCode), succeed()},
			stored:    []reminders.RemindMe{dueReminder(1, "a"), dueReminder(2, "b")},
			wantLeft:  []int{1},
			wantSends: 2,
		},
		{
			name:      "generic 4xx without a user-specific code keeps the reminder",
			script:    []step{rejectWith(0)},
			stored:    []reminders.RemindMe{dueReminder(1, "a")},
			wantLeft:  []int{1},
			wantSends: 1,
		},
		{
			name:      "first generic 4xx on a three-day-overdue reminder keeps it",
			script:    []step{rejectWith(0)},
			stored:    []reminders.RemindMe{overdueReminder(1, "a", 72*time.Hour)},
			wantLeft:  []int{1},
			wantSends: 1,
		},
		{
			name:      "generic 4xx after failing for 25 hours drops it",
			script:    []step{rejectWith(0)},
			stored:    []reminders.RemindMe{failingReminder(1, "a", 25*time.Hour)},
			wantLeft:  nil,
			wantSends: 1,
		},
		{
			name:      "generic 4xx after failing for 23 hours keeps it",
			script:    []step{rejectWith(0)},
			stored:    []reminders.RemindMe{failingReminder(1, "a", 23*time.Hour)},
			wantLeft:  []int{1},
			wantSends: 1,
		},
		{
			name:      "server error after failing for 25 hours keeps it (may be an outage)",
			script:    []step{serverError()},
			stored:    []reminders.RemindMe{failingReminder(1, "a", 25*time.Hour)},
			wantLeft:  []int{1},
			wantSends: 1,
		},
		{
			name:      "missing access keeps the reminder",
			script:    []step{rejectWith(discord.MissingAccessCode)},
			stored:    []reminders.RemindMe{dueReminder(1, "a")},
			wantLeft:  []int{1},
			wantSends: 1,
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
		{"cannot-DM-user on channel creation removes the user's due reminders", rejected, []int{3}},
		{"unknown user on channel creation removes the user's due reminders", restErr(http.StatusNotFound, discord.UnknownUserCode), []int{3}},
		{"401 on channel creation keeps them", restErr(http.StatusUnauthorized, 0), []int{1, 2, 3}},
		{"missing access on channel creation keeps them", restErr(http.StatusForbidden, discord.MissingAccessCode), []int{1, 2, 3}},
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

// The 24-hour give-up window counts from the first rejection, not the due time: the first rejection records
// failing_since, and later rejections leave it unchanged. Both the send and the OpenDM path behave this way.
func TestProcessReminderSlice_FirstRejectionRecordsFailingSince(t *testing.T) {
	earlier := remindNow.Add(-time.Hour)
	tests := []struct {
		name   string
		dmErr  error
		script []step
		stored reminders.RemindMe
		want   time.Time
	}{
		{"send path, first failure", nil, []step{rejectWith(0)}, overdueReminder(1, "a", 72*time.Hour), remindNow},
		{"send path, later failure keeps the first time", nil, []step{rejectWith(0)}, failingReminder(1, "a", time.Hour), earlier},
		{"OpenDM path, first failure", restErr(http.StatusForbidden, 40003), nil, overdueReminder(1, "a", 72*time.Hour), remindNow},
		{"OpenDM path, later failure keeps the first time", restErr(http.StatusForbidden, 40003), nil, failingReminder(1, "a", time.Hour), earlier},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			captureLogs(t)
			d := newFakeDiscord(tc.script...)
			d.DMError = tc.dmErr
			st := stateWith("u1", tc.stored)
			if err := runSlice(t, context.Background(), d, st, "u1"); err != nil {
				t.Fatal(err)
			}
			if got := st.reminderIDs("u1"); !reflect.DeepEqual(got, []int{1}) {
				t.Fatalf("remaining = %v, want [1]", got)
			}
			if got := st.failingSince("u1", 1); !got.Equal(tc.want) {
				t.Errorf("failing_since = %v, want %v", got, tc.want)
			}
		})
	}
}

// A non-user rejection of the DM channel creation keeps reminders (it may be an outage) but gives up on the ones
// that have been failing for more than 24 hours, with one warning.
func TestProcessReminderSlice_OpenDMRejectionGivesUpAfter24Hours(t *testing.T) {
	logs := captureLogs(t)
	d := newFakeDiscord()
	d.DMError = restErr(http.StatusUnauthorized, 0)
	st := stateWith("u1", failingReminder(1, "old", 25*time.Hour), failingReminder(2, "recent", 23*time.Hour), overdueReminder(4, "first", 72*time.Hour), futureReminder(3, "later"))
	if err := runSlice(t, context.Background(), d, st, "u1"); err != nil {
		t.Fatal(err)
	}
	if got := st.reminderIDs("u1"); !reflect.DeepEqual(got, []int{2, 4, 3}) {
		t.Errorf("remaining = %v, want [2 4 3]", got)
	}
	if got := logs.countContaining("WARN", "giving up"); got != 1 {
		t.Errorf("give-up warnings = %d, want 1", got)
	}
	// The dropped reminder is not retried (nor logged) again.
	if err := runSlice(t, context.Background(), d, st, "u1"); err != nil {
		t.Fatal(err)
	}
	if got := logs.countContaining("WARN", "giving up"); got != 1 {
		t.Errorf("give-up warnings after rerun = %d, want 1", got)
	}
}

// A failed send still waits the per-message throttle before the next reminder of the user.
func TestProcessReminderSlice_ThrottlesBetweenFailingSends(t *testing.T) {
	captureLogs(t)
	old := reminderThrottlePerMessage
	reminderThrottlePerMessage = 20 * time.Millisecond
	t.Cleanup(func() { reminderThrottlePerMessage = old })
	var mu sync.Mutex
	var sentAt []time.Time
	d := &hookDiscord{fakeDiscord: newFakeDiscord(rejectWith(0), rejectWith(0), rejectWith(0))}
	d.afterSend = func() {
		mu.Lock()
		sentAt = append(sentAt, time.Now())
		mu.Unlock()
	}
	st := stateWith("u1", dueReminder(1, "a"), dueReminder(2, "b"), dueReminder(3, "c"))
	if err := runSlice(t, context.Background(), d, st, "u1"); err != nil {
		t.Fatal(err)
	}
	if len(sentAt) != 3 {
		t.Fatalf("sends = %d, want 3", len(sentAt))
	}
	for i := 1; i < len(sentAt); i++ {
		if gap := sentAt[i].Sub(sentAt[i-1]); gap < reminderThrottlePerMessage {
			t.Errorf("gap before send %d = %v, want at least %v", i+1, gap, reminderThrottlePerMessage)
		}
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

func TestProcessReminderSlice_ReusedIDIsNotRemoved(t *testing.T) {
	captureLogs(t)
	st := stateWith("u1", dueReminder(2, "old"))
	d := &hookDiscord{fakeDiscord: newFakeDiscord()}
	d.afterSend = func() {
		// The user removed the old reminder 2 and created a new one that re-used the ID.
		st.mu.Lock()
		st.Reminders["u1"].Reminders = []reminders.RemindMe{futureReminder(2, "new")}
		st.mu.Unlock()
	}
	if err := runSlice(t, context.Background(), d, st, "u1"); err != nil {
		t.Fatal(err)
	}
	doc := st.snapshotReminders("u1")
	if doc == nil || len(doc.Reminders) != 1 || doc.Reminders[0].Message != "new" {
		t.Errorf("stored = %+v, want the new reminder with the re-used ID kept", doc)
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
