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
func (f *fakeSentState) RemoveReminders(ctx context.Context, id string, refs []reminders.RemindRef) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	doc, ok := f.Reminders[id]
	if !ok {
		return nil
	}
	var kept []reminders.RemindMe
	for _, r := range doc.Reminders {
		dropIt := false
		for _, ref := range refs {
			if ref.RemindID == r.RemindID && ref.Date.Equal(r.Date) {
				dropIt = true
				break
			}
		}
		if !dropIt {
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

// MarkRemindersFailing sets FailingSince on the matching reminders that do not have one yet. A zero FailingSince
// (a missing or null field in the store) counts as unset.
func (f *fakeSentState) MarkRemindersFailing(ctx context.Context, id string, refs []reminders.RemindRef, since time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.MarkFailingCalls++
	doc, ok := f.Reminders[id]
	if !ok {
		return nil
	}
	for i := range doc.Reminders {
		r := &doc.Reminders[i]
		for _, ref := range refs {
			if ref.RemindID == r.RemindID && ref.Date.Equal(r.Date) && r.FailingSince.IsZero() {
				r.FailingSince = since
			}
		}
	}
	return nil
}

// failingSince returns the stored FailingSince of the user's reminder with remindID.
func (f *fakeSentState) failingSince(id string, remindID int) time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	if doc, ok := f.Reminders[id]; ok {
		for _, r := range doc.Reminders {
			if r.RemindID == remindID {
				return r.FailingSince
			}
		}
	}
	return time.Time{}
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
