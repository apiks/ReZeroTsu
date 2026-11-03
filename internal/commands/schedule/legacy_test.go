package schedule

import (
	"testing"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/interaction"
	"ReZeroTsu/internal/interaction/interactiontest"

	"github.com/bwmarrin/discordgo"
)

// A button on a schedule message posted before the day picker carries a pag:schedule: custom ID. Whoever presses
// it is told privately that the day is gone.
func TestPressOnAnOldPaginationScheduleMessageIsRefusedPrivatelyForAnyone(t *testing.T) {
	for _, user := range []string{"author", "stranger"} {
		fake := &interactiontest.Fake{}
		i := &discordgo.Interaction{Type: discordgo.InteractionMessageComponent, ChannelID: "chan", Message: publicMsg(), User: &discordgo.User{ID: user},
			Data: discordgo.MessageComponentInteractionData{CustomID: "pag:schedule:1:7:author", ComponentType: discordgo.ButtonComponent}}
		ic := interaction.NewContext(fake, i, (*database.Client)(nil), interaction.Private, interaction.Direct)
		if err := handleLegacyPress(ic, nil); err != nil {
			t.Fatal(err)
		}
		calls := fake.Calls()
		if len(calls) != 1 || calls[0].Visibility != interaction.Private || calls[0].Response.Content != msgDayNotLoaded {
			t.Errorf("%s: want the private note %q, got %+v", user, msgDayNotLoaded, calls)
		}
	}
}

// The handler owns the pag:schedule: prefix, which wins over the generic pag: handler by its length.
func TestOldScheduleButtonsPrefixIsRegistered(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("pag:schedule: is not registered, so old buttons fall to the generic pagination handler")
		}
	}()
	commands.AddComponent("pag:schedule:", handleLegacyPress)
}
