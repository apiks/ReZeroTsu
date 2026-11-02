package bot

import (
	"context"
	"fmt"
	"testing"
	"time"

	"ReZeroTsu/internal/animeschedule"
	"ReZeroTsu/internal/database/anime_subs"
	"ReZeroTsu/internal/database/guilds"
)

func TestProcessGuildNewEpisodes(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	aired := now.Unix() - 600
	future := now.Unix() + 3600
	shows := []animeschedule.ShowEntry{
		{Name: "Aired One", Route: "aired-one", Episode: "Episode 1", AirTimeUnix: aired, AirType: "sub"},
		{Name: "Aired Two", Route: "aired-two", Episode: "Episode 2", AirTimeUnix: aired, AirType: "sub"},
		{Name: "Later", Route: "later", Episode: "Episode 3", AirTimeUnix: future, AirType: "sub"},
		{Name: "Delayed", Route: "delayed", Episode: "Episode 4", AirTimeUnix: aired, Delayed: "delayed", AirType: "sub"},
	}
	dayShows := func(bool) []animeschedule.ShowEntry { return shows }

	newState := func() *fakeSentState {
		st := newFakeSentState()
		st.Autoposts[apKey("g1", postTypeNewEpisodes)] = &guilds.Autopost{PostType: postTypeNewEpisodes, ID: "c1", RoleID: "r1"}
		// Document already created by the daily seed with nothing notified yet.
		st.AnimeSubs["c1"] = &anime_subs.AnimeSubs{ID: "c1", IsGuild: true, Shows: []*anime_subs.ShowSub{
			{Show: "Aired One", Guild: true}, {Show: "Aired Two", Guild: true}, {Show: "Later", Guild: true}, {Show: "Delayed", Guild: true},
		}}
		return st
	}

	tests := []struct {
		name string
		run  func(t *testing.T, d *fakeDiscord, st *fakeSentState)
	}{
		{
			name: "posts aired shows once with one role ping and records them as notified",
			run: func(t *testing.T, d *fakeDiscord, st *fakeSentState) {
				d.Roles["r1"] = true
				processGuildNewEpisodes(context.Background(), d, st, "g1", dayShows, now)

				got := d.deliveredTo("c1")
				if len(got) != 1 {
					t.Fatalf("delivered %d messages, want 1", len(got))
				}
				if got[0].Msg.Content != "<@&r1>" {
					t.Errorf("content = %q, want role ping", got[0].Msg.Content)
				}
				if n := len(got[0].Msg.Embeds); n != 2 {
					t.Fatalf("embeds = %d, want 2 (aired, non-delayed shows only)", n)
				}
				doc, _ := st.GetAnimeSubs(context.Background(), "c1")
				notified := map[string]bool{}
				for _, s := range doc.Shows {
					notified[s.Show] = s.Notified
				}
				want := map[string]bool{"Aired One": true, "Aired Two": true, "Later": false, "Delayed": false}
				for k, v := range want {
					if notified[k] != v {
						t.Errorf("notified[%q] = %v, want %v", k, notified[k], v)
					}
				}

				processGuildNewEpisodes(context.Background(), d, st, "g1", dayShows, now)
				if n := len(d.deliveredTo("c1")); n != 1 {
					t.Errorf("second run delivered again: %d messages total, want 1", n)
				}
			},
		},
		{
			name: "uses the webhook when one is available",
			run: func(t *testing.T, d *fakeDiscord, st *fakeSentState) {
				d.Webhook = &WebhookTarget{ID: "wh", Token: "tok"}
				processGuildNewEpisodes(context.Background(), d, st, "g1", dayShows, now)
				got := d.deliveredTo("c1")
				if len(got) != 1 || got[0].Webhook == nil || got[0].Webhook.ID != "wh" {
					t.Fatalf("delivered = %+v, want one webhook message", got)
				}
			},
		},
		{
			name: "dead webhook is not reused for later chunks",
			run: func(t *testing.T, d *fakeDiscord, st *fakeSentState) {
				var many []animeschedule.ShowEntry
				var subs []*anime_subs.ShowSub
				for i := 0; i < 11; i++ {
					name := fmt.Sprintf("Show %d", i)
					many = append(many, animeschedule.ShowEntry{Name: name, Route: fmt.Sprintf("show-%d", i), Episode: "Episode 1", AirTimeUnix: aired, AirType: "sub"})
					subs = append(subs, &anime_subs.ShowSub{Show: name, Guild: true})
				}
				st.AnimeSubs["c1"] = &anime_subs.AnimeSubs{ID: "c1", IsGuild: true, Shows: subs}
				d.Webhook = &WebhookTarget{ID: "wh", Token: "tok"}
				d.script = []step{rejectWith(10015)}
				processGuildNewEpisodes(context.Background(), d, st, "g1", func(bool) []animeschedule.ShowEntry { return many }, now)
				if d.WebhookSends != 1 {
					t.Errorf("webhook attempts = %d, want 1", d.WebhookSends)
				}
				if got := len(d.deliveredTo("c1")); got != 2 {
					t.Errorf("delivered chunks = %d, want 2", got)
				}
			},
		},
		{
			name: "deleted channel clears the autopost and sent state",
			run: func(t *testing.T, d *fakeDiscord, st *fakeSentState) {
				d.script = []step{rejectWith(10003), rejectWith(10003)}
				processGuildNewEpisodes(context.Background(), d, st, "g1", dayShows, now)
				if len(d.Delivered) != 0 {
					t.Fatalf("delivered %d messages, want 0", len(d.Delivered))
				}
				if ap, _ := st.GetAutopost(context.Background(), "g1", postTypeNewEpisodes); ap != nil {
					t.Errorf("autopost still set: %+v", ap)
				}
				if doc, _ := st.GetAnimeSubs(context.Background(), "c1"); doc != nil {
					t.Errorf("anime subs doc for the channel still exists")
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.run(t, newFakeDiscord(), newState())
		})
	}
}
