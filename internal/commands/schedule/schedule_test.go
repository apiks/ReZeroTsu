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
		"2026-11-09": {{Name: "Frieren", Episode: "Episode 5", AirType: "sub", AirTimeUnix: 1700000000}},
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
	if next.CustomID != "sched:2026-11-03:user" {
		t.Errorf("Next button = %q, want it to carry Tuesday's date and the author", next.CustomID)
	}
}

// Without server state (a user install in a server the bot has not joined, or a DM) there are no settings to
// read, so /schedule includes donghua. The nil database proves no settings read is attempted.
func TestScheduleWithoutServerStateIncludesDonghua(t *testing.T) {
	setClock(t)
	t.Cleanup(animeschedule.Seed(map[string][]animeschedule.ShowEntry{
		"2026-11-09": {{Name: "Soul Land", Episode: "Episode 9", AirType: "sub", AirTimeUnix: 1700000000, Donghua: true}},
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

// scheduleDateFor runs /schedule with a day typed and returns the date key of the seeded show the page lists.
// Every date from 2026-10-31 to 2026-11-15 holds one show named after its date.
func scheduleDateFor(t *testing.T, now time.Time, day string) string {
	t.Helper()
	t.Cleanup(animeschedule.SetClock(func() time.Time { return now }))
	days := map[string][]animeschedule.ShowEntry{}
	for d := time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC); d.Before(time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)); d = d.AddDate(0, 0, 1) {
		days[animeschedule.DateKey(d)] = []animeschedule.ShowEntry{{Name: "show-" + animeschedule.DateKey(d), AirType: "sub", AirTimeUnix: 1700000000}}
	}
	t.Cleanup(animeschedule.Seed(days))
	animeschedule.SetAPIKey("key")
	t.Cleanup(func() { animeschedule.SetAPIKey("") })

	fake := runSchedule(t, day)
	calls := fake.Calls()
	last := calls[len(calls)-1]
	if len(last.Response.Embeds) == 0 {
		t.Fatalf("day %q: no page, got %+v", day, last)
	}
	desc := last.Response.Embeds[0].Description
	_, after, ok := strings.Cut(desc, "show-")
	if !ok || len(after) < 10 {
		t.Fatalf("day %q: description %q lists no seeded show", day, desc)
	}
	return after[:10]
}

func TestScheduleDayGrammarResolvesToUTCDates(t *testing.T) {
	sat := time.Date(2026, 11, 7, 23, 30, 0, 0, time.UTC)
	sun := time.Date(2026, 11, 8, 9, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		now  time.Time
		day  string
		want string
	}{
		{"today", sat, "today", "2026-11-07"},
		{"tomorrow", sat, "Tomorrow", "2026-11-08"},
		{"tomorrow on Sunday crosses into next Monday", sun, "tomorrow", "2026-11-09"},
		{"monday on a Saturday is the coming Monday", sat, "monday", "2026-11-09"},
		{"today's weekday name opens today", sat, "saturday", "2026-11-07"},
		{"short name", sat, "tue", "2026-11-10"},
		{"plural", sat, "Fridays", "2026-11-13"},
		{"sunday on a Sunday opens today", sun, "sun", "2026-11-08"},
		{"picked autocomplete date", sat, "2026-11-12", "2026-11-12"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := scheduleDateFor(t, tt.now, tt.day); got != tt.want {
				t.Errorf("day %q opened %s, want %s", tt.day, got, tt.want)
			}
		})
	}
}

func autocompleteDay(t *testing.T, typed string) []*discordgo.ApplicationCommandOptionChoice {
	t.Helper()
	i := &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{}}
	return findCommand(t).AutocompleteOption(t.Context(), nil, nil, i, "day", typed)
}

func TestScheduleDayAutocompleteOffersTodayTomorrowThenSevenDatedDays(t *testing.T) {
	setClock(t) // Wednesday 2026-11-04
	var names []string
	for _, c := range autocompleteDay(t, "") {
		names = append(names, c.Name)
	}
	want := []string{"Today", "Tomorrow", "Wednesday 4 Nov", "Thursday 5 Nov", "Friday 6 Nov", "Saturday 7 Nov", "Sunday 8 Nov", "Monday 9 Nov", "Tuesday 10 Nov"}
	if strings.Join(names, "|") != strings.Join(want, "|") {
		t.Errorf("choices = %v, want %v", names, want)
	}
}

func TestScheduleDayAutocompleteFiltersAnywhereIgnoringCaseAndPickedValueResolves(t *testing.T) {
	setClock(t)
	if got := autocompleteDay(t, "NOV"); len(got) != 7 {
		t.Fatalf("matching the month should keep the seven dated days, got %d", len(got))
	}
	got := autocompleteDay(t, "ues")
	if len(got) != 1 || got[0].Name != "Tuesday 10 Nov" {
		t.Fatalf("matching inside a name = %+v, want only Tuesday 10 Nov", got)
	}
	if v := scheduleDateFor(t, time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC), got[0].Value.(string)); v != "2026-11-10" {
		t.Errorf("picked value opened %s, want 2026-11-10", v)
	}
}

func TestScheduleDayOptionDescriptionMentionsTodayAndTomorrow(t *testing.T) {
	cmd := findCommand(t)
	for name, d := range map[string]string{"option description": cmd.Options[0].Description, "command description (shown in /help)": cmd.Desc} {
		if !strings.Contains(d, "today") || !strings.Contains(d, "tomorrow") {
			t.Errorf("%s = %q, want it to mention today and tomorrow", name, d)
		}
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
		{"unknown day", "key", "someday", "I don't know that day. Pick a suggestion, or type a day like monday, tue or tomorrow."},
		{"impossible date", "key", "2026-13-45", "I don't know that day. Pick a suggestion, or type a day like monday, tue or tomorrow."},
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
