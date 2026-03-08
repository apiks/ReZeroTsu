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
	now := time.Now()
	slices, err := db.Reminders().ListDue(ctx, now)
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
				if err := processReminderSlice(ctx, db, s, slice, now); err != nil && !errors.Is(err, context.Canceled) {
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

func processReminderSlice(ctx context.Context, db *database.Client, s *discordgo.Session, slice *reminders.RemindMeSlice, now time.Time) error {
	if slice == nil {
		return nil
	}
	due, remaining := splitDueReminders(slice.Reminders, now)
	if len(due) == 0 {
		return nil
	}

	userID := slice.ID
	dm, err := s.UserChannelCreate(userID)
	if err != nil {
		logger.For("reminders").Error("UserChannelCreate failed", "user_id", userID, "err", err)
		throttle := time.NewTimer(reminderThrottlePerUser)
		defer throttle.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-throttle.C:
		}
		return nil
	}
	var dueNotSent []reminders.RemindMe
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
		_, err = s.ChannelMessageSendComplex(dm.ID, &discordgo.MessageSend{Embeds: []*discordgo.MessageEmbed{emb}})
		if err != nil {
			args := append(discord.RESTAttrs(err), "user_id", userID, "err", err)
			if discord.IsCannotDMUser(err) {
				logger.For("reminders").Warn("ChannelMessageSendComplex failed", args...)
			} else {
				logger.For("reminders").Error("ChannelMessageSendComplex failed", args...)
			}
			dueNotSent = append(dueNotSent, r)
			continue
		}
		logger.For("reminders").Debug("reminder sent", "user_id", userID)
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

	// Save only future reminders plus due ones we failed to send (so they are retried next run).
	toSave := append(remaining, dueNotSent...)
	var remainingSlice *reminders.RemindMeSlice
	if len(toSave) > 0 {
		remainingSlice = &reminders.RemindMeSlice{
			ID:        slice.ID,
			IsGuild:   slice.IsGuild,
			Reminders: toSave,
			Premium:   slice.Premium,
		}
	}
	if err := db.Reminders().Save(ctx, slice.ID, remainingSlice); err != nil {
		logger.For("reminders").Error("Save failed", "user_id", slice.ID, "err", err)
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
