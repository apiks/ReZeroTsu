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
	reminderThrottlePerUser    = 200 * time.Millisecond
	reminderThrottlePerMessage = 150 * time.Millisecond
	reminderRunInterval        = 15 * time.Second
)

// RunReminderLoop fetches due reminders, sends DMs, updates DB. Exits when ctx is cancelled.
func RunReminderLoop(ctx context.Context, db *database.Client, session *discordgo.Session, interval time.Duration) {
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
			runReminderCheck(ctx, db, session)
		}
	}
}

func runReminderCheck(ctx context.Context, db *database.Client, s *discordgo.Session) {
	deliverDueReminders(ctx, newDiscordgoPort(s), newMongoSentState(db), time.Now())
}

// deliverDueReminders lists the documents with reminders due at now and delivers them with a worker pool.
func deliverDueReminders(ctx context.Context, d DiscordPort, st SentState, now time.Time) {
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
				if err := processReminderSlice(ctx, d, st, slice, now); err != nil && !errors.Is(err, context.Canceled) {
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

func processReminderSlice(ctx context.Context, d DiscordPort, st SentState, slice *reminders.RemindMeSlice, now time.Time) error {
	if slice == nil {
		return nil
	}
	due, _ := splitDueReminders(slice.Reminders, now)
	if len(due) == 0 {
		return nil
	}

	userID := slice.ID
	// remove deletes delivered or undeliverable reminders by ID, on a context detached from shutdown.
	remove := func(ids ...int) {
		if err := detachedWrite(ctx, func(wctx context.Context) error {
			return st.RemoveReminders(wctx, userID, ids)
		}); err != nil {
			logger.For("reminders").Error("RemoveReminders failed", "user_id", userID, "remind_ids", ids, "err", err)
		}
	}
	dmID, err := d.OpenDM(ctx, userID)
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if discord.ClassifySend(err) == discord.Rejected {
			// Discord refused to open a DM channel: none of this user's due reminders can ever be delivered.
			ids := make([]int, 0, len(due))
			for _, r := range due {
				ids = append(ids, r.RemindID)
			}
			logger.For("reminders").Warn("UserChannelCreate rejected, dropping due reminders",
				append(discord.RESTAttrs(err), "user_id", userID, "dropped", len(ids), "err", err)...)
			remove(ids...)
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
		res := sendWithRules(ctx, d, "", dmID, nil, OutgoingMessage{Embeds: []*discordgo.MessageEmbed{emb}})
		switch {
		case res.Delivered:
			remove(r.RemindID)
			logger.For("reminders").Debug("reminder sent", "user_id", userID, "remind_id", r.RemindID, "presumed", res.Presumed)
		case ctx.Err() != nil:
			return ctx.Err()
		case discord.ClassifySend(res.Err) == discord.Rejected:
			// Rejected (closed DMs, ...): drop after this one attempt instead of retrying every run.
			logger.For("reminders").Warn("reminder rejected, dropping",
				append(discord.RESTAttrs(res.Err), "user_id", userID, "remind_id", r.RemindID, "err", res.Err)...)
			remove(r.RemindID)
		default:
			// Unknown outcome verified as not delivered: keep it for the next run.
			logger.For("reminders").Warn("reminder send failed, will retry", "user_id", userID, "remind_id", r.RemindID, "err", res.Err)
			continue
		}
		// Throttle per channel so we don't hit Discord's per-channel send limit
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
