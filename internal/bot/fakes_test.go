package bot

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"ReZeroTsu/internal/database/anime_subs"
	"ReZeroTsu/internal/database/feed_checks"
	"ReZeroTsu/internal/database/guilds"
	"ReZeroTsu/internal/database/reminders"

	"github.com/bwmarrin/discordgo"
)

// stepKind scripts what one fake Send call does.
type stepKind int

const (
	stepSucceed              stepKind = iota // deliver and return nil
	stepReject                               // do not deliver, return a REST 4xx with Code
	stepTimeoutDelivered                     // deliver, then return a deadline error
	stepTimeoutUndelivered                   // do not deliver, return a deadline error
	stepBlock                                // do not deliver, block until ctx is cancelled
	stepServerError                          // do not deliver, return a REST 500
	stepServerErrorDelivered                 // deliver, then return a REST 500
	stepInFlightAtShutdown                   // block until ctx is cancelled, deliver, then return a client deadline error
)

type step struct {
	kind stepKind
	code int // REST error code for stepReject
}

func succeed() step                   { return step{kind: stepSucceed} }
func rejectWith(code int) step        { return step{kind: stepReject, code: code} }
func timeoutAfterDelivery() step      { return step{kind: stepTimeoutDelivered} }
func timeoutWithoutDelivery() step    { return step{kind: stepTimeoutUndelivered} }
func blockUntilCancelled() step       { return step{kind: stepBlock} }
func serverError() step               { return step{kind: stepServerError} }
func serverErrorAfterDelivery() step  { return step{kind: stepServerErrorDelivered} }
func inFlightTimeoutAtShutdown() step { return step{kind: stepInFlightAtShutdown} }

// recordingLimiter is a fake send limiter that records the priority of every wait. Waits succeed unless err is set;
// onAcquire, when set, runs first and its error (if any) ends the wait, so tests can end a wait the way shutdown does.
type recordingLimiter struct {
	mu         sync.Mutex
	priorities []int
	err        error
	onAcquire  func() error
}

func (l *recordingLimiter) Acquire(_ context.Context, priority int) error {
	l.mu.Lock()
	l.priorities = append(l.priorities, priority)
	hook, err := l.onAcquire, l.err
	l.mu.Unlock()
	if hook != nil {
		if herr := hook(); herr != nil {
			return herr
		}
	}
	return err
}

// Priorities returns the priority of each wait so far, in order.
func (l *recordingLimiter) Priorities() []int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]int(nil), l.priorities...)
}

// newTestSendersWithClock builds the real channel sender factory over the fake raw port, with a fresh shared
// webhook cache, a fake clock and a recording send limiter whose waits succeed. Runs that must share webhook state
// across calls reuse the returned factory.
func newTestSendersWithClock(raw RawDiscord) (*channelSenderFactory, *fakeClock) {
	f, clk, _ := newTestSendersWithLimiter(raw, &recordingLimiter{})
	return f, clk
}

// newTestSendersWithLimiter is newTestSendersWithClock over the given recording limiter, for tests that assert
// what the channel sender waited for.
func newTestSendersWithLimiter(raw RawDiscord, lim *recordingLimiter) (*channelSenderFactory, *fakeClock, *recordingLimiter) {
	clk := newFakeClock()
	return newChannelSenderFactory(raw, newWebhookCache(clk), lim), clk, lim
}

// newTestSenders is newTestSendersWithClock for tests that never move the clock.
func newTestSenders(raw RawDiscord) *channelSenderFactory {
	f, _ := newTestSendersWithClock(raw)
	return f
}

// delivered is one message that "reached Discord".
type delivered struct {
	ChannelID string
	Webhook   *WebhookTarget
	Msg       OutgoingMessage
}

// fakeDiscordPort is both interfaces a Discord adapter satisfies.
type fakeDiscordPort interface {
	RunOperations
	RawDiscord
}

// fakeDiscord is an in-memory Discord adapter (run operations and raw Discord). Send calls consume script steps in order; once the script is
// exhausted every Send succeeds.
type fakeDiscord struct {
	mu        sync.Mutex
	script    []step
	Delivered []delivered
	SendCalls int
	// WebhookSends counts Send calls made through a webhook, delivered or not.
	WebhookSends int
	// RecentCalls counts RecentMessages calls (verification reads).
	RecentCalls int

	// Configuration.
	Webhook     *WebhookTarget  // returned by ResolveWebhook; nil means no webhook available
	Roles       map[string]bool // roleID -> exists
	NoSendPerms map[string]bool // channelID -> CanSend false
	// WebhookUnusable marks channels whose state says the bot lacks the permissions a webhook needs.
	WebhookUnusable map[string]bool
	DMError         error // returned by OpenDM when set
	RecentErr       error // returned by RecentMessages when set
	// OnBlock, when set, runs as a blocking step starts waiting. Tests cancel the run from it, so shutdown tests
	// need no timers.
	OnBlock func()

	// RolePresence overrides Roles for the listed role IDs (e.g. roleUnknown).
	RolePresence map[string]rolePresence

	Pinned []string // "channel:message"

	// ResolveCalls counts ResolveWebhook calls: the only record of webhook resolution.
	ResolveCalls int
}

func newFakeDiscord(script ...step) *fakeDiscord {
	return &fakeDiscord{script: script, Roles: map[string]bool{}, NoSendPerms: map[string]bool{}, WebhookUnusable: map[string]bool{}}
}

func (f *fakeDiscord) Send(ctx context.Context, channelID string, webhook *WebhookTarget, msg OutgoingMessage) (string, error) {
	f.mu.Lock()
	f.SendCalls++
	if webhook != nil {
		f.WebhookSends++
	}
	st := succeed()
	if len(f.script) > 0 {
		st = f.script[0]
		f.script = f.script[1:]
	}
	deliver := func() string {
		f.Delivered = append(f.Delivered, delivered{ChannelID: channelID, Webhook: webhook, Msg: msg})
		return fmt.Sprintf("msg-%d", len(f.Delivered))
	}
	switch st.kind {
	case stepSucceed:
		id := deliver()
		f.mu.Unlock()
		return id, nil
	case stepTimeoutDelivered:
		deliver()
		f.mu.Unlock()
		return "", context.DeadlineExceeded
	case stepTimeoutUndelivered:
		f.mu.Unlock()
		return "", context.DeadlineExceeded
	case stepServerError, stepServerErrorDelivered:
		if st.kind == stepServerErrorDelivered {
			deliver()
		}
		f.mu.Unlock()
		return "", &discordgo.RESTError{
			Response: &http.Response{StatusCode: http.StatusInternalServerError},
			Message:  &discordgo.APIErrorMessage{Message: "server error"},
		}
	case stepReject:
		f.mu.Unlock()
		return "", &discordgo.RESTError{
			Response: &http.Response{StatusCode: rejectStatus(st.code)},
			Message:  &discordgo.APIErrorMessage{Code: st.code, Message: "rejected"},
		}
	case stepInFlightAtShutdown:
		// An in-flight REST call whose http.Client timeout fires while the run is shutting down: the message
		// still reached Discord.
		f.mu.Unlock()
		f.blocked()
		<-ctx.Done()
		f.mu.Lock()
		deliver()
		f.mu.Unlock()
		return "", context.DeadlineExceeded
	default: // stepBlock: waiting for a send limiter, the request is never made
		f.mu.Unlock()
		f.blocked()
		<-ctx.Done()
		return "", notAttempted(ctx.Err())
	}
}

// rejectStatus is the HTTP status Discord answers with for a rejected-send code. Only access and permission
// codes are 403s, so an unrecognised code is not mistaken for missing permissions (any 403).
func rejectStatus(code int) int {
	switch code {
	case discordgo.ErrCodeMissingAccess, discordgo.ErrCodeMissingPermissions, discordgo.ErrCodeCannotSendMessagesToThisUser:
		return http.StatusForbidden
	case discordgo.ErrCodeUnknownChannel, discordgo.ErrCodeUnknownMessage, discordgo.ErrCodeUnknownRole,
		discordgo.ErrCodeUnknownUser, discordgo.ErrCodeUnknownWebhook:
		return http.StatusNotFound
	case discordgo.ErrCodeInvalidWebhookTokenProvided:
		return http.StatusUnauthorized
	default:
		return http.StatusBadRequest
	}
}

func (f *fakeDiscord) OpenDM(_ context.Context, userID string) (string, error) {
	if f.DMError != nil {
		return "", f.DMError
	}
	return "dm-" + userID, nil
}

func (f *fakeDiscord) RecentMessages(_ context.Context, channelID string, limit int) ([]ChannelMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.RecentCalls++
	if f.RecentErr != nil {
		return nil, f.RecentErr
	}
	var out []ChannelMessage
	for i := len(f.Delivered) - 1; i >= 0 && len(out) < limit; i-- {
		d := f.Delivered[i]
		if d.ChannelID != channelID {
			continue
		}
		author := fakeBotUserID
		if d.Webhook != nil {
			author = d.Webhook.ID
		}
		out = append(out, ChannelMessage{ID: fmt.Sprintf("msg-%d", i+1), AuthorID: author, Content: d.Msg.Content, Embeds: d.Msg.Embeds})
	}
	return out, nil
}

// ResolveWebhook returns the configured webhook and counts the call.
func (f *fakeDiscord) ResolveWebhook(_, _ string) *WebhookTarget {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ResolveCalls++
	return f.Webhook
}

// WebhookUsable is true unless the channel is marked in WebhookUnusable.
func (f *fakeDiscord) WebhookUsable(channelID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return !f.WebhookUnusable[channelID]
}

func (f *fakeDiscord) CanSend(channelID string) bool { return !f.NoSendPerms[channelID] }

func (f *fakeDiscord) GuildRole(_, roleID string) rolePresence {
	if p, ok := f.RolePresence[roleID]; ok {
		return p
	}
	if f.Roles[roleID] {
		return rolePresent
	}
	return roleAbsent
}

func (f *fakeDiscord) PinReplacing(channelID, messageID, _ string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Pinned = append(f.Pinned, channelID+":"+messageID)
}

func (f *fakeDiscord) ShardID() int { return 0 }

const fakeBotUserID = "bot-user"

func (f *fakeDiscord) BotUserID() string { return fakeBotUserID }

// deliveredTo returns the messages delivered to channelID.
func (f *fakeDiscord) deliveredTo(channelID string) []delivered {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []delivered
	for _, d := range f.Delivered {
		if d.ChannelID == channelID {
			out = append(out, d)
		}
	}
	return out
}

// fakeSentState is an in-memory SentState.
type fakeSentState struct {
	mu          sync.Mutex
	AnimeSubs   map[string]*anime_subs.AnimeSubs
	Autoposts   map[string]*guilds.Autopost // key guildID+"/"+postType
	Settings    map[string]*guilds.GuildSettings
	FeedChecks  []feed_checks.FeedCheck
	Reminders   map[string]*reminders.RemindMeSlice
	FeedsPurged []string // "guild:channel"

	// FeedCheckWrites is the number of feed checks in each SaveFeedChecks call, in order.
	FeedCheckWrites []int

	// MarkFailingCalls counts MarkRemindersFailing calls.
	MarkFailingCalls int
}

func newFakeSentState() *fakeSentState {
	return &fakeSentState{
		AnimeSubs: map[string]*anime_subs.AnimeSubs{},
		Autoposts: map[string]*guilds.Autopost{},
		Settings:  map[string]*guilds.GuildSettings{},
		Reminders: map[string]*reminders.RemindMeSlice{},
	}
}

func apKey(guildID, postType string) string { return guildID + "/" + postType }

func copyShows(in []*anime_subs.ShowSub) []*anime_subs.ShowSub {
	out := make([]*anime_subs.ShowSub, 0, len(in))
	for _, s := range in {
		if s == nil {
			out = append(out, nil)
			continue
		}
		c := *s
		out = append(out, &c)
	}
	return out
}

func (f *fakeSentState) GetAnimeSubs(_ context.Context, id string) (*anime_subs.AnimeSubs, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.AnimeSubs[id]
	if !ok {
		return nil, nil
	}
	c := *d
	c.Shows = copyShows(d.Shows)
	return &c, nil
}

func (f *fakeSentState) SeedAnimeSubs(_ context.Context, id string, isGuild bool, shows []*anime_subs.ShowSub, seedDate string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.AnimeSubs[id] = &anime_subs.AnimeSubs{ID: id, IsGuild: isGuild, Shows: copyShows(shows), LastSeedDate: seedDate}
	return nil
}

func (f *fakeSentState) AppendAnimeSubShows(_ context.Context, id string, shows []*anime_subs.ShowSub) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.AnimeSubs[id]
	if !ok {
		return nil
	}
	d.Shows = append(d.Shows, copyShows(shows)...)
	return nil
}

func (f *fakeSentState) MarkShowsNotified(_ context.Context, id string, marks []*anime_subs.ShowSub) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.AnimeSubs[id]
	if !ok {
		return nil
	}
	for _, s := range d.Shows {
		if s == nil {
			continue
		}
		for _, m := range marks {
			if s.Show == m.Show && s.Track == m.Track && s.Episode == m.Episode {
				s.Notified = true
			}
		}
	}
	return nil
}

func (f *fakeSentState) DeleteAnimeSubs(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.AnimeSubs, id)
	return nil
}

func (f *fakeSentState) GetAutopost(_ context.Context, guildID, postType string) (*guilds.Autopost, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ap, ok := f.Autoposts[apKey(guildID, postType)]
	if !ok {
		return nil, nil
	}
	c := *ap
	return &c, nil
}

func (f *fakeSentState) SetAutopost(_ context.Context, guildID, postType string, ap *guilds.Autopost) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if ap == nil {
		delete(f.Autoposts, apKey(guildID, postType))
		return nil
	}
	// Like the Mongo repository, which rebuilds the entry from PostType, Name, ID and RoleID only.
	c := guilds.Autopost{PostType: postType, Name: ap.Name, ID: ap.ID, RoleID: ap.RoleID}
	f.Autoposts[apKey(guildID, postType)] = &c
	return nil
}

// GetGuildSettings returns the defaults for a guild without settings, like the Mongo repository.
func (f *fakeSentState) GetGuildSettings(_ context.Context, guildID string) (*guilds.GuildSettings, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s, ok := f.Settings[guildID]; ok && s != nil {
		return s, nil
	}
	return guilds.DefaultGuildSettings(), nil
}

func (f *fakeSentState) SaveFeedChecks(_ context.Context, guildID string, fcs []feed_checks.FeedCheck) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.FeedCheckWrites = append(f.FeedCheckWrites, len(fcs))
next:
	for _, fc := range fcs {
		fc.GuildID = guildID
		for i := range f.FeedChecks {
			if f.FeedChecks[i].GuildID == guildID && f.FeedChecks[i].GUID == fc.GUID {
				f.FeedChecks[i] = fc
				continue next
			}
		}
		f.FeedChecks = append(f.FeedChecks, fc)
	}
	return nil
}

func (f *fakeSentState) RemoveFeedsByChannel(_ context.Context, guildID, channelID string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.FeedsPurged = append(f.FeedsPurged, guildID+":"+channelID)
	return 1, nil
}

func (f *fakeSentState) DeleteFeedChecksByChannel(_ context.Context, guildID, channelID string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var kept []feed_checks.FeedCheck
	var n int64
	for _, fc := range f.FeedChecks {
		if fc.GuildID == guildID && fc.Feed.ChannelID == channelID {
			n++
			continue
		}
		kept = append(kept, fc)
	}
	f.FeedChecks = kept
	return n, nil
}

var (
	_ RunOperations = (*fakeDiscord)(nil)
	_ RawDiscord    = (*fakeDiscord)(nil)
	_ SentState     = (*fakeSentState)(nil)
)

// blocked runs OnBlock, if set, as a blocking step starts waiting. The caller must not hold f.mu.
func (f *fakeDiscord) blocked() {
	if f.OnBlock != nil {
		f.OnBlock()
	}
}
