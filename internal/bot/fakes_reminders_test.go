package bot

import (
	"context"
	"time"

	"ReZeroTsu/internal/database/reminders"
)

// Fake SentState methods for the reminders delivery run (ticket #11).

func (f *fakeSentState) ListDueReminders(ctx context.Context, asOf time.Time) ([]*reminders.RemindMeSlice, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var out []*reminders.RemindMeSlice
	for _, doc := range f.Reminders {
		for _, r := range doc.Reminders {
			if !r.Date.After(asOf) {
				c := *doc
				c.Reminders = append([]reminders.RemindMe(nil), doc.Reminders...)
				out = append(out, &c)
				break
			}
		}
	}
	return out, nil
}

// RemoveReminders removes the named reminders from the user's document and deletes the document when it empties.
// Like the real store it fails on a cancelled context, which proves callers detach their "sent" writes.
func (f *fakeSentState) RemoveReminders(ctx context.Context, id string, remindIDs []int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	doc, ok := f.Reminders[id]
	if !ok {
		return nil
	}
	drop := map[int]bool{}
	for _, rid := range remindIDs {
		drop[rid] = true
	}
	var kept []reminders.RemindMe
	for _, r := range doc.Reminders {
		if !drop[r.RemindID] {
			kept = append(kept, r)
		}
	}
	if len(kept) == 0 {
		delete(f.Reminders, id)
		return nil
	}
	doc.Reminders = kept
	return nil
}

// appendReminderDirect simulates a concurrent /remind appending to the stored document.
func (f *fakeSentState) appendReminderDirect(id string, r reminders.RemindMe) {
	f.mu.Lock()
	defer f.mu.Unlock()
	doc, ok := f.Reminders[id]
	if !ok {
		f.Reminders[id] = &reminders.RemindMeSlice{ID: id, Reminders: []reminders.RemindMe{r}}
		return
	}
	doc.Reminders = append(doc.Reminders, r)
}

// snapshotReminders returns a copy of the stored document, as ListDue would hand to a delivery run.
func (f *fakeSentState) snapshotReminders(id string) *reminders.RemindMeSlice {
	f.mu.Lock()
	defer f.mu.Unlock()
	doc, ok := f.Reminders[id]
	if !ok {
		return nil
	}
	c := *doc
	c.Reminders = append([]reminders.RemindMe(nil), doc.Reminders...)
	return &c
}

func (f *fakeSentState) reminderIDs(id string) []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	doc, ok := f.Reminders[id]
	if !ok {
		return nil
	}
	var out []int
	for _, r := range doc.Reminders {
		out = append(out, r.RemindID)
	}
	return out
}

// hookDiscord runs afterSend right after every Send on the wrapped fake.
type hookDiscord struct {
	*fakeDiscord
	afterSend func()
}

func (h *hookDiscord) Send(ctx context.Context, channelID string, webhook *WebhookTarget, msg OutgoingMessage) (string, error) {
	id, err := h.fakeDiscord.Send(ctx, channelID, webhook, msg)
	if h.afterSend != nil {
		h.afterSend()
	}
	return id, err
}
