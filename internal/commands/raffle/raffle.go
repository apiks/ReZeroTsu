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
	"ReZeroTsu/internal/logger"

	"github.com/bwmarrin/discordgo"
)

const (
	embedTitleRaffle = "Raffle"
	hintShowRaffles  = "Use /join-raffle to enter, /remove-raffle to delete a raffle."
	hintMyRaffles    = "Use /leave-raffle to leave a raffle."
)

func init() {
	commands.Add(&commands.Command{
		Name:       "join-raffle",
		Desc:       "Enters you in a raffle.",
		Permission: commands.PermEveryone,
		Context:    commands.ContextGuildOnly,
		Module:     "raffles",
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionString, Name: "raffle", Description: "The name of the raffle you want to enter.", Required: true, Autocomplete: true},
		},
		Handler:            handleJoinRaffle,
		AutocompleteOption: autocompleteRaffleName,
	})
	commands.Add(&commands.Command{
		Name:       "leave-raffle",
		Desc:       "Removes you from a raffle.",
		Permission: commands.PermEveryone,
		Context:    commands.ContextGuildOnly,
		Module:     "raffles",
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionString, Name: "raffle", Description: "The name of the raffle you want to leave.", Required: true, Autocomplete: true},
		},
		Handler:            handleLeaveRaffle,
		AutocompleteOption: autocompleteRaffleName,
	})
	commands.Add(&commands.Command{
		Name:       "create-raffle",
		Desc:       "Creates a raffle.",
		Permission: commands.PermMod,
		Context:    commands.ContextGuildOnly,
		Module:     "raffles",
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionString, Name: "raffle", Description: "The name of the raffle.", Required: true},
			{Type: discordgo.ApplicationCommandOptionBoolean, Name: "react", Description: "Whether users can react with the slot emoji to join/leave.", Required: false},
		},
		Handler: handleCreateRaffle,
	})
	commands.Add(&commands.Command{
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
		Name:       "show-raffles",
		Desc:       "Lists existing raffles.",
		Permission: commands.PermEveryone,
		Context:    commands.ContextGuildOnly,
		Module:     "raffles",
		Options:    nil,
		Handler:    handleRaffles,
	})
	commands.Add(&commands.Command{
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
	focusedLower := strings.ToLower(strings.TrimSpace(focusedValue))
	var names []string
	for _, r := range raffles {
		if r == nil || r.Name == "" {
			continue
		}
		if focusedLower != "" && !strings.HasPrefix(strings.ToLower(r.Name), focusedLower) {
			continue
		}
		names = append(names, r.Name)
	}
	sort.Strings(names)
	const maxChoices = 25
	if len(names) > maxChoices {
		names = names[:maxChoices]
	}
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

func handleJoinRaffle(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.GuildID == "" || i.Member == nil {
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
	userID := i.Member.User.ID

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
	if participantContains(raffle.ParticipantIDs, userID) {
		commands.RespondEmbed(s, i, embedTitleRaffle, "You've already joined that raffle!")
		return
	}
	if err := db.Guilds().UpdateRaffleParticipant(ctx, i.GuildID, raffle.Name, userID, false); err != nil {
		logger.For("commands").Error("raffle UpdateRaffleParticipant failed", "guild_id", i.GuildID, "err", err)
		commands.RespondEmbed(s, i, embedTitleRaffle, "Could not join raffle.")
		return
	}
	content := "Success! You have entered raffle `" + raffle.Name + "`."
	if _, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &content}); err != nil {
		args := append(discord.RESTAttrs(err), "guild_id", i.GuildID, "channel_id", i.ChannelID, "err", err)
		logger.For("commands").Error("raffle edit failed", args...)
	}
}

func handleLeaveRaffle(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.GuildID == "" || i.Member == nil {
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
	userID := i.Member.User.ID

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
	if !participantContains(raffle.ParticipantIDs, userID) {
		commands.RespondEmbed(s, i, embedTitleRaffle, "You haven't joined that raffle!")
		return
	}
	if err := db.Guilds().UpdateRaffleParticipant(ctx, i.GuildID, raffle.Name, userID, true); err != nil {
		logger.For("commands").Error("raffle UpdateRaffleParticipant failed", "guild_id", i.GuildID, "err", err)
		commands.RespondEmbed(s, i, embedTitleRaffle, "Could not leave raffle.")
		return
	}
	content := "Success! You have left raffle `" + raffle.Name + "`."
	if _, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &content}); err != nil {
		args := append(discord.RESTAttrs(err), "guild_id", i.GuildID, "channel_id", i.ChannelID, "err", err)
		logger.For("commands").Error("raffle edit failed", args...)
	}
}

func handleCreateRaffle(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.GuildID == "" {
		commands.RespondEmbed(s, i, embedTitleRaffle, "This command can only be used in a server.")
		return
	}
	data := i.ApplicationCommandData()
	optRaffle := commands.ParseOption(data.Options, "raffle")
	if optRaffle == nil || optRaffle.StringValue() == "" {
		commands.RespondEmbed(s, i, embedTitleRaffle, "Please provide a raffle name.")
		return
	}
	raffleName := strings.TrimSpace(optRaffle.StringValue())
	reactJoin := false
	if optReact := commands.ParseOption(data.Options, "react"); optReact != nil {
		reactJoin = optReact.BoolValue()
	}

	raffles, err := db.Guilds().GetRaffles(ctx, i.GuildID)
	if err != nil {
		logger.For("commands").Error("raffle GetRaffles failed", "guild_id", i.GuildID, "err", err)
		commands.RespondEmbed(s, i, embedTitleRaffle, "Could not load raffles.")
		return
	}
	if findRaffleByName(raffles, raffleName) != nil {
		commands.RespondEmbed(s, i, embedTitleRaffle, "A raffle with this name already exists.")
		return
	}

	if reactJoin {
		msg, err := s.ChannelMessageSend(i.ChannelID, "Raffle `"+raffleName+"` is now active.")
		if err != nil {
			logger.For("commands").Error("raffle ChannelMessageSend failed", "guild_id", i.GuildID, "err", err)
			commands.RespondEmbed(s, i, embedTitleRaffle, "Could not send raffle message.")
			return
		}
		if err := s.MessageReactionAdd(msg.ChannelID, msg.ID, "🎰"); err != nil {
			args := append(discord.RESTAttrs(err), "guild_id", i.GuildID, "channel_id", msg.ChannelID, "err", err)
			logger.For("commands").Error("raffle MessageReactionAdd failed", args...)
		}
		raffle := &guilds.Raffle{
			Name:           raffleName,
			ParticipantIDs: nil,
			ReactMessageID: msg.ID,
		}
		if err := db.Guilds().SetRaffle(ctx, i.GuildID, raffle); err != nil {
			logger.For("commands").Error("raffle SetRaffle failed", "guild_id", i.GuildID, "err", err)
			commands.RespondEmbed(s, i, embedTitleRaffle, "Could not save raffle.")
			return
		}
		content := "Raffle `" + raffleName + "` is now active. React with 🎰 to join or leave."
		if _, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &content}); err != nil {
			args := append(discord.RESTAttrs(err), "guild_id", i.GuildID, "channel_id", i.ChannelID, "err", err)
			logger.For("commands").Error("raffle edit failed", args...)
		}
		return
	}

	raffle := &guilds.Raffle{
		Name:           raffleName,
		ParticipantIDs: nil,
		ReactMessageID: "",
	}
	if err := db.Guilds().SetRaffle(ctx, i.GuildID, raffle); err != nil {
		logger.For("commands").Error("raffle SetRaffle failed", "guild_id", i.GuildID, "err", err)
		commands.RespondEmbed(s, i, embedTitleRaffle, "Could not save raffle.")
		return
	}
	content := "Raffle `" + raffleName + "` is now active. Use `/join-raffle` to join."
	if _, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &content}); err != nil {
		args := append(discord.RESTAttrs(err), "guild_id", i.GuildID, "channel_id", i.ChannelID, "err", err)
		logger.For("commands").Error("raffle edit failed", args...)
	}
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
		components = commands.PaginationComponents("show-raffles", 0, len(raffles), "")
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
func RenderShowRafflesPage(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate, page, total int, authorID string) {
	raffles, err := db.Guilds().GetRaffles(ctx, i.GuildID)
	if err != nil {
		logger.For("commands").Error("raffle GetRaffles page failed", "guild_id", i.GuildID, "err", err)
		_ = commands.RespondEphemeral(s, i, "Could not load raffles.")
		return
	}
	n := len(raffles)
	if n == 0 {
		_ = commands.RespondEphemeral(s, i, "There are no raffles.")
		return
	}
	totalPages := (n + discord.MaxEmbedFields - 1) / discord.MaxEmbedFields
	if page >= totalPages {
		page = totalPages - 1
	}
	start := page * discord.MaxEmbedFields
	fields := raffleFieldsForPage(raffles, start, true)
	description := "Page " + strconv.Itoa(page+1) + " of " + strconv.Itoa(totalPages) + ". Showing " + strconv.Itoa(start+1) + "–" + strconv.Itoa(min(start+len(fields), n)) + " of " + strconv.Itoa(n) + "."
	components := commands.PaginationComponents("show-raffles", page, n, "")
	emb := commands.NewEmbedWithFields(s, embedTitleRaffle, description, fields, hintShowRaffles)
	err = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Embeds:     []*discordgo.MessageEmbed{emb},
			Components: components,
		},
	})
	if err != nil {
		args := append(discord.RESTAttrs(err), "guild_id", i.GuildID, "channel_id", i.ChannelID, "err", err)
		logger.For("commands").Error("raffle RenderShowRafflesPage respond failed", args...)
	}
}

// RenderMyRafflesPage renders one page of my-raffles for pagination.
func RenderMyRafflesPage(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate, page, total int, authorID string) {
	raffles, err := db.Guilds().GetRaffles(ctx, i.GuildID)
	if err != nil {
		logger.For("commands").Error("raffle GetRaffles page failed", "guild_id", i.GuildID, "err", err)
		_ = commands.RespondEphemeral(s, i, "Could not load raffles.")
		return
	}
	userID := commands.UserID(i)
	var mine []*guilds.Raffle
	for _, r := range raffles {
		if r != nil && participantContains(r.ParticipantIDs, userID) {
			mine = append(mine, r)
		}
	}
	n := len(mine)
	if n == 0 {
		_ = commands.RespondEphemeral(s, i, "You're not in any raffles.")
		return
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
	err = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Embeds:     []*discordgo.MessageEmbed{emb},
			Components: components,
		},
	})
	if err != nil {
		args := append(discord.RESTAttrs(err), "guild_id", i.GuildID, "channel_id", i.ChannelID, "err", err)
		logger.For("commands").Error("raffle RenderMyRafflesPage respond failed", args...)
	}
}
