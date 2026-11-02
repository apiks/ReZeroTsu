package bot

import (
	"context"

	"ReZeroTsu/internal/database/feed_checks"

	"github.com/bwmarrin/discordgo"
)

// ExistingFeedGUIDs is the feeds-ticket (#10) lookup: which of guids already have a feed check for the guild.
func (f *fakeSentState) ExistingFeedGUIDs(_ context.Context, guildID string, guids []string) (map[string]struct{}, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	want := make(map[string]struct{}, len(guids))
	for _, g := range guids {
		want[g] = struct{}{}
	}
	out := make(map[string]struct{})
	for _, fc := range f.FeedChecks {
		if fc.GuildID != guildID {
			continue
		}
		if _, ok := want[fc.GUID]; ok {
			out[fc.GUID] = struct{}{}
		}
	}
	return out, nil
}

// strictWriteState rejects feed-check writes made on a cancelled context, like the real database would.
type strictWriteState struct {
	*fakeSentState
}

func (s strictWriteState) SaveFeedChecks(ctx context.Context, guildID string, fcs []feed_checks.FeedCheck) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.fakeSentState.SaveFeedChecks(ctx, guildID, fcs)
}

// cancelAfterSendPort cancels the run context once its n-th Send has returned (a shutdown mid-flush).
type cancelAfterSendPort struct {
	*fakeDiscord
	cancel context.CancelFunc
	after  int
	calls  int
}

func (p *cancelAfterSendPort) Send(ctx context.Context, channelID string, webhook *WebhookTarget, msg OutgoingMessage) (string, error) {
	id, err := p.fakeDiscord.Send(ctx, channelID, webhook, msg)
	p.calls++
	if p.calls >= p.after {
		p.cancel()
	}
	return id, err
}

// embedAuthorURLs lists the embed author URLs of every delivered message in channelID.
func embedAuthorURLs(f *fakeDiscord, channelID string) []string {
	var out []string
	for _, d := range f.deliveredTo(channelID) {
		for _, e := range d.Msg.Embeds {
			out = append(out, authorURL(e))
		}
	}
	return out
}

func authorURL(e *discordgo.MessageEmbed) string {
	if e == nil || e.Author == nil {
		return ""
	}
	return e.Author.URL
}
