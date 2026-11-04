package schedule

import (
	"context"
	"errors"
	"strings"
	"testing"

	"ReZeroTsu/internal/animeschedule"
	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/database/guilds"
	"ReZeroTsu/internal/interaction"
	"ReZeroTsu/internal/interaction/interactiontest"

	"github.com/bwmarrin/discordgo"
)

// Two shows whose title, romaji, English and native names all differ; the second has no English name.
var (
	frierenNames = animeschedule.ShowEntry{
		Name: "Frieren Title", Romaji: "Sousou no Frieren", English: "Frieren: Beyond Journey's End", Native: "葬送のフリーレン",
		Route: "frieren", Episode: "Ep 5", Track: animeschedule.TrackSub, AirTimeUnix: 1700000000,
	}
	apothecaryNames = animeschedule.ShowEntry{
		Name: "Apothecary Title", Romaji: "Kusuriya no Hitorigoto", Native: "薬屋のひとりごと",
		Route: "apothecary", Episode: "Ep 3", Track: animeschedule.TrackSub, AirTimeUnix: 1700000100,
	}
)

// seedNamedShows seeds both shows on Wednesday 2026-11-04 and Thursday 2026-11-05.
func seedNamedShows(t *testing.T) {
	t.Helper()
	setClock(t)
	day := []animeschedule.ShowEntry{frierenNames, apothecaryNames}
	t.Cleanup(animeschedule.Seed(map[string][]animeschedule.ShowEntry{"2026-11-04": day, "2026-11-05": day}))
	animeschedule.SetAPIKey("key")
	t.Cleanup(func() { animeschedule.SetAPIKey("") })
}

// serverNames replaces the settings lookup with a server whose stored settings are settings (or a failed read when
// err is set) and returns its read count.
func serverNames(t *testing.T, settings *guilds.GuildSettings, err error) *int {
	t.Helper()
	reads := 0
	prev := guildSettings
	guildSettings = func(context.Context, *database.Client, string) (*guilds.GuildSettings, error) {
		reads++
		return settings, err
	}
	t.Cleanup(func() { guildSettings = prev })
	return &reads
}

// wantNames fails unless the page calls the two shows by exactly these names, and by no other of their names.
func wantNames(t *testing.T, where string, emb *discordgo.MessageEmbed, frieren, apothecary string) {
	t.Helper()
	for _, show := range []struct {
		e    animeschedule.ShowEntry
		want string
	}{{frierenNames, frieren}, {apothecaryNames, apothecary}} {
		if !strings.Contains(emb.Description, "**"+show.want+"** — ") {
			t.Errorf("%s: page %q does not call %s %q", where, emb.Description, show.e.Route, show.want)
		}
		for _, other := range show.e.AllNames() {
			if other != show.want && strings.Contains(emb.Description, other) {
				t.Errorf("%s: page %q also shows %q", where, emb.Description, other)
			}
		}
	}
}

// runScheduleAs runs /schedule for Wednesday from guildID as user; userInstallOnly marks a user install in a server
// the bot has not joined.
func runScheduleAs(t *testing.T, guildID string, userInstallOnly bool) *discordgo.MessageEmbed {
	t.Helper()
	fake := &interactiontest.Fake{}
	data := discordgo.ApplicationCommandInteractionData{Name: "schedule", Options: []*discordgo.ApplicationCommandInteractionDataOption{
		{Name: "day", Type: discordgo.ApplicationCommandOptionString, Value: "wednesday"},
	}}
	i := &discordgo.Interaction{Type: discordgo.InteractionApplicationCommand, GuildID: guildID, ChannelID: "chan", Data: data, User: &discordgo.User{ID: "user"}}
	if userInstallOnly {
		i.AuthorizingIntegrationOwners = map[discordgo.ApplicationIntegrationType]string{discordgo.ApplicationIntegrationUserInstall: "user"}
	}
	ic := interaction.NewContext(fake, i, (*database.Client)(nil), interaction.Public, interaction.Deferred)
	ic.Ctx = context.Background()
	if err := ic.Defer(interaction.Public); err != nil {
		t.Fatal(err)
	}
	if err := handleSchedule(ic); err != nil {
		t.Fatal(err)
	}
	calls := fake.Calls()
	return calls[len(calls)-1].Response.Embeds[0]
}

func TestScheduleInAServerCallsShowsByTheServersPreferredNameElseTheTitle(t *testing.T) {
	for _, tc := range []struct {
		stored              string
		frieren, apothecary string
	}{
		{"", "Frieren Title", "Apothecary Title"},
		{"title", "Frieren Title", "Apothecary Title"},
		{"romaji", "Sousou no Frieren", "Kusuriya no Hitorigoto"},
		// The show without an English name falls back to its title, never to its romaji or native name.
		{"english", "Frieren: Beyond Journey's End", "Apothecary Title"},
		{"native", "葬送のフリーレン", "薬屋のひとりごと"},
	} {
		t.Run("preferred name "+tc.stored, func(t *testing.T) {
			seedNamedShows(t)
			serverNames(t, &guilds.GuildSettings{Donghua: true, PreferredName: tc.stored}, nil)
			wantNames(t, "/schedule", runScheduleAs(t, "guild", false), tc.frieren, tc.apothecary)

			// A page press reads the server's preferred name again, for the author and for another member's copy.
			for _, user := range []string{"user", "stranger"} {
				calls := pressNavIn(t, "sched:2026-11-05:user:sub", user, "guild", publicMsg())
				wantNames(t, user+"'s press", calls[0].Response.Embeds[0], tc.frieren, tc.apothecary)
			}
			picked := choosePickIn(t, "schedpick:user:sub", "2026-11-05", "user", "guild")
			wantNames(t, "day picker", picked[0].Response.Embeds[0], tc.frieren, tc.apothecary)
		})
	}
}

func TestScheduleShowsTitlesWhereThereIsNoReadableServerSetting(t *testing.T) {
	for _, tc := range []struct {
		name     string
		settings *guilds.GuildSettings
		err      error
	}{
		{"no stored settings", guilds.DefaultGuildSettings(), nil},
		{"no settings document", nil, nil},
		{"unreadable settings", nil, errors.New("boom")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seedNamedShows(t)
			serverNames(t, tc.settings, tc.err)
			wantNames(t, "/schedule", runScheduleAs(t, "guild", false), "Frieren Title", "Apothecary Title")
		})
	}
}

func TestScheduleWithoutServerMembershipNeverReadsTheServersPreferredName(t *testing.T) {
	seedNamedShows(t)
	reads := serverNames(t, &guilds.GuildSettings{Donghua: true, PreferredName: "native"}, nil)
	wantNames(t, "DM", runScheduleAs(t, "", false), "Frieren Title", "Apothecary Title")
	wantNames(t, "user install", runScheduleAs(t, "guild", true), "Frieren Title", "Apothecary Title")
	if *reads != 0 {
		t.Errorf("settings were read %d times without server membership", *reads)
	}
}

// choosePickIn chooses date in the day picker as user in the server guildID, on a public message.
func choosePickIn(t *testing.T, customID, date, user, guildID string) []interactiontest.Call {
	t.Helper()
	fake := &interactiontest.Fake{}
	i := &discordgo.Interaction{Type: discordgo.InteractionMessageComponent, GuildID: guildID, ChannelID: "chan", Message: publicMsg(), User: &discordgo.User{ID: user},
		Data: discordgo.MessageComponentInteractionData{CustomID: customID, ComponentType: discordgo.SelectMenuComponent, Values: []string{date}}}
	ic := interaction.NewContext(fake, i, (*database.Client)(nil), interaction.Private, interaction.Direct)
	if err := handlePick(ic, nil); err != nil {
		t.Fatal(err)
	}
	calls := fake.Calls()
	if len(calls) == 0 {
		t.Fatalf("choosing %q answered nothing", date)
	}
	return calls
}
