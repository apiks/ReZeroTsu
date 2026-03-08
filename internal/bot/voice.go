package bot

import (
	"context"
	"slices"

	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/database/guilds"
	"ReZeroTsu/internal/discord"
	"ReZeroTsu/internal/logger"

	"github.com/bwmarrin/discordgo"
)

// removeVoiceRoleFromSettings removes the role at channel/role indices and persists; removes channel entry if no roles left.
func removeVoiceRoleFromSettings(ctx context.Context, db *database.Client, settings *guilds.GuildSettings, guildID string, chaIdx, roleIdx int) {
	roles := settings.VoiceChas[chaIdx].Roles
	settings.VoiceChas[chaIdx].Roles = append(roles[:roleIdx], roles[roleIdx+1:]...)
	if len(settings.VoiceChas[chaIdx].Roles) == 0 {
		settings.VoiceChas = append(settings.VoiceChas[:chaIdx], settings.VoiceChas[chaIdx+1:]...)
	}
	_ = db.Guilds().SetGuildSettings(ctx, guildID, settings)
}

// OnVoiceStateUpdate toggles voice-channel roles on join/leave; user gets channel's roles while in it, removed when leaving.
func OnVoiceStateUpdate(ctx context.Context, db *database.Client) func(*discordgo.Session, *discordgo.VoiceStateUpdate) {
	return func(s *discordgo.Session, v *discordgo.VoiceStateUpdate) {
		if v.GuildID == "" {
			return
		}

		settings, err := db.Guilds().GetGuildSettings(ctx, v.GuildID)
		if err != nil || settings == nil {
			return
		}
		if len(settings.VoiceChas) == 0 {
			return
		}

		var noRemovalRoleIDs []string

		for chaIdx, cha := range settings.VoiceChas {
			if cha.ID == "" {
				continue
			}

			perms, err := s.State.UserChannelPermissions(s.State.User.ID, cha.ID)
			if err != nil {
				continue
			}
			if perms&discordgo.PermissionViewChannel != discordgo.PermissionViewChannel {
				continue
			}
			if perms&discordgo.PermissionManageRoles != discordgo.PermissionManageRoles {
				continue
			}

			for roleIdx, chaRole := range cha.Roles {
				if chaRole.ID == "" {
					continue
				}

				if v.ChannelID == cha.ID {
					if err := s.GuildMemberRoleAdd(v.GuildID, v.UserID, chaRole.ID); err != nil {
						if discord.IsUnknownRole(err) {
							removeVoiceRoleFromSettings(ctx, db, settings, v.GuildID, chaIdx, roleIdx)
							logger.For("bot").Warn("role deleted (10011), removed from voice roles", append(discord.RESTAttrs(err), "guild_id", v.GuildID, "role_id", chaRole.ID, "err", err)...)
							break
						}
						logger.For("bot").Error("voice role add failed", "guild_id", v.GuildID, "user_id", v.UserID, "role_id", chaRole.ID, "err", err)
						continue
					}
					noRemovalRoleIDs = append(noRemovalRoleIDs, chaRole.ID)
					continue
				}

				// Remove role only if we didn't just add it in this pass
				if slices.Contains(noRemovalRoleIDs, chaRole.ID) {
					continue
				}
				if err := s.GuildMemberRoleRemove(v.GuildID, v.UserID, chaRole.ID); err != nil && discord.IsUnknownRole(err) {
					removeVoiceRoleFromSettings(ctx, db, settings, v.GuildID, chaIdx, roleIdx)
					logger.For("bot").Warn("role deleted (10011), removed from voice roles", append(discord.RESTAttrs(err), "guild_id", v.GuildID, "role_id", chaRole.ID, "err", err)...)
					break
				}
			}
		}
	}
}
