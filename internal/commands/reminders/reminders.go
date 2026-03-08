package reminders

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/database/reminders"
	"ReZeroTsu/internal/discord"

	"github.com/bwmarrin/discordgo"
)

const (
	embedTitleReminders = "Reminders"
	hintRemoveRemind    = "Use /remove-remind with the ID to delete a reminder."
)

// timeHintChoices are autocomplete hints for /remind time option.
var timeHintChoices = []string{
	"1 minute", "5 minutes", "15 minutes", "30 minutes",
	"1 hour", "1 day", "1 week",
	"2 weeks 1 day 12 hours 30 minutes",
}

func init() {
	commands.Add(&commands.Command{
		Name:       "remind",
		Desc:       "Set a reminder; you'll get a DM at the given time.",
		Permission: commands.PermEveryone,
		Module:     "general",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:         discordgo.ApplicationCommandOptionString,
				Name:         "time",
				Description:  "Amount of time to wait. Format: 2w1d12h30m (weeks, days, hours, minutes).",
				Required:     true,
				Autocomplete: true,
			},
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "message",
				Description: "The message you want to be sent.",
				Required:    true,
			},
		},
		Handler:            handleRemind,
		AutocompleteOption: autocompleteRemindTime,
	})
	commands.Add(&commands.Command{
		Name:       "remove-remind",
		Desc:       "Remove a reminder by ID.",
		Permission: commands.PermEveryone,
		Module:     "general",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:         discordgo.ApplicationCommandOptionInteger,
				Name:         "id",
				Description:  "The reminder ID (from /reminders).",
				Required:     true,
				Autocomplete: true,
			},
		},
		Handler:            handleRemoveRemind,
		AutocompleteOption: autocompleteRemindID,
	})
	commands.Add(&commands.Command{
		Name:       "reminders",
		Desc:       "List your reminders.",
		Permission: commands.PermEveryone,
		Module:     "general",
		Handler:    handleReminders,
	})
	commands.RegisterPaginationRenderer("reminders", RenderRemindersPage)

	commands.AddMessageCommand(&commands.MessageCommand{
		Name:       "remind",
		Aliases:    []string{"setremind", "addremind", "remindme", "remind-me", "add-remind"},
		Permission: commands.PermEveryone,
		Context:    commands.ContextDMOnly,
		MessageHandler: func(ctx context.Context, db *database.Client, s *discordgo.Session, m *discordgo.MessageCreate, args string, runtime commands.BotRuntime) {
			parts := strings.SplitN(strings.TrimSpace(args), " ", 2)
			timeStr := ""
			msg := ""
			if len(parts) >= 1 {
				timeStr = strings.TrimSpace(parts[0])
			}
			if len(parts) >= 2 {
				msg = strings.TrimSpace(parts[1])
			}
			desc, logErr := addRemindCore(ctx, db, m.Author.ID, m.GuildID, m.ChannelID, timeStr, msg)
			if logErr != nil {
				commands.LogToBotLog(ctx, db, s, m.GuildID, fmt.Sprintf("[reminders] %v", logErr))
			}
			emb := commands.NewEmbed(s, embedTitleReminders, desc)
			_, _ = s.ChannelMessageSendEmbed(m.ChannelID, emb)
		},
	})
	commands.AddMessageCommand(&commands.MessageCommand{
		Name:       "remove-remind",
		Aliases:    []string{"removeremindme", "deleteremind", "deleteremindme", "killremind", "stopremind", "removeremind"},
		Permission: commands.PermEveryone,
		Context:    commands.ContextDMOnly,
		MessageHandler: func(ctx context.Context, db *database.Client, s *discordgo.Session, m *discordgo.MessageCreate, args string, runtime commands.BotRuntime) {
			id := 0
			if f := strings.Fields(strings.TrimSpace(args)); len(f) > 0 {
				id, _ = strconv.Atoi(f[0])
			}
			desc, logErr := removeRemindCore(ctx, db, m.Author.ID, id)
			if logErr != nil {
				commands.LogToBotLog(ctx, db, s, m.GuildID, fmt.Sprintf("[reminders] %v", logErr))
			}
			emb := commands.NewEmbed(s, embedTitleReminders, desc)
			_, _ = s.ChannelMessageSendEmbed(m.ChannelID, emb)
		},
	})
	commands.AddMessageCommand(&commands.MessageCommand{
		Name:       "reminders",
		Aliases:    []string{"viewremindmes", "viewremindme", "viewremind", "viewreminds", "remindmes"},
		Permission: commands.PermEveryone,
		Context:    commands.ContextDMOnly,
		MessageHandler: func(ctx context.Context, db *database.Client, s *discordgo.Session, m *discordgo.MessageCreate, args string, runtime commands.BotRuntime) {
			uid := m.Author.ID
			slice, err := db.Reminders().GetByID(ctx, uid)
			if err != nil {
				commands.LogToBotLog(ctx, db, s, m.GuildID, fmt.Sprintf("[reminders] GetByID %s: %v", uid, err))
				emb := commands.NewEmbed(s, embedTitleReminders, "Could not load your reminders.")
				_, _ = s.ChannelMessageSendEmbed(m.ChannelID, emb)
				return
			}
			if slice == nil || len(slice.Reminders) == 0 {
				emb := commands.NewEmbed(s, embedTitleReminders, "No saved reminders for you found.")
				_, _ = s.ChannelMessageSendEmbed(m.ChannelID, emb)
				return
			}
			desc, fields, components := buildRemindersListResponse(slice, 0, uid)
			emb := commands.NewEmbedWithFields(s, embedTitleReminders, desc, fields, hintRemoveRemind)
			params := &discordgo.MessageSend{Embeds: []*discordgo.MessageEmbed{emb}}
			if len(components) > 0 {
				params.Components = components
			}
			_, _ = s.ChannelMessageSendComplex(m.ChannelID, params)
		},
	})
}

func autocompleteRemindTime(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate, optionName, focusedValue string) []*discordgo.ApplicationCommandOptionChoice {
	focusedLower := strings.ToLower(strings.TrimSpace(focusedValue))
	var choices []*discordgo.ApplicationCommandOptionChoice
	for _, hint := range timeHintChoices {
		if focusedLower != "" && !strings.HasPrefix(strings.ToLower(hint), focusedLower) {
			continue
		}
		choices = append(choices, &discordgo.ApplicationCommandOptionChoice{Name: hint, Value: hint})
	}
	return choices
}

func autocompleteRemindID(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate, optionName, focusedValue string) []*discordgo.ApplicationCommandOptionChoice {
	uid := commands.UserID(i)
	slice, err := db.Reminders().GetByID(ctx, uid)
	if err != nil || slice == nil || len(slice.Reminders) == 0 {
		return nil
	}
	focusedTrim := strings.TrimSpace(focusedValue)
	var filtered []reminders.RemindMe
	for _, r := range slice.Reminders {
		idStr := strconv.Itoa(r.RemindID)
		if focusedTrim != "" && !strings.HasPrefix(idStr, focusedTrim) {
			continue
		}
		filtered = append(filtered, r)
	}
	sort.Slice(filtered, func(a, b int) bool { return filtered[a].RemindID < filtered[b].RemindID })
	const maxChoices = 25
	const maxNameLen = 100
	choices := make([]*discordgo.ApplicationCommandOptionChoice, 0, min(len(filtered), maxChoices))
	for j := 0; j < len(filtered) && j < maxChoices; j++ {
		r := filtered[j]
		msgShort := r.Message
		if len(msgShort) > 50 {
			msgShort = msgShort[:47] + "..."
		}
		name := fmt.Sprintf("%d: %s", r.RemindID, msgShort)
		if len(name) > maxNameLen {
			name = name[:maxNameLen-3] + "..."
		}
		choices = append(choices, &discordgo.ApplicationCommandOptionChoice{Name: name, Value: r.RemindID})
	}
	return choices
}

func addRemindCore(ctx context.Context, db *database.Client, uid, guildID, channelID, timeStr, msg string) (responseDesc string, logErr error) {
	if timeStr == "" || msg == "" {
		return "Please provide time and message.", nil
	}
	targetTime, perma, err := commands.ResolveTimeFromString(timeStr)
	if err != nil {
		return "Invalid time given.", nil
	}
	if perma {
		return "Cannot use that time. Please use another.", nil
	}
	slice, err := db.Reminders().GetByID(ctx, uid)
	if err != nil {
		return "Could not load your reminders.", err
	}
	if slice == nil {
		slice = &reminders.RemindMeSlice{
			ID:        uid,
			IsGuild:   guildID != "",
			Reminders: nil,
			Premium:   false,
		}
	}
	maxID := 0
	for _, r := range slice.Reminders {
		if r.RemindID > maxID {
			maxID = r.RemindID
		}
	}
	maxID++
	slice.Reminders = append(slice.Reminders, reminders.RemindMe{
		Message:        msg,
		Date:           targetTime,
		CommandChannel: channelID,
		RemindID:       maxID,
		CreatedAt:      time.Now(),
	})
	if err := db.Reminders().Save(ctx, uid, slice); err != nil {
		return "Could not save reminder.", err
	}
	return fmt.Sprintf("Success! You will be reminded of the message <t:%d:R>. Make sure your DMs are open.", targetTime.UTC().Unix()), nil
}

func handleRemind(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ApplicationCommandData()
	optTime := commands.ParseOption(data.Options, "time")
	optMessage := commands.ParseOption(data.Options, "message")
	timeStr := ""
	if optTime != nil {
		timeStr = optTime.StringValue()
	}
	msg := ""
	if optMessage != nil {
		msg = optMessage.StringValue()
	}
	desc, logErr := addRemindCore(ctx, db, commands.UserID(i), i.GuildID, i.ChannelID, timeStr, msg)
	if logErr != nil {
		commands.LogToBotLog(ctx, db, s, i.GuildID, fmt.Sprintf("[reminders] %v", logErr))
	}
	commands.RespondEmbed(s, i, embedTitleReminders, desc)
}

func removeRemindCore(ctx context.Context, db *database.Client, uid string, id int) (responseDesc string, logErr error) {
	if id <= 0 {
		return "Invalid ID.", nil
	}
	slice, err := db.Reminders().GetByID(ctx, uid)
	if err != nil {
		return "Could not load your reminders.", err
	}
	if slice == nil || len(slice.Reminders) == 0 {
		return "No saved reminders for you found.", nil
	}
	var remaining []reminders.RemindMe
	found := false
	for _, r := range slice.Reminders {
		if r.RemindID == id {
			found = true
			continue
		}
		remaining = append(remaining, r)
	}
	if !found {
		return "No reminder found with that ID.", nil
	}
	var toSave *reminders.RemindMeSlice
	if len(remaining) > 0 {
		toSave = &reminders.RemindMeSlice{
			ID:        slice.ID,
			IsGuild:   slice.IsGuild,
			Reminders: remaining,
			Premium:   slice.Premium,
		}
	}
	if err := db.Reminders().Save(ctx, uid, toSave); err != nil {
		return "Could not remove reminder.", err
	}
	return fmt.Sprintf("Success! Deleted reminder with ID `%d`.", id), nil
}

func handleRemoveRemind(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ApplicationCommandData()
	optID := commands.ParseOption(data.Options, "id")
	if optID == nil {
		commands.RespondEmbed(s, i, embedTitleReminders, "Please provide the reminder ID.")
		return
	}
	id := int(optID.IntValue())
	desc, logErr := removeRemindCore(ctx, db, commands.UserID(i), id)
	if logErr != nil {
		commands.LogToBotLog(ctx, db, s, i.GuildID, fmt.Sprintf("[reminders] %v", logErr))
	}
	commands.RespondEmbed(s, i, embedTitleReminders, desc)
}

func reminderFieldsForPage(reminders []reminders.RemindMe, start int) []*discordgo.MessageEmbedField {
	fields := make([]*discordgo.MessageEmbedField, 0, discord.MaxEmbedFields)
	end := min(start+discord.MaxEmbedFields, len(reminders))
	for j := start; j < end; j++ {
		r := reminders[j]
		dueTs := r.Date.UTC().Unix()
		createdLine := fmt.Sprintf("<t:%d:R>", r.CreatedAt.UTC().Unix())
		if r.CreatedAt.IsZero() {
			createdLine = "—"
		}
		msgShort := r.Message
		if len(msgShort) > 200 {
			msgShort = msgShort[:197] + "..."
		}
		value := fmt.Sprintf("**Message:** %s\n**Created:** %s\n**Due:** <t:%d:R>", msgShort, createdLine, dueTs)
		fields = append(fields, &discordgo.MessageEmbedField{
			Name:   fmt.Sprintf("ID %d", r.RemindID),
			Value:  value,
			Inline: false,
		})
	}
	return fields
}

func buildRemindersListResponse(slice *reminders.RemindMeSlice, page int, authorID string) (desc string, fields []*discordgo.MessageEmbedField, components []discordgo.MessageComponent) {
	n := len(slice.Reminders)
	if n == 0 {
		return "", nil, nil
	}
	totalPages := max((n+discord.MaxEmbedFields-1)/discord.MaxEmbedFields, 1)
	if page >= totalPages {
		page = totalPages - 1
	}
	start := page * discord.MaxEmbedFields
	fields = reminderFieldsForPage(slice.Reminders, start)
	endShow := min(start+len(fields), n)
	desc = fmt.Sprintf("Page %d of %d. Showing %d–%d of %d.", page+1, totalPages, start+1, endShow, n)
	if n > discord.MaxEmbedFields {
		components = commands.PaginationComponents("reminders", page, n, authorID)
	}
	return desc, fields, components
}

func handleReminders(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate) {
	uid := commands.UserID(i)
	slice, err := db.Reminders().GetByID(ctx, uid)
	if err != nil {
		commands.RespondEmbed(s, i, embedTitleReminders, "Could not load your reminders.")
		commands.LogToBotLog(ctx, db, s, i.GuildID, fmt.Sprintf("[reminders] GetByID %s: %v", uid, err))
		return
	}
	if slice == nil || len(slice.Reminders) == 0 {
		commands.RespondEmbed(s, i, embedTitleReminders, "No saved reminders for you found.")
		return
	}
	desc, fields, components := buildRemindersListResponse(slice, 0, uid)
	if len(components) > 0 {
		commands.RespondEmbedWithFieldsAndComponents(s, i, embedTitleReminders, desc, fields, components, hintRemoveRemind)
	} else {
		commands.RespondEmbedWithFields(s, i, embedTitleReminders, desc, fields, hintRemoveRemind)
	}
}

// RenderRemindersPage renders one page of reminders for pagination.
func RenderRemindersPage(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate, page, total int, authorID string) {
	if authorID == "" {
		_ = commands.RespondEphemeral(s, i, "Could not load reminders.")
		return
	}
	slice, err := db.Reminders().GetByID(ctx, authorID)
	if err != nil {
		commands.LogToBotLog(ctx, db, s, i.GuildID, fmt.Sprintf("[reminders] GetByID %s: %v", authorID, err))
		_ = commands.RespondEphemeral(s, i, "Could not load your reminders.")
		return
	}
	if slice == nil || len(slice.Reminders) == 0 {
		_ = commands.RespondEphemeral(s, i, "No saved reminders for you found.")
		return
	}
	n := len(slice.Reminders)
	totalPages := max((n+discord.MaxEmbedFields-1)/discord.MaxEmbedFields, 1)
	if page >= totalPages {
		page = totalPages - 1
	}
	start := page * discord.MaxEmbedFields
	fields := reminderFieldsForPage(slice.Reminders, start)
	endShow := min(start+len(fields), n)
	desc := fmt.Sprintf("Page %d of %d. Showing %d–%d of %d.", page+1, totalPages, start+1, endShow, n)
	components := commands.PaginationComponents("reminders", page, n, authorID)
	emb := commands.NewEmbedWithFields(s, embedTitleReminders, desc, fields, hintRemoveRemind)
	err = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Embeds:     []*discordgo.MessageEmbed{emb},
			Components: components,
		},
	})
	if err != nil {
		commands.LogToBotLog(ctx, db, s, i.GuildID, fmt.Sprintf("[reminders] RenderRemindersPage respond: %v", err))
	}
}
