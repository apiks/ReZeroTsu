package roll

import (
	"context"
	"math/rand"
	"strconv"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/interaction"

	"github.com/bwmarrin/discordgo"
)

// rollChoices are dice options for autocomplete (d2..d100).
var rollChoices = []int{2, 4, 6, 8, 10, 12, 20, 100}

func init() {
	commands.Add(&commands.Command{
		Visibility: commands.Public,
		Response:   commands.Direct,
		Name:       "roll",
		Desc:       "Rolls a number from 1 to 100. Specify a positive number to change the range.",
		Permission: commands.PermEveryone,
		Module:     "general",
		Installs:   commands.BothInstalls(),
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:         discordgo.ApplicationCommandOptionInteger,
				Name:         "number",
				Description:  "A positive number that specifies the range from 1 to the number (default 100).",
				Required:     false,
				Autocomplete: true,
			},
		},
		Run:                run,
		AutocompleteOption: autocompleteRollNumber,
	})
}

func autocompleteRollNumber(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate, optionName, focusedValue string) []*discordgo.ApplicationCommandOptionChoice {
	var choices []*discordgo.ApplicationCommandOptionChoice
	for _, n := range commands.MatchAnywhere(rollChoices, focusedValue, func(n int) []string { return []string{strconv.Itoa(n), "d" + strconv.Itoa(n)} }) {
		choices = append(choices, &discordgo.ApplicationCommandOptionChoice{
			Name:  "d" + strconv.Itoa(n) + " (1–" + strconv.Itoa(n) + ")",
			Value: n,
		})
	}
	return choices
}

func run(ic *interaction.Context) error {
	max := 100
	if opt := commands.ParseOption(ic.Command.Options, "number"); opt != nil {
		max = int(opt.IntValue())
		if max < 1 {
			return ic.Refuse("Invalid number. Please use a positive number.")
		}
	}
	return ic.Reply(interaction.Response{Content: "**Rolled:** " + strconv.Itoa(rand.Intn(max)+1)})
}
