package commands

import (
	"ReZeroTsu/internal/interaction"

	"github.com/bwmarrin/discordgo"
)

// CanHandOutRole applies Discord's own role hierarchy to the invoker: a role at or above their highest role is
// refused, so a role the bot hands out automatically is never more than they could assign by hand. The server owner
// may hand out any role. Administrator does not lift the rule, as in Discord. When the owner cannot be read, the
// role is refused. roles is the server's role list, which gives the invoker's roles their positions.
func CanHandOutRole(ic *interaction.Context, roles []*discordgo.Role, role *discordgo.Role) bool {
	if ic.Member == nil || role == nil {
		return false
	}
	top, held := 0, false
	for _, id := range ic.Member.Roles {
		for _, r := range roles {
			if r != nil && r.ID == id && (!held || r.Position > top) {
				top, held = r.Position, true
			}
		}
	}
	if held && role.Position < top {
		return true
	}
	guild, err := ic.Guild(ic.Context(), ic.GuildID)
	return err == nil && guild != nil && ic.Invoker != nil && guild.OwnerID == ic.Invoker.ID
}

// RoleAboveInvoker is the refusal for a role CanHandOutRole turned down; what names the feature, such as "a reaction role".
func RoleAboveInvoker(roleID, what string) string {
	return RoleMention(roleID) + " is at or above your highest role, so you can't set it up as " + what + ". Ask someone with a higher role, or the server owner."
}
