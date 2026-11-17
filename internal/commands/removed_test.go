package commands

import (
	"testing"

	"ReZeroTsu/internal/interaction"
	"ReZeroTsu/internal/interaction/interactiontest"

	"github.com/bwmarrin/discordgo"
)

// The page buttons and Remove picker of a /reddit list opened before the removal say the feeds were removed.
func TestOldRedditListControlsSayFeedsWereRemoved(t *testing.T) {
	const author = "mod"
	controls := map[string]discordgo.MessageComponentInteractionData{
		"page button": press(BuildPaginationCustomID(removedFeedsPageKey, 1, 30, author)),
		"remove picker": {
			CustomID:      removedFeedsPickerPrefix + author + ":0",
			ComponentType: discordgo.SelectMenuComponent,
			Values:        []string{"abc"},
		},
	}
	for name, data := range controls {
		t.Run(name, func(t *testing.T) {
			fake := &interactiontest.Fake{}
			dispatchPress(t, fake, componentInteraction(data, "g", author, nil))
			calls := fake.Calls()
			if len(calls) != 1 || calls[0].Op != "Reply" || calls[0].Visibility != interaction.Private || calls[0].Response.Content != msgFeedsRemoved {
				t.Errorf("want one private %q, got %+v", msgFeedsRemoved, calls)
			}
		})
	}
}
