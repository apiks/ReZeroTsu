package animesubs

import (
	"fmt"

	"ReZeroTsu/internal/animeschedule"
	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/interaction"

	"github.com/bwmarrin/discordgo"
)

// optName is the option of /subscription name.
const optName = "name"

// nameOption offers the four preferred names, each stored in its lower-case form.
var nameOption = &discordgo.ApplicationCommandOption{
	Type:        discordgo.ApplicationCommandOptionString,
	Name:        optName,
	Description: "Title (default), Romaji, English or Native; leave it out to see your current choice",
	Choices: func() []*discordgo.ApplicationCommandOptionChoice {
		var out []*discordgo.ApplicationCommandOptionChoice
		for _, p := range []animeschedule.PreferredName{
			animeschedule.PreferTitle, animeschedule.PreferRomaji, animeschedule.PreferEnglish, animeschedule.PreferNative,
		} {
			out = append(out, &discordgo.ApplicationCommandOptionChoice{Name: p.Title(), Value: p.String()})
		}
		return out
	}(),
}

// runName saves the member's preferred name, or states it when no choice was given. Setting it needs no
// subscription, and a member's preferred name is never read from a server.
func runName(ic *interaction.Context) error {
	store := storeOf(ic.DB)
	if opt := commands.ParseOption(ic.Command.Options, optName); opt != nil {
		// Discord offers only the four choices; anything else reads as Title, as a stored value does.
		name, _ := animeschedule.ParsePreferredName(opt.StringValue())
		if err := store.SetPreferredName(ic.Context(), ic.Invoker.ID, name.String()); err != nil {
			return finish(ic, "Could not save your preferred name.", err)
		}
		return answer(ic, nameSentence("Shows are now called by their", name))
	}
	doc, err := store.GetByID(ic.Context(), ic.Invoker.ID)
	if err != nil {
		return finish(ic, "Could not load your preferred name.", err)
	}
	return answer(ic, nameSentence("Shows are called by their", animeschedule.MemberPreferredName(doc)))
}

// nameSentence ends lead with the preferred name and its fallback: "their English name, else their title.", or
// "their title." for Title.
func nameSentence(lead string, name animeschedule.PreferredName) string {
	if name == animeschedule.PreferTitle {
		return lead + " title."
	}
	return fmt.Sprintf("%s %s name, else their title.", lead, name.Title())
}
