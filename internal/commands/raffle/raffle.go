package raffle

import (
	"context"
	"math/rand"
	"sort"
	"strings"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/database/guilds"
	"ReZeroTsu/internal/discord"
	"ReZeroTsu/internal/logger"

	"github.com/bwmarrin/discordgo"
)

const (
	embedTitleRaffle = "Raffle"
)

func init() {
	commands.Add(&commands.Command{
		Visibility: commands.Public,
		Response:   commands.Deferred,
		Name:       "remove-raffle",
		Desc:       "Removes a raffle.",
		Permission: commands.PermMod,
		Context:    commands.ContextGuildOnly,
		Module:     "raffles",
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionString, Name: "raffle", Description: "The name of the raffle to remove.", Required: true, Autocomplete: true},
		},
		Handler:            handleRemoveRaffle,
		AutocompleteOption: autocompleteRaffleName,
	})
	commands.Add(&commands.Command{
		Visibility: commands.Public,
		Response:   commands.Deferred,
		Name:       "pick-raffle-winner",
		Desc:       "Picks a random winner from those in a raffle.",
		Permission: commands.PermMod,
		Context:    commands.ContextGuildOnly,
		Module:     "raffles",
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionString, Name: "raffle", Description: "The name of the raffle to pick a winner from.", Required: true, Autocomplete: true},
		},
		Handler:            handlePickRaffleWinner,
		AutocompleteOption: autocompleteRaffleName,
	})
}

func autocompleteRaffleName(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate, optionName, focusedValue string) []*discordgo.ApplicationCommandOptionChoice {
	if i.GuildID == "" {
		return nil
	}
	raffles, err := db.Guilds().GetRaffles(ctx, i.GuildID)
	if err != nil || len(raffles) == 0 {
		return nil
	}
	var names []string
	for _, r := range raffles {
		if r == nil || r.Name == "" {
			continue
		}
		names = append(names, r.Name)
	}
	sort.Strings(names)
	names = commands.MatchAnywhere(names, focusedValue, func(n string) []string { return []string{n} })
	choices := make([]*discordgo.ApplicationCommandOptionChoice, len(names))
	for i, name := range names {
		choices[i] = &discordgo.ApplicationCommandOptionChoice{Name: name, Value: name}
	}
	return choices
}

func findRaffleByName(raffles []*guilds.Raffle, name string) *guilds.Raffle {
	for _, r := range raffles {
		if r != nil && strings.EqualFold(r.Name, name) {
			return r
		}
	}
	return nil
}

func participantContains(ids []string, userID string) bool {
	for _, id := range ids {
		if id == userID {
			return true
		}
	}
	return false
}

func handleRemoveRaffle(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.GuildID == "" {
		commands.RespondEmbed(s, i, embedTitleRaffle, "This command can only be used in a server.")
		return
	}
	data := i.ApplicationCommandData()
	opt := commands.ParseOption(data.Options, "raffle")
	if opt == nil || opt.StringValue() == "" {
		commands.RespondEmbed(s, i, embedTitleRaffle, "Please provide a raffle name.")
		return
	}
	raffleName := strings.TrimSpace(opt.StringValue())

	raffles, err := db.Guilds().GetRaffles(ctx, i.GuildID)
	if err != nil {
		logger.For("commands").Error("raffle GetRaffles failed", "guild_id", i.GuildID, "err", err)
		commands.RespondEmbed(s, i, embedTitleRaffle, "Could not load raffles.")
		return
	}
	raffle := findRaffleByName(raffles, raffleName)
	if raffle == nil {
		commands.RespondEmbed(s, i, embedTitleRaffle, "No such raffle.")
		return
	}
	if err := db.Guilds().DeleteRaffle(ctx, i.GuildID, raffle.Name); err != nil {
		logger.For("commands").Error("raffle DeleteRaffle failed", "guild_id", i.GuildID, "err", err)
		commands.RespondEmbed(s, i, embedTitleRaffle, "Could not remove raffle.")
		return
	}
	content := "Success! Removed raffle `" + raffle.Name + "`."
	if _, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &content}); err != nil {
		args := append(discord.RESTAttrs(err), "guild_id", i.GuildID, "channel_id", i.ChannelID, "err", err)
		logger.For("commands").Error("raffle edit failed", args...)
	}
}

func handlePickRaffleWinner(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.GuildID == "" {
		commands.RespondEmbed(s, i, embedTitleRaffle, "This command can only be used in a server.")
		return
	}
	data := i.ApplicationCommandData()
	opt := commands.ParseOption(data.Options, "raffle")
	if opt == nil || opt.StringValue() == "" {
		commands.RespondEmbed(s, i, embedTitleRaffle, "Please provide a raffle name.")
		return
	}
	raffleName := strings.TrimSpace(opt.StringValue())

	raffles, err := db.Guilds().GetRaffles(ctx, i.GuildID)
	if err != nil {
		logger.For("commands").Error("raffle GetRaffles failed", "guild_id", i.GuildID, "err", err)
		commands.RespondEmbed(s, i, embedTitleRaffle, "Could not load raffles.")
		return
	}
	raffle := findRaffleByName(raffles, raffleName)
	if raffle == nil {
		commands.RespondEmbed(s, i, embedTitleRaffle, "No such raffle.")
		return
	}
	if len(raffle.ParticipantIDs) == 0 {
		commands.RespondEmbed(s, i, embedTitleRaffle, "There is nobody to pick to win in that raffle.")
		return
	}
	winnerID := raffle.ParticipantIDs[rand.Intn(len(raffle.ParticipantIDs))]
	content := "**" + raffle.Name + "** winner is <@" + winnerID + ">! Congratulations!"
	if _, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &content}); err != nil {
		args := append(discord.RESTAttrs(err), "guild_id", i.GuildID, "channel_id", i.ChannelID, "err", err)
		logger.For("commands").Error("raffle edit failed", args...)
	}
}
