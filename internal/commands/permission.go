package commands

import "ReZeroTsu/internal/interaction"

// ModeratorRefusal re-runs the bot-side moderator check for a button press, picker choice or form submission,
// the same bounded-timeout check dispatch runs before a PermMod command. It returns the private text to show
// when the member may not act, or "" when they may. A lookup that does not finish counts as a refusal, so the
// check fails closed. settings is the guild store the check reads the roles granted through /add-moderator-role from.
func ModeratorRefusal(ic *interaction.Context, settings settingsReader) string {
	return checkPermission(ic.Context(), PermMod, settings, ic.Port, ic.Interaction)
}
