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

// ReadMemberSubs reads a member's subscription document by user ID; nil when the member has none.
type ReadMemberSubs func(ctx context.Context, db *database.Client, userID string) (*anime_subs.AnimeSubs, error)

// ServerShowSettings reads, in one lookup through read, whether the server shows donghua, its preferred track and its
// preferred name. Where the bot has no server membership (DMs, and a user install in a server it has not joined)
// there are no settings: donghua is shown, the track is Sub, and the name is the invoking member's own preferred
// name, read through member. In a server the bot is a member of, settings that cannot be read give donghua shown,
// Sub and Title. A caller that needs no preferred name passes a nil member and gets Title. /schedule and
// /subscription add read the setting here, so they share one rule; the autoposts in internal/bot restate the same
// default themselves.
func ServerShowSettings(ctx context.Context, db *database.Client, i *discordgo.Interaction, read func(context.Context, *database.Client, string) (*guilds.GuildSettings, error), member ReadMemberSubs) (donghua bool, pref animeschedule.PreferredTrack, name animeschedule.PreferredName) {
	if !interaction.BotInGuild(i) {
		return true, animeschedule.PreferSub, memberPreferredName(ctx, db, i, member)
	}
	settings, err := read(ctx, db, i.GuildID)
	if err != nil || settings == nil {
		return true, animeschedule.PreferSub, animeschedule.PreferTitle
	}
	return settings.Donghua, settings.Preferred(), settings.PreferredShowName()
}

// memberPreferredName is the preferred name of the member whose interaction i is, read through member; Title when
// member is nil or the read fails.
func memberPreferredName(ctx context.Context, db *database.Client, i *discordgo.Interaction, member ReadMemberSubs) animeschedule.PreferredName {
	if member == nil {
		return animeschedule.PreferTitle
	}
	var user *discordgo.User
	if i.Member != nil {
		user = i.Member.User
	} else {
		user = i.User
	}
	if user == nil {
		return animeschedule.PreferTitle
	}
	doc, err := member(ctx, db, user.ID)
	if err != nil {
		return animeschedule.PreferTitle
	}
	return animeschedule.MemberPreferredName(doc)
}
