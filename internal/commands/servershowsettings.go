package commands

import (
	"context"

	"ReZeroTsu/internal/animeschedule"
	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/database/anime_subs"
	"ReZeroTsu/internal/database/guilds"
	"ReZeroTsu/internal/interaction"

	"github.com/bwmarrin/discordgo"
)

// ReadServerSettings reads a server's settings by guild ID.
type ReadServerSettings func(ctx context.Context, db *database.Client, guildID string) (*guilds.GuildSettings, error)

// ReadMemberDocument reads a member's subscription document by user ID; nil when the member has none.
type ReadMemberDocument func(ctx context.Context, db *database.Client, userID string) (*anime_subs.AnimeSubs, error)

// ServerShowSettings is the show settings of the server interaction i runs in, read in one lookup through read, and
// whether there is one. Where the bot has no server membership (DMs, and a user install in a server it has not
// joined) there is no server: inServer is false and the settings are the default. In a server the bot is a member
// of, settings that cannot be read are the default. /subscription add reads its donghua setting here.
func ServerShowSettings(ctx context.Context, db *database.Client, i *discordgo.Interaction, read ReadServerSettings) (settings animeschedule.ShowSettings, inServer bool) {
	if !interaction.BotInGuild(i) {
		return animeschedule.DefaultShowSettings(), false
	}
	gs, err := read(ctx, db, i.GuildID)
	if err != nil {
		return animeschedule.DefaultShowSettings(), true
	}
	return gs.ShowSettings(), true
}

// InvokerShowSettings is the show settings that apply to the member whose interaction i is: the server's
// (ServerShowSettings), or where there is no server the default with the member's own preferred name, read through
// member. A member whose preferred name cannot be read gets Title. /schedule reads its settings here.
func InvokerShowSettings(ctx context.Context, db *database.Client, i *discordgo.Interaction, read ReadServerSettings, member ReadMemberDocument) animeschedule.ShowSettings {
	settings, inServer := ServerShowSettings(ctx, db, i, read)
	if inServer {
		return settings
	}
	if doc, err := member(ctx, db, UserID(i)); err == nil {
		settings.Name = animeschedule.MemberPreferredName(doc)
	}
	return settings
}
