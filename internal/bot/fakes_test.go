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
	stepSucceed           stepKind = iota // deliver and return nil
	stepReject                            // do not deliver, return a REST 4xx with Code
	stepTimeoutDelivered                  // deliver, then return a deadline error
	stepTimeoutUndelivered                // do not deliver, return a deadline error
	stepBlock                             // do not deliver, block until ctx is cancelled
	stepServerError                       // do not deliver, return a REST 500
	stepServerErrorDelivered              // deliver, then return a REST 500
)

type step struct {
	kind stepKind
	code int // REST error code for stepReject
}

func succeed() step                 { return step{kind: stepSucceed} }
func rejectWith(code int) step      { return step{kind: stepReject, code: code} }
func timeoutAfterDelivery() step    { return step{kind: stepTimeoutDelivered} }
func timeoutWithoutDelivery() step  { return step{kind: stepTimeoutUndelivered} }
func blockUntilCancelled() step     { return step{kind: stepBlock} }
func serverError() step             { return step{kind: stepServerError} }
func serverErrorAfterDelivery() step { return step{kind: stepServerErrorDelivered} }

// delivered is one message that "reached Discord".
type delivered struct {
	ChannelID string
	Webhook   *WebhookTarget
	Msg       OutgoingMessage
}

// fakeDiscord is an in-memory DiscordPort. Send calls consume script steps in order; once the script is
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
	Webhook     *WebhookTarget  // returned by ChannelWebhook; nil means no webhook available
	Roles       map[string]bool // roleID -> exists
	NoSendPerms map[string]bool // channelID -> CanSend false
	DMError     error           // returned by OpenDM when set
	RecentErr   error           // returned by RecentMessages when set

	Invalidated []string // "guild:channel"
	Pinned      []string // "channel:message"
}

func newFakeDiscord(script ...step) *fakeDiscord {
	return &fakeDiscord{script: script, Roles: map[string]bool{}, NoSendPerms: map[string]bool{}}
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
			Response: &http.Response{StatusCode: http.StatusForbidden},
			Message:  &discordgo.APIErrorMessage{Code: st.code, Message: "rejected"},
		}
	default: // stepBlock
		f.mu.Unlock()
		<-ctx.Done()
		return "", ctx.Err()
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

func (f *fakeDiscord) ChannelWebhook(_, _ string) *WebhookTarget { return f.Webhook }

func (f *fakeDiscord) InvalidateChannelWebhook(guildID, channelID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Invalidated = append(f.Invalidated, guildID+":"+channelID)
}

func (f *fakeDiscord) CanSend(channelID string) bool { return !f.NoSendPerms[channelID] }

func (f *fakeDiscord) GuildHasRole(_, roleID string) bool { return f.Roles[roleID] }

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

func (f *fakeSentState) MarkShowsNotified(_ context.Context, id string, showNames []string) error {
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
		for _, n := range showNames {
			if s.Show == n {
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
	c := *ap
	f.Autoposts[apKey(guildID, postType)] = &c
	return nil
}

func (f *fakeSentState) GetGuildSettings(_ context.Context, guildID string) (*guilds.GuildSettings, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.Settings[guildID], nil
}

func (f *fakeSentState) SaveFeedCheck(_ context.Context, guildID string, fc feed_checks.FeedCheck) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	fc.GuildID = guildID
	for i := range f.FeedChecks {
		if f.FeedChecks[i].GuildID == guildID && f.FeedChecks[i].GUID == fc.GUID {
			f.FeedChecks[i] = fc
			return nil
		}
	}
	f.FeedChecks = append(f.FeedChecks, fc)
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

func (f *fakeSentState) SaveReminders(_ context.Context, id string, slice *reminders.RemindMeSlice) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if slice == nil || len(slice.Reminders) == 0 {
		delete(f.Reminders, id)
		return nil
	}
	c := *slice
	c.ID = id
	c.Reminders = append([]reminders.RemindMe(nil), slice.Reminders...)
	f.Reminders[id] = &c
	return nil
}

var (
	_ DiscordPort = (*fakeDiscord)(nil)
	_ SentState   = (*fakeSentState)(nil)
)
