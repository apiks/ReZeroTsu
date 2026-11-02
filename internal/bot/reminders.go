package bot

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"time"

	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/database/reminders"
	"ReZeroTsu/internal/discord"
	"ReZeroTsu/internal/logger"

	"github.com/bwmarrin/discordgo"
)

const (
	reminderRunInterval = 15 * time.Second
	// reminderGiveUpAfter bounds retries of a reminder that Discord keeps rejecting with a code other than the
	// user-undeliverable ones: once it has been failing this long since its first rejection it is dropped.
	reminderGiveUpAfter = 24 * time.Hour
)

// The pauses between the reminder DMs of one user and between users; variables so tests can set them.
var (
	reminderThrottlePerUser    = 200 * time.Millisecond
	reminderThrottlePerMessage = 150 * time.Millisecond
)

// reminderGivenUp reports whether a reminder rejected by Discord has been failing too long to keep retrying.
// A reminder that has not been marked as failing is never given up on: the mark is rewritten on every later rejection,
// so a failed write repairs itself on the next run, and an outage can not wipe overdue reminders.
func reminderGivenUp(r reminders.RemindMe, now time.Time) bool {
	if r.FailingSince.IsZero() {
		return false
	}
	return now.Sub(r.FailingSince) > reminderGiveUpAfter
}

// RunReminderLoop fetches due reminders, sends DMs, updates DB. Exits when ctx is cancelled.
func RunReminderLoop(ctx context.Context, db *database.Client, session *discordgo.Session, ops RunOperations, senders *channelSenderFactory, interval time.Duration) {
	if interval <= 0 {
		interval = reminderRunInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runReminderCheck(ctx, db, ops, senders)
		}
	}
}

func runReminderCheck(ctx context.Context, db *database.Client, d RunOperations, senders *channelSenderFactory) {
	deliverDueReminders(ctx, d, senders, newMongoSentState(db), time.Now())
}

// deliverDueReminders lists the documents with reminders due at now and delivers them with a worker pool.
func deliverDueReminders(ctx context.Context, d RunOperations, senders *channelSenderFactory, st SentState, now time.Time) {
	slices, err := st.ListDueReminders(ctx, now)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			logger.For("reminders").Error("ListDue failed", "err", err)
		}
		return
	}
	if len(slices) == 0 {
		return
	}

	sliceCh := make(chan *reminders.RemindMeSlice)
	workerCount := max(runtime.NumCPU()*2, 1)
	var wg sync.WaitGroup
	wg.Add(workerCount)
	for range workerCount {
		go func() {
			defer wg.Done()
			for slice := range sliceCh {
				if slice == nil {
					continue
				}
				if err := processReminderSlice(ctx, d, senders, st, slice, now); err != nil && !errors.Is(err, context.Canceled) {
					logger.For("reminders").Error("processReminderSlice failed", "user_id", slice.ID, "err", err)
				}
			}
		}()
	}

	for i := range slices {
		select {
		case <-ctx.Done():
			close(sliceCh)
			wg.Wait()
			return
		case sliceCh <- slices[i]:
		}
	}
	close(sliceCh)
	wg.Wait()
}

func processReminderSlice(ctx context.Context, d RunOperations, senders *channelSenderFactory, st SentState, slice *reminders.RemindMeSlice, now time.Time) error {
	if slice == nil {
		return nil
	}
	due, _ := splitDueReminders(slice.Reminders, now)
	if len(due) == 0 {
		return nil
	}

	userID := slice.ID
	// remove deletes delivered or undeliverable reminders by ID, on a context detached from shutdown.
	// Reminders are matched on ID and due time, so an ID re-used by a newer reminder is never removed.
	remove := func(refs ...reminders.RemindRef) {
		if err := detachedWrite(ctx, func(wctx context.Context) error {
			return st.RemoveReminders(wctx, userID, refs)
		}); err != nil {
			logger.For("reminders").Error("RemoveReminders failed", "user_id", userID, "reminders", refs, "err", err)
		}
	}
	// markFailing records now as the first-rejection time of the reminders that have none yet, on a detached context.
	markFailing := func(rs ...reminders.RemindMe) {
		var refs []reminders.RemindRef
		for _, r := range rs {
			if r.FailingSince.IsZero() {
				refs = append(refs, reminders.RemindRef{RemindID: r.RemindID, Date: r.Date})
			}
		}
		if len(refs) == 0 {
			return
		}
		if err := detachedWrite(ctx, func(wctx context.Context) error {
			return st.MarkRemindersFailing(wctx, userID, refs, now)
		}); err != nil {
			logger.For("reminders").Error("MarkRemindersFailing failed", "user_id", userID, "reminders", refs, "err", err)
		}
	}
	dmID, err := d.OpenDM(ctx, userID)
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if discord.IsUserUndeliverable(err) {
			// The user can never be DMed (closed DMs, deleted account): none of the due reminders can be delivered.
			refs := make([]reminders.RemindRef, 0, len(due))
			for _, r := range due {
				refs = append(refs, reminders.RemindRef{RemindID: r.RemindID, Date: r.Date})
			}
			logger.For("reminders").Warn("UserChannelCreate rejected, dropping due reminders",
				append(discord.RESTAttrs(err), "user_id", userID, "dropped", len(refs), "err", err)...)
			remove(refs...)
		} else if discord.ClassifySend(err) == discord.Rejected {
			// Any other rejection (401, missing access, Cloudflare ban, ...) may be an outage: keep and retry,
			// except the reminders that have kept failing for more than reminderGiveUpAfter.
			var expired []reminders.RemindRef
			var kept []reminders.RemindMe
			for _, r := range due {
				if reminderGivenUp(r, now) {
					expired = append(expired, reminders.RemindRef{RemindID: r.RemindID, Date: r.Date})
				} else {
					kept = append(kept, r)
				}
			}
			markFailing(kept...)
			if len(expired) > 0 {
				logger.For("reminders").Warn("UserChannelCreate still rejected long after the first failure, giving up on those reminders",
					append(discord.RESTAttrs(err), "user_id", userID, "dropped", len(expired), "err", err)...)
				remove(expired...)
			}
			if len(expired) < len(due) {
				logger.For("reminders").Warn("UserChannelCreate rejected, will retry",
					append(discord.RESTAttrs(err), "user_id", userID, "err", err)...)
			}
		} else {
			logger.For("reminders").Error("UserChannelCreate failed", "user_id", userID, "err", err)
		}
		throttle := time.NewTimer(reminderThrottlePerUser)
		defer throttle.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-throttle.C:
		}
		return nil
	}
	// The rejected reminders are marked in one write when the loop ends, also when it ends early.
	var rejected []reminders.RemindMe
	defer func() { markFailing(rejected...) }()
	throttleTimer := time.NewTimer(reminderThrottlePerMessage)
	defer throttleTimer.Stop()
	for i, r := range due {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		setVal := "—"
		if !r.CreatedAt.IsZero() {
			setVal = fmt.Sprintf("<t:%d:R>", r.CreatedAt.UTC().Unix())
		}
		dueVal := fmt.Sprintf("<t:%d:R>", r.Date.UTC().Unix())
		desc := fmt.Sprintf("**Set:** %s · **Due:** %s\n\n%s", setVal, dueVal, r.Message)
		if len(desc) > discord.MaxEmbedDescriptionLength {
			desc = desc[:discord.MaxEmbedDescriptionLength]
		}
		emb := &discordgo.MessageEmbed{
			Title:       "Reminder",
			Description: desc,
			Color:       discord.EmbedColor,
		}
		if globalSendLimiter != nil {
			if acqErr := globalSendLimiter.Acquire(ctx, PriorityReminders); acqErr != nil {
				return acqErr
			}
		}
		res := senders.open("", dmID, dmPolicy()).post(ctx, OutgoingMessage{Embeds: []*discordgo.MessageEmbed{emb}})
		switch {
		case res.Delivered:
			remove(reminders.RemindRef{RemindID: r.RemindID, Date: r.Date})
			logger.For("reminders").Debug("reminder sent", "user_id", userID, "remind_id", r.RemindID, "presumed", res.Presumed)
		case ctx.Err() != nil:
			return ctx.Err()
		case discord.IsUserUndeliverable(res.Err):
			// The user can never be DMed (closed DMs, deleted account): drop after this one attempt.
			logger.For("reminders").Warn("reminder rejected, dropping",
				append(discord.RESTAttrs(res.Err), "user_id", userID, "remind_id", r.RemindID, "err", res.Err)...)
			remove(reminders.RemindRef{RemindID: r.RemindID, Date: r.Date})
		case discord.ClassifySend(res.Err) == discord.Rejected && reminderGivenUp(r, now):
			// Rejected with another code for more than reminderGiveUpAfter: it is not going to work.
			logger.For("reminders").Warn("reminder still rejected long after the first failure, giving up",
				append(discord.RESTAttrs(res.Err), "user_id", userID, "remind_id", r.RemindID, "err", res.Err)...)
			remove(reminders.RemindRef{RemindID: r.RemindID, Date: r.Date})
		default:
			// Any other rejection, or an Unknown outcome verified as not delivered: keep it for the next run.
			// A rejection starts the give-up window, if this is the reminder's first one.
			if discord.ClassifySend(res.Err) == discord.Rejected {
				rejected = append(rejected, r)
			}
			logger.For("reminders").Warn("reminder send failed, will retry",
				append(discord.RESTAttrs(res.Err), "user_id", userID, "remind_id", r.RemindID, "err", res.Err)...)
		}
		// Throttle per channel so we don't hit Discord's per-channel send limit, after failed sends too.
		if i < len(due)-1 {
			if !throttleTimer.Stop() {
				select {
				case <-throttleTimer.C:
				default:
				}
			}
			throttleTimer.Reset(reminderThrottlePerMessage)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-throttleTimer.C:
			}
		}
	}

	throttle := time.NewTimer(reminderThrottlePerUser)
	defer throttle.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-throttle.C:
	}
	return nil
}

func splitDueReminders(list []reminders.RemindMe, asOf time.Time) (due, remaining []reminders.RemindMe) {
	for _, r := range list {
		if !r.Date.After(asOf) {
			due = append(due, r)
		} else {
			remaining = append(remaining, r)
		}
	}
	return due, remaining
}
