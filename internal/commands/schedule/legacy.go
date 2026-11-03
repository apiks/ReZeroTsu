package schedule

import (
	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/interaction"

	"github.com/bwmarrin/discordgo"
)

// legacyPrefix starts the custom ID of a button on a schedule message posted before the day picker, when the
// schedule paged through the shared pagination handler. It is longer than the pagination prefix, so it wins.
const legacyPrefix = "pag:schedule:"

func init() {
	commands.AddComponent(legacyPrefix, handleLegacyPress)
}

// handleLegacyPress answers a press on an old schedule message privately, whoever presses: those pages were
// numbered by weekday, which no longer maps to a day.
func handleLegacyPress(ic *interaction.Context, _ *discordgo.Session) error {
	return ic.Refuse(msgDayNotLoaded)
}
