package bot

import (
	"context"
	"errors"
	"testing"
	"time"

	"ReZeroTsu/internal/commands/schedule"

	"github.com/bwmarrin/discordgo"
)

func TestDailyScheduleDelivery(t *testing.T) {
	tests := []struct {
		name          string
		script        []step
		recentErr     error
		wantDelivered int
		wantSendCalls int
		wantPosted    bool
		wantApCleared bool
	}{
		{name: "plain success", script: nil, wantDelivered: 1, wantSendCalls: 1, wantPosted: true},
		{name: "timeout after delivery posts once", script: []step{timeoutAfterDelivery()}, wantDelivered: 1, wantSendCalls: 1, wantPosted: true},
		{name: "server error after delivery posts once", script: []step{serverErrorAfterDelivery()}, wantDelivered: 1, wantSendCalls: 1, wantPosted: true},
		{name: "verifiably missing is retried once", script: []step{timeoutWithoutDelivery()}, wantDelivered: 1, wantSendCalls: 2, wantPosted: true},
		{name: "missing twice gives up after one retry", script: []step{timeoutWithoutDelivery(), timeoutWithoutDelivery()}, wantDelivered: 0, wantSendCalls: 2, wantPosted: false},
		{name: "verification failure counts as not delivered and retries", script: []step{timeoutAfterDelivery()}, recentErr: errors.New("boom"), wantDelivered: 2, wantSendCalls: 2, wantPosted: true},
		{name: "verification failure on both attempts leaves it unposted", script: []step{timeoutAfterDelivery(), timeoutAfterDelivery()}, recentErr: errors.New("boom"), wantDelivered: 2, wantSendCalls: 2, wantPosted: false},
		{name: "missing permissions is not retried", script: []step{rejectWith(discordgo.ErrCodeMissingPermissions)}, wantDelivered: 0, wantSendCalls: 1, wantPosted: false},
		{name: "unknown channel clears autopost, no retry", script: []step{rejectWith(discordgo.ErrCodeUnknownChannel)}, wantDelivered: 0, wantSendCalls: 1, wantPosted: false, wantApCleared: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newScheduleHarness(tc.script...)
			h.fd.RecentErr = tc.recentErr
			if err := h.run(context.Background()); err != nil {
				t.Fatalf("run: %v", err)
			}
			if got := len(h.fd.deliveredTo("sch")); got != tc.wantDelivered {
				t.Errorf("delivered = %d, want %d", got, tc.wantDelivered)
			}
			if h.fd.SendCalls != tc.wantSendCalls {
				t.Errorf("send calls = %d, want %d", h.fd.SendCalls, tc.wantSendCalls)
			}
			wantDate := ""
			if tc.wantPosted {
				wantDate = "2024-03-05"
			}
			if got := h.lastPosted(); got != wantDate {
				t.Errorf("last_posted_date = %q, want %q", got, wantDate)
			}
			ap, _ := h.st.GetAutopost(context.Background(), "g1", postTypeDailySchedule)
			if tc.wantApCleared != (ap == nil) {
				t.Errorf("autopost cleared = %v, want %v", ap == nil, tc.wantApCleared)
			}
		})
	}
}

// The daily schedule run retries a missing post once and never retries a rejected or cancelled one.
func TestDailyScheduleRetriesOnlyMissingPosts(t *testing.T) {
	t.Run("missing is retried once", func(t *testing.T) {
		h := newScheduleHarness(timeoutWithoutDelivery(), timeoutWithoutDelivery(), timeoutWithoutDelivery())
		_ = h.run(context.Background())
		if h.fd.SendCalls != 2 {
			t.Errorf("send calls = %d, want 2", h.fd.SendCalls)
		}
	})
	t.Run("rejected is never retried", func(t *testing.T) {
		for _, code := range []int{discordgo.ErrCodeMissingPermissions, discordgo.ErrCodeUnknownChannel, discordgo.ErrCodeInvalidFormBody} {
			h := newScheduleHarness(rejectWith(code))
			if err := h.run(context.Background()); err != nil {
				t.Fatalf("code %d: run: %v", code, err)
			}
			if h.fd.SendCalls != 1 {
				t.Errorf("code %d: send calls = %d, want 1", code, h.fd.SendCalls)
			}
		}
	})
	t.Run("cancelled is never retried", func(t *testing.T) {
		// The limiter stops while the context is alive, so the post is cancelled without shutdown. A wrongly retried
		// post would wait for a second token, which the shutdown case below cannot show (a retry there stops at the
		// cancelled context before any wait).
		h := newScheduleHarness(succeed())
		lim := &recordingLimiter{err: errors.New("send limiter stopped")}
		senders, _, _ := newTestSendersWithLimiter(h.d, lim)
		err := processDailyScheduleForGuild(context.Background(), h.d, senders, h.st, "g1", int(h.now.Weekday()), h.fn, h.now)
		if err != nil {
			t.Errorf("run = %v, want nil (context alive)", err)
		}
		if got := lim.Priorities(); len(got) != 1 {
			t.Errorf("waits = %v, want exactly one", got)
		}
		if h.fd.SendCalls != 0 {
			t.Errorf("send calls = %d, want 0", h.fd.SendCalls)
		}
	})
	t.Run("cancelled by shutdown is never retried", func(t *testing.T) {
		h := newScheduleHarness(timeoutWithoutDelivery(), succeed())
		ctx, cancel := context.WithCancel(context.Background())
		h.d = cancelDuringSend{fakeDiscord: h.fd, cancel: cancel}
		if err := h.run(ctx); !errors.Is(err, context.Canceled) {
			t.Errorf("run = %v, want context.Canceled", err)
		}
		if h.fd.SendCalls != 1 {
			t.Errorf("send calls = %d, want 1", h.fd.SendCalls)
		}
	})
}

func TestDailyScheduleRerunSameDayPostsNothing(t *testing.T) {
	h := newScheduleHarness()
	_ = h.run(context.Background())
	_ = h.run(context.Background()) // restart at 00:00:30
	if got := len(h.fd.deliveredTo("sch")); got != 1 {
		t.Fatalf("delivered = %d, want 1", got)
	}
}

func TestDailyScheduleRerunAfterTimeoutDelivered(t *testing.T) {
	h := newScheduleHarness(timeoutAfterDelivery())
	_ = h.run(context.Background())
	_ = h.run(context.Background())
	if got := len(h.fd.deliveredTo("sch")); got != 1 {
		t.Fatalf("delivered = %d, want 1", got)
	}
}

func TestDailySchedulePostsOnNewDay(t *testing.T) {
	h := newScheduleHarness()
	h.st.Autoposts[apKey("g1", postTypeDailySchedule)].LastPostedDate = "2024-03-04"
	_ = h.run(context.Background())
	if got := len(h.fd.deliveredTo("sch")); got != 1 {
		t.Fatalf("delivered = %d, want 1", got)
	}
}

func TestDailyScheduleNoSendAfterCancel(t *testing.T) {
	h := newScheduleHarness()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = h.run(ctx)
	if h.fd.SendCalls != 0 {
		t.Fatalf("send calls = %d, want 0", h.fd.SendCalls)
	}
}

func TestDailyScheduleShutdownDuringSendDoesNotRetry(t *testing.T) {
	h := newScheduleHarness(blockUntilCancelled())
	ctx, cancel := context.WithCancel(context.Background())
	h.fd.OnBlock = cancel
	_ = h.run(ctx)
	if h.fd.SendCalls != 1 {
		t.Fatalf("send calls = %d, want 1", h.fd.SendCalls)
	}
	if h.lastPosted() != "" {
		t.Fatal("undelivered post must not be recorded")
	}
}

func scheduleEmbedFor(h *scheduleHarness) *discordgo.MessageEmbed {
	return schedule.BuildScheduleEmbed(int(h.now.Weekday()), h.fn(true))
}

func TestDailyScheduleWebhookGoneFallsBackOnce(t *testing.T) {
	h := newScheduleHarness(rejectWith(discordgo.ErrCodeUnknownWebhook))
	h.fd.Webhook = &WebhookTarget{ID: "wh", Token: "tok"}
	_ = h.run(context.Background())
	got := h.fd.deliveredTo("sch")
	if len(got) != 1 || got[0].Webhook != nil {
		t.Fatalf("want one bot-account delivery, got %+v", got)
	}
	if h.lastPosted() == "" {
		t.Fatal("not recorded")
	}
}

// The retry of a verifiably missing post does not go back to a webhook that was already rejected in this run.
func TestDailyScheduleRetrySkipsRejectedWebhook(t *testing.T) {
	h := newScheduleHarness(rejectWith(discordgo.ErrCodeMissingAccess), timeoutWithoutDelivery(), succeed())
	h.fd.Webhook = &WebhookTarget{ID: "wh", Token: "tok"}
	_ = h.run(context.Background())
	if h.fd.WebhookSends != 1 {
		t.Errorf("webhook sends = %d, want 1", h.fd.WebhookSends)
	}
	if got := h.fd.deliveredTo("sch"); len(got) != 1 || got[0].Webhook != nil {
		t.Fatalf("want one bot-account delivery, got %+v", got)
	}
}

func TestDailyScheduleStaleSameTitleMessageDoesNotCountAsPosted(t *testing.T) {
	h := newScheduleHarness(timeoutWithoutDelivery())
	// Same embed (title, description) posted yesterday by the bot: must not satisfy verification.
	emb := scheduleEmbedFor(h)
	sd := &stampedDiscord{
		fakeDiscord: h.fd,
		stamp:       h.now,
		older: []ChannelMessage{{
			ID: "old", AuthorID: fakeBotUserID, Embeds: []*discordgo.MessageEmbed{emb},
			Timestamp: h.now.Add(-24 * time.Hour),
		}},
	}
	h.d = sd
	_ = h.run(context.Background())
	if got := len(h.fd.deliveredTo("sch")); got != 1 {
		t.Fatalf("delivered = %d, want 1 (retry after stale match)", got)
	}
	if h.fd.SendCalls != 2 {
		t.Fatalf("send calls = %d, want 2", h.fd.SendCalls)
	}
}

func TestScheduleMatcher(t *testing.T) {
	now := time.Date(2024, 3, 5, 10, 0, 0, 0, time.UTC)
	emb := &discordgo.MessageEmbed{Title: "T", Description: "today's shows"}
	msg := OutgoingMessage{Embeds: []*discordgo.MessageEmbed{emb}}
	wh := &WebhookTarget{ID: "wh", Token: "t"}
	mk := func(author string, ts time.Time, e *discordgo.MessageEmbed) ChannelMessage {
		return ChannelMessage{ID: "m", AuthorID: author, Timestamp: ts, Embeds: []*discordgo.MessageEmbed{e}}
	}
	tests := []struct {
		name    string
		msg     ChannelMessage
		webhook *WebhookTarget
		want    bool
	}{
		{"bot post today", mk(fakeBotUserID, now.Add(-time.Minute), emb), nil, true},
		{"webhook post today", mk("wh", now.Add(-time.Minute), emb), wh, true},
		{"zero timestamp accepted", mk(fakeBotUserID, time.Time{}, emb), nil, true},
		{"same post yesterday", mk(fakeBotUserID, now.Add(-24*time.Hour), emb), nil, false},
		{"other author", mk("someone", now, emb), nil, false},
		{"webhook author without a webhook send", mk("wh", now, emb), nil, false},
		{"other title", mk(fakeBotUserID, now, &discordgo.MessageEmbed{Title: "X", Description: "today's shows"}), nil, false},
		{"same title, other description", mk(fakeBotUserID, now, &discordgo.MessageEmbed{Title: "T", Description: "other"}), nil, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, got := scheduleMatcher("T", now)([]ChannelMessage{tc.msg}, fakeBotUserID, tc.webhook, msg)
			if got != tc.want {
				t.Errorf("match = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestScheduleMessagesToday(t *testing.T) {
	now := time.Date(2024, 3, 5, 10, 0, 0, 0, time.UTC)
	mk := func(title string, ts time.Time) ChannelMessage {
		return ChannelMessage{AuthorID: "b", Timestamp: ts, Embeds: []*discordgo.MessageEmbed{{Title: title}}}
	}
	tests := []struct {
		name string
		msg  ChannelMessage
		want bool
	}{
		{"same title today", mk("T", now.Add(-time.Hour)), true},
		{"same title just after midnight UTC", mk("T", time.Date(2024, 3, 5, 0, 0, 1, 0, time.UTC)), true},
		{"same title yesterday", mk("T", time.Date(2024, 3, 4, 23, 59, 59, 0, time.UTC)), false},
		{"other title today", mk("X", now), false},
		{"zero timestamp falls back to title", mk("T", time.Time{}), true},
		{"no embeds", ChannelMessage{Timestamp: now}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := len(scheduleMessagesToday([]ChannelMessage{tc.msg}, "T", now)) == 1
			if got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}
