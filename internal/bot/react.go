package bot

import (
	"context"
	"strconv"
	"strings"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/database/guilds"
	"ReZeroTsu/internal/logger"

	"github.com/bwmarrin/discordgo"
)

// reactRaffleStore is the part of the guild repository the legacy reaction raffles use.
type reactRaffleStore interface {
	GetRaffles(ctx context.Context, guildID string) ([]*guilds.Raffle, error)
	UpdateRaffleParticipant(ctx context.Context, guildID, raffleName, userID string, remove bool) (bool, error)
}

// reactRaffleStoreOf picks the reaction raffle store of a database client; tests replace it.
var reactRaffleStoreOf = func(db *database.Client) reactRaffleStore { return db.Guilds() }

// handleSlotReaction enters (or, with remove, removes) the reacting member of the reaction raffle whose message
// was reacted to. It reports whether the emoji is the raffle's slot emoji, which ends the handler whether or not
// a raffle matched; any other emoji returns false and changes nothing. A store failure is dropped, as before.
// Guard homes: the guild-only and own-reaction checks stay in the two handlers, before this runs; the message
// match (ReactMessageID) is below.
func handleSlotReaction(ctx context.Context, store reactRaffleStore, guildID, messageID, userID, emoji string, remove bool) bool {
	if emoji != "🎰" {
		return false
	}
	raffles, err := store.GetRaffles(ctx, guildID)
	if err != nil {
		return true
	}
	for _, raffle := range raffles {
		if raffle != nil && raffle.ReactMessageID == messageID {
			_, _ = store.UpdateRaffleParticipant(ctx, guildID, raffle.Name, userID, remove)
			return true
		}
	}
	return true
}

// OnMessageReactionAdd adds role on bound emoji reaction, or adds user to raffle on slot emoji.
func OnMessageReactionAdd(ctx context.Context, db *database.Client) func(*discordgo.Session, *discordgo.MessageReactionAdd) {
	return func(s *discordgo.Session, r *discordgo.MessageReactionAdd) {
		if r.GuildID == "" || r.UserID == s.State.User.ID {
			return
		}
		if handleSlotReaction(ctx, reactRaffleStoreOf(db), r.GuildID, r.MessageID, r.UserID, r.Emoji.APIName(), false) {
			return
		}
		reactLower := strings.TrimPrefix(strings.ToLower(r.Emoji.APIName()), "a:")
		roleID, ok := findRoleForEmoji(ctx, db, s, r.GuildID, r.MessageID, reactLower)
		if !ok || roleID == "" {
			return
		}
		if err := s.GuildMemberRoleAdd(r.GuildID, r.UserID, roleID); err != nil {
			logger.For("bot").Error("react add role failed", "guild_id", r.GuildID, "user_id", r.UserID, "role_id", roleID, "err", err)
			commands.LogToBotLog(ctx, db, s, r.GuildID, "[react] GuildMemberRoleAdd: "+err.Error())
		}
	}
}

// OnMessageReactionRemove removes role on bound emoji remove, or removes user from raffle on slot emoji remove.
func OnMessageReactionRemove(ctx context.Context, db *database.Client) func(*discordgo.Session, *discordgo.MessageReactionRemove) {
	return func(s *discordgo.Session, r *discordgo.MessageReactionRemove) {
		if r.GuildID == "" || r.UserID == s.State.User.ID {
			return
		}
		if handleSlotReaction(ctx, reactRaffleStoreOf(db), r.GuildID, r.MessageID, r.UserID, r.Emoji.APIName(), true) {
			return
		}
		reactLower := strings.TrimPrefix(strings.ToLower(r.Emoji.APIName()), "a:")
		roleID, ok := findRoleForEmoji(ctx, db, s, r.GuildID, r.MessageID, reactLower)
		if !ok || roleID == "" {
			return
		}
		_ = s.GuildMemberRoleRemove(r.GuildID, r.UserID, roleID)
	}
}

// findRoleForEmoji looks up react_join_map for guild+message, finds binding for emoji (lowercase), returns (roleID, true) if resolved.
func findRoleForEmoji(ctx context.Context, db *database.Client, s *discordgo.Session, guildID, messageID, emojiLower string) (string, bool) {
	m, err := db.Guilds().GetReactJoinMap(ctx, guildID)
	if err != nil || m == nil {
		return "", false
	}
	entry, ok := m[messageID]
	if !ok || entry == nil || len(entry.RoleEmojiMap) == 0 {
		return "", false
	}
	for _, roleToEmojis := range entry.RoleEmojiMap {
		for role, emojis := range roleToEmojis {
			for _, e := range emojis {
				storedNorm := strings.TrimPrefix(strings.ToLower(e), "a:")
				if storedNorm == emojiLower {
					rid := resolveRoleToID(s, guildID, role)
					return rid, rid != ""
				}
			}
		}
	}
	return "", false
}

func resolveRoleToID(s *discordgo.Session, guildID, role string) string {
	if len(role) >= 17 {
		if _, err := strconv.ParseInt(role, 10, 64); err == nil {
			return role
		}
	}
	roles, err := s.GuildRoles(guildID)
	if err != nil {
		return ""
	}
	roleLower := strings.ToLower(role)
	for _, r := range roles {
		if strings.ToLower(r.Name) == roleLower {
			return r.ID
		}
	}
	return ""
}
