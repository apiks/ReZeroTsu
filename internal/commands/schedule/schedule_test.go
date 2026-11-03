package schedule

import (
	"context"
	"strings"
	"testing"
	"time"

	"ReZeroTsu/internal/animeschedule"
	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/interaction"
	"ReZeroTsu/internal/interaction/interactiontest"

	"github.com/bwmarrin/discordgo"
)

// setClock fixes the shared clock to a Wednesday (2026-11-04 12:00 UTC) for the test.
func setClock(t *testing.T) {
	t.Helper()
	t.Cleanup(animeschedule.SetClock(func() time.Time { return time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC) }))
}

// Just after UTC midnight the host may still be on the previous day; /schedule without a day opens the UTC day.
func TestScheduleWithoutDayOpensTheUTCDayNotTheHostsLocalDay(t *testing.T) {
	// 2026-11-04 00:30 UTC is still Tuesday 2026-11-03 at UTC-10.
	far := time.FixedZone("far", -10*3600)
	t.Cleanup(animeschedule.SetClock(func() time.Time { return time.Date(2026, 11, 3, 14, 30, 0, 0, far) }))
	prev := time.Local
	time.Local = far
	t.Cleanup(func() { time.Local = prev })
	t.Cleanup(animeschedule.Seed(map[string][]animeschedule.ShowEntry{
		"2026-11-03": {{Name: "Tuesday Show", AirType: "sub", AirTimeUnix: 1700000000}},
		"2026-11-04": {{Name: "Wednesday Show", AirType: "sub", AirTimeUnix: 1700000000}},
	}))
	animeschedule.SetAPIKey("key")
	t.Cleanup(func() { animeschedule.SetAPIKey("") })

	fake := runSchedule(t, "")
	calls := fake.Calls()
	emb := calls[len(calls)-1].Response.Embeds[0]
	if !strings.Contains(emb.Description, "Wednesday Show") || strings.Contains(emb.Description, "Tuesday Show") {
		t.Errorf("description = %q, want the UTC Wednesday", emb.Description)
	}
	if !strings.HasPrefix(emb.Title, "Wednesday, 4 Nov 2026") {
		t.Errorf("title = %q, want the UTC date", emb.Title)
	}
}

func runSchedule(t *testing.T, day string) *interactiontest.Fake {
	t.Helper()
	fake := &interactiontest.Fake{}
	data := discordgo.ApplicationCommandInteractionData{Name: "schedule"}
	if day != "" {
		data.Options = []*discordgo.ApplicationCommandInteractionDataOption{{Name: "day", Type: discordgo.ApplicationCommandOptionString, Value: day}}
	}
	i := &discordgo.Interaction{Type: discordgo.InteractionApplicationCommand, ChannelID: "chan", Data: data, User: &discordgo.User{ID: "user"}}
	ic := interaction.NewContext(fake, i, (*database.Client)(nil), interaction.Public, interaction.Deferred)
	ic.Ctx = context.Background()
	if err := ic.Defer(interaction.Public); err != nil {
		t.Fatal(err)
	}
	if err := handleSchedule(ic); err != nil {
		t.Fatalf("Run returned %v", err)
	}
	return fake
}

func TestScheduleIsPublicAndDeferred(t *testing.T) {
	cmd := findCommand(t)
	if cmd.Visibility != commands.Public || cmd.Response != commands.Deferred || cmd.Run == nil {
		t.Errorf("schedule must be a public, deferred command: %+v", cmd)
	}
}

func findCommand(t *testing.T) *commands.Command {
	t.Helper()
	for _, c := range commands.AllCommands() {
		if c.Name == "schedule" {
			return c
		}
	}
	t.Fatal("schedule is not registered")
	return nil
}

func TestScheduleShowsTheRequestedDayWithPagingButtons(t *testing.T) {
	setClock(t)
	t.Cleanup(animeschedule.Seed(map[string][]animeschedule.ShowEntry{
		"2026-11-02": {{Name: "Frieren", Episode: "Episode 5", AirType: "sub", AirTimeUnix: 1700000000}},
	}))
	animeschedule.SetAPIKey("key")
	t.Cleanup(func() { animeschedule.SetAPIKey("") })

	fake := runSchedule(t, "monday")
	calls := fake.Calls()
	if len(calls) != 2 || calls[0].Op != "Defer" || calls[0].Visibility != interaction.Public || calls[1].Op != "EditReply" {
		t.Fatalf("want a public Defer then EditReply, got %v", fake.Ops())
	}
	r := calls[1].Response
	if len(r.Embeds) != 1 || !strings.Contains(r.Embeds[0].Description, "Frieren") {
		t.Errorf("embed should list Monday's show: %+v", r.Embeds)
	}
	row := r.Components[0].(discordgo.ActionsRow)
	next := row.Components[1].(discordgo.Button)
	if next.CustomID != commands.BuildPaginationCustomID("schedule", 2, 7, "user") {
		t.Errorf("Next button = %q, want it to carry the author and page 2 of 7", next.CustomID)
	}
}

// Without server state (a user install in a server the bot has not joined, or a DM) there are no settings to
// read, so /schedule includes donghua. The nil database proves no settings read is attempted.
func TestScheduleWithoutServerStateIncludesDonghua(t *testing.T) {
	setClock(t)
	t.Cleanup(animeschedule.Seed(map[string][]animeschedule.ShowEntry{
		"2026-11-02": {{Name: "Soul Land", Episode: "Episode 9", AirType: "sub", AirTimeUnix: 1700000000, Donghua: true}},
	}))
	animeschedule.SetAPIKey("key")
	t.Cleanup(func() { animeschedule.SetAPIKey("") })

	userInstall := map[discordgo.ApplicationIntegrationType]string{discordgo.ApplicationIntegrationUserInstall: "user"}
	for name, guildID := range map[string]string{"server without the bot": "other-guild", "DM": ""} {
		t.Run(name, func(t *testing.T) {
			fake := &interactiontest.Fake{}
			i := &discordgo.Interaction{Type: discordgo.InteractionApplicationCommand, GuildID: guildID, ChannelID: "chan", User: &discordgo.User{ID: "user"},
				AuthorizingIntegrationOwners: userInstall,
				Data: discordgo.ApplicationCommandInteractionData{Name: "schedule",
					Options: []*discordgo.ApplicationCommandInteractionDataOption{{Name: "day", Type: discordgo.ApplicationCommandOptionString, Value: "monday"}}}}
			ic := interaction.NewContext(fake, i, (*database.Client)(nil), interaction.Public, interaction.Deferred)
			ic.Ctx = context.Background()
			if err := ic.Defer(interaction.Public); err != nil {
				t.Fatal(err)
			}
			if err := handleSchedule(ic); err != nil {
				t.Fatal(err)
			}
			calls := fake.Calls()
			if got := calls[len(calls)-1].Response.Embeds[0].Description; !strings.Contains(got, "Soul Land") {
				t.Errorf("description = %q, want the donghua show listed", got)
			}
		})
	}
}

func TestScheduleRefusalsArePrivate(t *testing.T) {
	animeschedule.SetAPIKey("key")
	t.Cleanup(func() { animeschedule.SetAPIKey("") })
	t.Cleanup(animeschedule.Seed(map[string][]animeschedule.ShowEntry{}))

	tests := []struct {
		name string
		key  string
		day  string
		want string
	}{
		{"no API key", "", "", msgNotConfigured},
		{"unparsable day", "key", "someday", msgCannotParseDay},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			animeschedule.SetAPIKey(tt.key)
			fake := runSchedule(t, tt.day)
			calls := fake.Calls()
			// Defer public, then the placeholder is deleted and the refusal follows up privately.
			if len(calls) != 3 || calls[1].Op != "DeleteReply" || calls[2].Op != "FollowUp" ||
				calls[2].Visibility != interaction.Private || calls[2].Response.Content != tt.want {
				t.Errorf("want delete + private follow-up %q, got %+v", tt.want, calls)
			}
		})
	}
}
