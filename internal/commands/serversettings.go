package commands

import (
	"context"

	"ReZeroTsu/internal/animeschedule"
	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/database/guilds"
	"ReZeroTsu/internal/interaction"

	"github.com/bwmarrin/discordgo"
)

// ServerSettings reads, in one lookup through read, whether the server shows donghua and its preferred track. Where
// the bot has no server membership (DMs, and a user install in a server it has not joined) there are no settings;
// donghua is shown and the track is Sub, as when the settings cannot be read. Every command that offers shows by the
// server's donghua setting reads it here, so they share one rule.
func ServerSettings(ctx context.Context, db *database.Client, i *discordgo.Interaction, read func(context.Context, *database.Client, string) (*guilds.GuildSettings, error)) (donghua bool, pref animeschedule.PreferredTrack) {
	if !interaction.BotInGuild(i) {
		return true, animeschedule.PreferSub
	}
	settings, err := read(ctx, db, i.GuildID)
	if err != nil || settings == nil {
		return true, animeschedule.PreferSub
	}
	return settings.Donghua, settings.Preferred()
}
