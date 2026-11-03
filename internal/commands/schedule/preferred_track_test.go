package schedule

import (
	"context"
	"strings"
	"testing"

	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/database/guilds"
	"ReZeroTsu/internal/interaction"
	"ReZeroTsu/internal/interaction/interactiontest"

	"github.com/bwmarrin/discordgo"
)

// serverPrefers replaces the settings lookup with a server whose preferred track is pref and returns its read count.
func serverPrefers(t *testing.T, pref string) *int {
	t.Helper()
	reads := 0
	prev := guildSettings
	guildSettings = func(context.Context, *database.Client, string) (*guilds.GuildSettings, error) {
		reads++
		return &guilds.GuildSettings{Donghua: true, PreferredTrack: pref}, nil
	}
	t.Cleanup(func() { guildSettings = prev })
	return &reads
}

func runWithTrackIn(t *testing.T, guildID, day, track string) *discordgo.MessageEmbed {
	t.Helper()
	fake := &interactiontest.Fake{}
	data := discordgo.ApplicationCommandInteractionData{Name: "schedule", Options: []*discordgo.ApplicationCommandInteractionDataOption{
		{Name: "day", Type: discordgo.ApplicationCommandOptionString, Value: day},
	}}
	if track != "" {
		data.Options = append(data.Options, &discordgo.ApplicationCommandInteractionDataOption{Name: "track", Type: discordgo.ApplicationCommandOptionString, Value: track})
	}
	i := &discordgo.Interaction{Type: discordgo.InteractionApplicationCommand, GuildID: guildID, ChannelID: "chan", Data: data, User: &discordgo.User{ID: "user"}}
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

func TestScheduleWithoutTrackFollowsTheServersPreferredTrack(t *testing.T) {
	seedFrieren(t)
	serverPrefers(t, "dub")
	emb := runWithTrackIn(t, "guild", "2026-11-03", "")
	if !strings.Contains(emb.Description, "Ep 2 DUB") || !strings.Contains(emb.Footer.Text, "Dub preferred") {
		t.Errorf("page = %q / %q, want the dub view", emb.Description, emb.Footer.Text)
	}
	serverPrefers(t, "raw")
	emb = runWithTrackIn(t, "guild", "wednesday", "")
	if !strings.Contains(emb.Description, "Ep 5 RAW") || !strings.Contains(emb.Footer.Text, "Raw preferred") {
		t.Errorf("page = %q / %q, want the raw view", emb.Description, emb.Footer.Text)
	}
}

func TestScheduleTrackOptionOverridesTheServersPreferredTrack(t *testing.T) {
	seedFrieren(t)
	serverPrefers(t, "dub")
	emb := runWithTrackIn(t, "guild", "wednesday", "sub")
	if !strings.Contains(emb.Description, "Ep 5 SUB") || strings.Contains(emb.Footer.Text, "preferred") {
		t.Errorf("page = %q / %q, want the sub view", emb.Description, emb.Footer.Text)
	}
}

func TestScheduleOutsideAJoinedServerUsesSubWhateverTheSetting(t *testing.T) {
	seedFrieren(t)
	reads := serverPrefers(t, "dub")
	emb := runWithTrackIn(t, "", "wednesday", "")
	if !strings.Contains(emb.Description, "Ep 5 SUB") || strings.Contains(emb.Footer.Text, "preferred") {
		t.Errorf("DM page = %q / %q, want the sub view", emb.Description, emb.Footer.Text)
	}
	if *reads != 0 {
		t.Errorf("settings were read %d times without a server", *reads)
	}
}

func TestControlsFromBeforeTracksOpenTheServersPreferenceNotSub(t *testing.T) {
	seedFrieren(t)
	serverPrefers(t, "dub")
	calls := pressNavIn(t, "sched:2026-11-03:user", "user", "guild", publicMsg())
	emb := calls[0].Response.Embeds[0]
	if !strings.Contains(emb.Description, "Ep 2 DUB") || !strings.Contains(emb.Footer.Text, "Dub preferred") {
		t.Errorf("old button = %q / %q, want the server's dub view", emb.Description, emb.Footer.Text)
	}
}
