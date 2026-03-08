package roll

import (
	"context"
	"math/rand"
	"strconv"
	"strings"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/discord"
	"ReZeroTsu/internal/logger"

	"github.com/bwmarrin/discordgo"
)

const embedTitleRoll = "Roll"

// rollChoices are dice options for autocomplete (d2..d100).
var rollChoices = []int{2, 4, 6, 8, 10, 12, 20, 100}

func init() {
	commands.Add(&commands.Command{
		Name:       "roll",
		Desc:       "Rolls a number from 1 to 100. Specify a positive number to change the range.",
		Permission: commands.PermEveryone,
		Module:     "general",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:         discordgo.ApplicationCommandOptionInteger,
				Name:         "number",
				Description:  "A positive number that specifies the range from 1 to the number (default 100).",
				Required:     false,
				Autocomplete: true,
			},
		},
		Handler:            handleRoll,
		AutocompleteOption: autocompleteRollNumber,
	})
	commands.AddMessageCommand(&commands.MessageCommand{
		Name:       "roll",
		Aliases:    []string{"rol", "r"},
		Permission: commands.PermEveryone,
		Context:    commands.ContextDMOnly,
		MessageHandler: func(ctx context.Context, db *database.Client, s *discordgo.Session, m *discordgo.MessageCreate, args string, runtime commands.BotRuntime) {
			max := 100
			if f := strings.Fields(strings.TrimSpace(args)); len(f) > 0 {
				if n, err := strconv.Atoi(f[0]); err == nil && n >= 1 {
					max = n
				} else {
					emb := commands.NewEmbed(s, embedTitleRoll, "Invalid number. Please use a positive number.")
					_, _ = s.ChannelMessageSendEmbed(m.ChannelID, emb)
					return
				}
			}
			result := rand.Intn(max) + 1
			_, _ = s.ChannelMessageSend(m.ChannelID, "**Rolled:** "+strconv.Itoa(result))
		},
	})
}

func autocompleteRollNumber(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate, optionName, focusedValue string) []*discordgo.ApplicationCommandOptionChoice {
	focusedTrim := strings.TrimSpace(focusedValue)
	var choices []*discordgo.ApplicationCommandOptionChoice
	const maxChoices = 25
	for _, n := range rollChoices {
		if focusedTrim != "" && !strings.HasPrefix(strconv.Itoa(n), focusedTrim) {
			continue
		}
		choices = append(choices, &discordgo.ApplicationCommandOptionChoice{
			Name:  "d" + strconv.Itoa(n) + " (1–" + strconv.Itoa(n) + ")",
			Value: n,
		})
		if len(choices) >= maxChoices {
			break
		}
	}
	return choices
}

func handleRoll(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ApplicationCommandData()
	opt := commands.ParseOption(data.Options, "number")

	max := 100
	if opt != nil {
		max = int(opt.IntValue())
		if max < 1 {
			commands.RespondEmbed(s, i, embedTitleRoll, "Invalid number. Please use a positive number.")
			return
		}
	}

	result := rand.Intn(max) + 1
	msg := "**Rolled:** " + strconv.Itoa(result)
	if _, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &msg}); err != nil {
		args := append(discord.RESTAttrs(err), "guild_id", i.GuildID, "channel_id", i.ChannelID, "err", err)
		logger.For("commands").Error("roll edit failed", args...)
	}
}
