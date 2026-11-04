package commands

import (
	"context"

	"ReZeroTsu/internal/animeschedule"
	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/database/guilds"
	"ReZeroTsu/internal/interaction"

	"github.com/bwmarrin/discordgo"
)

// ServerShowSettings reads, in one lookup through read, whether the server shows donghua, its preferred track and its
// preferred name. Where the bot has no server membership (DMs, and a user install in a server it has not joined)
// there are no settings; donghua is shown, the track is Sub and the name is Title, as when the settings cannot be
// read. /schedule and /subscription add read the setting here, so they share one rule; the autoposts in
// internal/bot restate the same default themselves.
func ServerShowSettings(ctx context.Context, db *database.Client, i *discordgo.Interaction, read func(context.Context, *database.Client, string) (*guilds.GuildSettings, error)) (donghua bool, pref animeschedule.PreferredTrack, name animeschedule.PreferredName) {
	if !interaction.BotInGuild(i) {
		return true, animeschedule.PreferSub, animeschedule.PreferTitle
	}
	settings, err := read(ctx, db, i.GuildID)
	if err != nil || settings == nil {
		return true, animeschedule.PreferSub, animeschedule.PreferTitle
	}
	return settings.Donghua, settings.Preferred(), settings.PreferredShowName()
}
