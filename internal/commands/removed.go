package commands

import (
	"context"

	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/interaction"

	"github.com/bwmarrin/discordgo"
)

// Reddit feeds were removed, but a moderator may still have a private /reddit list open from before the deploy.
// Its page buttons and Remove picker get this refusal instead of failing silently.
// REMOVE on 2026-12-17: by then no such list is still open.
const (
	removedFeedsPageKey      = "reddit"
	removedFeedsPickerPrefix = "reddit-rm:"
	msgFeedsRemoved          = "Reddit feeds were removed, since Reddit shut down its RSS feeds."
)

func init() {
	RegisterPaginationRenderer(removedFeedsPageKey, func(context.Context, *database.Client, *discordgo.Session, *discordgo.InteractionCreate, int, int, string) Page {
		return Page{Refusal: msgFeedsRemoved}
	})
	AddComponent(removedFeedsPickerPrefix, func(ic *interaction.Context, _ *discordgo.Session) error {
		return ic.Refuse(msgFeedsRemoved)
	})
}
