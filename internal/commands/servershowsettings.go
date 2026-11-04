package commands

import (
	"context"

	"ReZeroTsu/internal/animeschedule"
	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/database/guilds"
	"ReZeroTsu/internal/interaction"

	"github.com/bwmarrin/discordgo"
)

// ServerShowSettings reads, in one lookup through read, whether the server shows donghua and its preferred track. Where
// the bot has no server membership (DMs, and a user install in a server it has not joined) there are no settings;
// donghua is shown and the track is Sub, as when the settings cannot be read. /schedule and /subscription add read
// the setting here, so they share one rule; the autoposts in internal/bot restate the same default themselves.
func ServerShowSettings(ctx context.Context, db *database.Client, i *discordgo.Interaction, read func(context.Context, *database.Client, string) (*guilds.GuildSettings, error)) (donghua bool, pref animeschedule.PreferredTrack) {
	if !interaction.BotInGuild(i) {
		return true, animeschedule.PreferSub
	}
	settings, err := read(ctx, db, i.GuildID)
	if err != nil || settings == nil {
		return true, animeschedule.PreferSub
	}
	return settings.Donghua, settings.Preferred()
}
