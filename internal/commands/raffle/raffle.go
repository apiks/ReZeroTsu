package raffle

import (
	"context"
	"math/rand"
	"sort"
	"strconv"
	"strings"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/database/guilds"
	"ReZeroTsu/internal/discord"
	"ReZeroTsu/internal/interaction"
	"ReZeroTsu/internal/logger"

	"github.com/bwmarrin/discordgo"
)

const (
	embedTitleRaffle = "Raffle"
	hintShowRaffles  = "Press Enter on a raffle's message to enter, use /remove-raffle to delete a raffle."
	hintMyRaffles    = "Press Leave on a raffle's message to leave a raffle."
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
	commands.Add(&commands.Command{
		Visibility: commands.Public,
		Response:   commands.Deferred,
		Name:       "show-raffles",
		Desc:       "Lists existing raffles.",
		Permission: commands.PermEveryone,
		Context:    commands.ContextGuildOnly,
		Module:     "raffles",
		Options:    nil,
		Handler:    handleRaffles,
	})
	commands.Add(&commands.Command{
		Visibility: commands.Public,
		Response:   commands.Deferred,
		Name:       "my-raffles",
		Desc:       "Lists raffles you have joined.",
		Permission: commands.PermEveryone,
		Context:    commands.ContextGuildOnly,
		Module:     "raffles",
		Options:    nil,
		Handler:    handleMyRaffles,
	})
	commands.RegisterPaginationRenderer("show-raffles", RenderShowRafflesPage)
	commands.RegisterPaginationRenderer("my-raffles", RenderMyRafflesPage)
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

func raffleFieldsForPage(raffles []*guilds.Raffle, start int, showParticipants bool) []*discordgo.MessageEmbedField {
	fields := make([]*discordgo.MessageEmbedField, 0, discord.MaxEmbedFields)
	end := min(start+discord.MaxEmbedFields, len(raffles))
	for j := start; j < end; j++ {
		r := raffles[j]
		if r == nil {
			continue
		}
		name := r.Name
		if len(name) > discord.MaxEmbedFieldNameLength {
			name = name[:discord.MaxEmbedFieldNameLength-1] + "…"
		}
		value := "—"
		if showParticipants {
			value = strconv.Itoa(len(r.ParticipantIDs)) + " participants"
			if len(value) > discord.MaxEmbedFieldValueLength {
				value = value[:discord.MaxEmbedFieldValueLength-1] + "…"
			}
		}
		fields = append(fields, &discordgo.MessageEmbedField{Name: name, Value: value})
	}
	return fields
}

func handleRaffles(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.GuildID == "" {
		commands.RespondEmbed(s, i, embedTitleRaffle, "This command can only be used in a server.")
		return
	}
	raffles, err := db.Guilds().GetRaffles(ctx, i.GuildID)
	if err != nil {
		logger.For("commands").Error("raffle GetRaffles failed", "guild_id", i.GuildID, "err", err)
		commands.RespondEmbed(s, i, embedTitleRaffle, "Could not load raffles.")
		return
	}
	if len(raffles) == 0 {
		commands.RespondEmbed(s, i, embedTitleRaffle, "There are no raffles.")
		return
	}
	fields := raffleFieldsForPage(raffles, 0, true)
	description := ""
	var components []discordgo.MessageComponent
	if len(raffles) > discord.MaxEmbedFields {
		totalPages := (len(raffles) + discord.MaxEmbedFields - 1) / discord.MaxEmbedFields
		description = "Page 1 of " + strconv.Itoa(totalPages) + ". Showing 1–" + strconv.Itoa(discord.MaxEmbedFields) + " of " + strconv.Itoa(len(raffles)) + "."
		components = commands.PaginationComponents("show-raffles", 0, len(raffles), commands.UserID(i))
	}
	if len(components) > 0 {
		commands.RespondEmbedWithFieldsAndComponents(s, i, embedTitleRaffle, description, fields, components, hintShowRaffles)
	} else {
		commands.RespondEmbedWithFields(s, i, embedTitleRaffle, description, fields, hintShowRaffles)
	}
}

func handleMyRaffles(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.GuildID == "" || i.Member == nil {
		commands.RespondEmbed(s, i, embedTitleRaffle, "This command can only be used in a server.")
		return
	}
	userID := i.Member.User.ID
	raffles, err := db.Guilds().GetRaffles(ctx, i.GuildID)
	if err != nil {
		logger.For("commands").Error("raffle GetRaffles failed", "guild_id", i.GuildID, "err", err)
		commands.RespondEmbed(s, i, embedTitleRaffle, "Could not load raffles.")
		return
	}
	var mine []*guilds.Raffle
	for _, r := range raffles {
		if r != nil && participantContains(r.ParticipantIDs, userID) {
			mine = append(mine, r)
		}
	}
	if len(mine) == 0 {
		commands.RespondEmbed(s, i, embedTitleRaffle, "You're not in any raffles.")
		return
	}
	fields := raffleFieldsForPage(mine, 0, false)
	description := ""
	var components []discordgo.MessageComponent
	if len(mine) > discord.MaxEmbedFields {
		totalPages := (len(mine) + discord.MaxEmbedFields - 1) / discord.MaxEmbedFields
		description = "Page 1 of " + strconv.Itoa(totalPages) + ". Showing 1–" + strconv.Itoa(discord.MaxEmbedFields) + " of " + strconv.Itoa(len(mine)) + "."
		components = commands.PaginationComponents("my-raffles", 0, len(mine), userID)
	}
	if len(components) > 0 {
		commands.RespondEmbedWithFieldsAndComponents(s, i, embedTitleRaffle, description, fields, components, hintMyRaffles)
	} else {
		commands.RespondEmbedWithFields(s, i, embedTitleRaffle, description, fields, hintMyRaffles)
	}
}

// RenderShowRafflesPage renders one page of show-raffles for pagination.
func RenderShowRafflesPage(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate, page, total int, authorID string) commands.Page {
	raffles, err := db.Guilds().GetRaffles(ctx, i.GuildID)
	if err != nil {
		logger.For("commands").Error("raffle GetRaffles page failed", "guild_id", i.GuildID, "err", err)
		return commands.Page{Refusal: "Could not load raffles."}
	}
	n := len(raffles)
	if n == 0 {
		return commands.Page{Refusal: "There are no raffles."}
	}
	totalPages := (n + discord.MaxEmbedFields - 1) / discord.MaxEmbedFields
	if page >= totalPages {
		page = totalPages - 1
	}
	start := page * discord.MaxEmbedFields
	fields := raffleFieldsForPage(raffles, start, true)
	description := "Page " + strconv.Itoa(page+1) + " of " + strconv.Itoa(totalPages) + ". Showing " + strconv.Itoa(start+1) + "–" + strconv.Itoa(min(start+len(fields), n)) + " of " + strconv.Itoa(n) + "."
	components := commands.PaginationComponents("show-raffles", page, n, authorID)
	emb := commands.NewEmbedWithFields(s, embedTitleRaffle, description, fields, hintShowRaffles)
	return commands.Page{Response: interaction.Response{Embeds: []*discordgo.MessageEmbed{emb}, Components: components}}
}

// RenderMyRafflesPage renders one page of my-raffles for pagination.
func RenderMyRafflesPage(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate, page, total int, authorID string) commands.Page {
	raffles, err := db.Guilds().GetRaffles(ctx, i.GuildID)
	if err != nil {
		logger.For("commands").Error("raffle GetRaffles page failed", "guild_id", i.GuildID, "err", err)
		return commands.Page{Refusal: "Could not load raffles."}
	}
	// The list belongs to the message's author, so a private copy for someone else shows the author's raffles.
	userID := authorID
	var mine []*guilds.Raffle
	for _, r := range raffles {
		if r != nil && participantContains(r.ParticipantIDs, userID) {
			mine = append(mine, r)
		}
	}
	n := len(mine)
	if n == 0 {
		return commands.Page{Refusal: "You're not in any raffles."}
	}
	totalPages := (n + discord.MaxEmbedFields - 1) / discord.MaxEmbedFields
	if page >= totalPages {
		page = totalPages - 1
	}
	start := page * discord.MaxEmbedFields
	fields := raffleFieldsForPage(mine, start, false)
	description := "Page " + strconv.Itoa(page+1) + " of " + strconv.Itoa(totalPages) + ". Showing " + strconv.Itoa(start+1) + "–" + strconv.Itoa(min(start+len(fields), n)) + " of " + strconv.Itoa(n) + "."
	components := commands.PaginationComponents("my-raffles", page, n, userID)
	emb := commands.NewEmbedWithFields(s, embedTitleRaffle, description, fields, hintMyRaffles)
	return commands.Page{Response: interaction.Response{Embeds: []*discordgo.MessageEmbed{emb}, Components: components}}
}
