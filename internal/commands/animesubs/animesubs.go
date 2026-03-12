package animesubs

import (
	"context"
	"fmt"
	"strings"
	"time"

	"ReZeroTsu/internal/animeschedule"
	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/database/anime_subs"
	"ReZeroTsu/internal/discord"

	"github.com/bwmarrin/discordgo"
)

const (
	embedTitleAnimeSubs  = "Anime subscriptions"
	maxUserSubscriptions = 100
	hintUnsub            = "Use /unsub <anime> to unsubscribe."
)

func init() {
	commands.Add(&commands.Command{
		Name:       "sub",
		Desc:       "Subscribe to DM notifications when a new episode of an anime is released (subbed if possible).",
		Permission: commands.PermEveryone,
		Context:    commands.ContextBoth,
		Module:     "schedule",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "anime",
				Description: "The romaji title of an ongoing anime from AnimeSchedule.net or /schedule",
				Required:    true,
			},
		},
		Handler: handleSub,
	})
	commands.Add(&commands.Command{
		Name:       "unsub",
		Desc:       "Stop getting DM notifications for an anime's new episodes.",
		Permission: commands.PermEveryone,
		Context:    commands.ContextBoth,
		Module:     "schedule",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "anime",
				Description: "The anime to unsubscribe from.",
				Required:    true,
			},
		},
		Handler: handleUnsub,
	})
	commands.Add(&commands.Command{
		Name:       "clearsubs",
		Desc:       "Clear all your anime episode notifications.",
		Permission: commands.PermEveryone,
		Context:    commands.ContextBoth,
		Module:     "schedule",
		Handler:    handleClearsubs,
	})
	commands.Add(&commands.Command{
		Name:       "subs",
		Desc:       "List which anime you get new episode notifications for.",
		Permission: commands.PermEveryone,
		Context:    commands.ContextBoth,
		Module:     "schedule",
		Handler:    handleSubs,
	})
	commands.RegisterPaginationRenderer("subs", RenderSubsPage)

	commands.AddMessageCommand(&commands.MessageCommand{
		Name:       "clearsubs",
		Aliases:    []string{"clearanimesubs", "subsclear", "unsuball"},
		Permission: commands.PermEveryone,
		Context:    commands.ContextDMOnly,
		MessageHandler: func(ctx context.Context, db *database.Client, s *discordgo.Session, m *discordgo.MessageCreate, args string, runtime commands.BotRuntime) {
			desc, logErr := clearsubsCore(ctx, db, m.Author.ID)
			if logErr != nil {
				commands.LogToBotLog(ctx, db, s, m.GuildID, fmt.Sprintf("[animesubs] %v", logErr))
			}
			emb := commands.NewEmbed(s, embedTitleAnimeSubs, desc)
			_, _ = s.ChannelMessageSendEmbed(m.ChannelID, emb)
		},
	})
	commands.AddMessageCommand(&commands.MessageCommand{
		Name:       "sub",
		Aliases:    []string{"subscribe", "subs", "animesub", "subanime", "addsub"},
		Permission: commands.PermEveryone,
		Context:    commands.ContextDMOnly,
		MessageHandler: func(ctx context.Context, db *database.Client, s *discordgo.Session, m *discordgo.MessageCreate, args string, runtime commands.BotRuntime) {
			desc, logErr := subCore(ctx, db, m.Author.ID, strings.TrimSpace(args))
			if logErr != nil {
				commands.LogToBotLog(ctx, db, s, m.GuildID, fmt.Sprintf("[animesubs] %v", logErr))
			}
			emb := commands.NewEmbed(s, embedTitleAnimeSubs, desc)
			_, _ = s.ChannelMessageSendEmbed(m.ChannelID, emb)
		},
	})
	commands.AddMessageCommand(&commands.MessageCommand{
		Name:       "unsub",
		Aliases:    []string{"unsubscribe", "unsubs", "unanimesub", "unsubanime", "removesub", "killsub", "stopsub"},
		Permission: commands.PermEveryone,
		Context:    commands.ContextDMOnly,
		MessageHandler: func(ctx context.Context, db *database.Client, s *discordgo.Session, m *discordgo.MessageCreate, args string, runtime commands.BotRuntime) {
			desc, logErr := unsubCore(ctx, db, m.Author.ID, strings.TrimSpace(args))
			if logErr != nil {
				commands.LogToBotLog(ctx, db, s, m.GuildID, fmt.Sprintf("[animesubs] %v", logErr))
			}
			emb := commands.NewEmbed(s, embedTitleAnimeSubs, desc)
			_, _ = s.ChannelMessageSendEmbed(m.ChannelID, emb)
		},
	})
	commands.AddMessageCommand(&commands.MessageCommand{
		Name:       "subs",
		Aliases:    []string{"subscriptions", "animesubs", "showsubs", "showsubscriptions", "viewsubs", "viewsubscriptions"},
		Permission: commands.PermEveryone,
		Context:    commands.ContextDMOnly,
		MessageHandler: func(ctx context.Context, db *database.Client, s *discordgo.Session, m *discordgo.MessageCreate, args string, runtime commands.BotRuntime) {
			uid := m.Author.ID
			doc, err := db.AnimeSubs().GetByID(ctx, uid)
			if err != nil {
				commands.LogToBotLog(ctx, db, s, m.GuildID, fmt.Sprintf("[animesubs] GetByID %s: %v", uid, err))
				emb := commands.NewEmbed(s, embedTitleAnimeSubs, "Could not load your subscriptions.")
				_, _ = s.ChannelMessageSendEmbed(m.ChannelID, emb)
				return
			}
			if doc == nil || len(doc.Shows) == 0 {
				emb := commands.NewEmbed(s, embedTitleAnimeSubs, "You have no active anime subscriptions.")
				_, _ = s.ChannelMessageSendEmbed(m.ChannelID, emb)
				return
			}
			list := docToSubList(doc)
			if len(list) == 0 {
				emb := commands.NewEmbed(s, embedTitleAnimeSubs, "You have no active anime subscriptions.")
				_, _ = s.ChannelMessageSendEmbed(m.ChannelID, emb)
				return
			}
			desc, components := subsFirstPageContent(list, uid)
			emb := commands.NewEmbed(s, embedTitleAnimeSubs, desc, hintUnsub)
			_, _ = s.ChannelMessageSendComplex(m.ChannelID, &discordgo.MessageSend{
				Embeds:     []*discordgo.MessageEmbed{emb},
				Components: components,
			})
		},
	})
}

func subsDescriptionForPage(list []*anime_subs.ShowSub, start, page, totalPages, n int) string {
	var b strings.Builder
	end := min(start+discord.MaxEmbedFields, len(list))
	for j := start; j < end; j++ {
		if list[j] == nil {
			continue
		}
		fmt.Fprintf(&b, "%d. %s\n", j+1, list[j].Show)
	}
	endShow := min(start+discord.MaxEmbedFields, n)
	b.WriteString("\n")
	if totalPages > 1 {
		fmt.Fprintf(&b, "Page %d of %d. Showing %d–%d of %d.\n", page+1, totalPages, start+1, endShow, n)
	}
	s := b.String()
	if len(s) > discord.MaxEmbedDescriptionLength {
		return s[:discord.MaxEmbedDescriptionLength]
	}
	return s
}

func docToSubList(doc *anime_subs.AnimeSubs) []*anime_subs.ShowSub {
	var list []*anime_subs.ShowSub
	for _, sub := range doc.Shows {
		if sub != nil {
			list = append(list, sub)
		}
	}
	return list
}

func subsFirstPageContent(list []*anime_subs.ShowSub, uid string) (desc string, components []discordgo.MessageComponent) {
	if len(list) == 0 {
		return "", nil
	}
	n := len(list)
	totalPages := max((n+discord.MaxEmbedFields-1)/discord.MaxEmbedFields, 1)
	desc = subsDescriptionForPage(list, 0, 0, totalPages, n)
	if n > discord.MaxEmbedFields {
		components = commands.PaginationComponents("subs", 0, n, uid)
	}
	return desc, components
}

// findShowByName returns the first ShowEntry matching name (case-insensitive), or zero value and false if none.
func findShowByName(name string) (animeschedule.ShowEntry, bool) {
	nameLower := strings.ToLower(strings.TrimSpace(name))
	if nameLower == "" {
		return animeschedule.ShowEntry{}, false
	}
	for day := 0; day <= 6; day++ {
		shows := animeschedule.GetDayShows(day, true)
		for _, show := range shows {
			if strings.ToLower(show.Name) == nameLower {
				return show, true
			}
		}
	}
	return animeschedule.ShowEntry{}, false
}

func subCore(ctx context.Context, db *database.Client, uid, animeInput string) (responseDesc string, logErr error) {
	animeInput = strings.TrimSpace(animeInput)
	if animeInput == "" {
		return "Please provide an anime name (exact romaji from /schedule or AnimeSchedule.net).", nil
	}
	show, ok := findShowByName(animeInput)
	if !ok {
		return "That is not a valid airing show name. Use the exact romaji title from `/schedule` or AnimeSchedule.net.", nil
	}
	showName := show.Name

	doc, err := db.AnimeSubs().GetByID(ctx, uid)
	if err != nil {
		return "Could not load your subscriptions.", err
	}
	if doc == nil {
		doc = &anime_subs.AnimeSubs{ID: uid, IsGuild: false, Shows: nil}
	}
	for _, sub := range doc.Shows {
		if sub != nil && strings.EqualFold(sub.Show, showName) {
			return fmt.Sprintf("You are already subscribed to `%s`.", showName), nil
		}
	}
	if len(doc.Shows) >= maxUserSubscriptions {
		return fmt.Sprintf("You've reached the maximum number of subscriptions (%d). Unsubscribe from some shows to add more.", maxUserSubscriptions), nil
	}

	now := time.Now().UTC()
	hasAiredToday := false
	if weekday := int(now.Weekday()); weekday >= 0 && weekday <= 6 {
		todayShows := animeschedule.GetDayShows(weekday, true)
		for _, e := range todayShows {
			if strings.EqualFold(e.Name, showName) && e.AirTimeUnix <= now.Unix() {
				hasAiredToday = true
				break
			}
		}
	}

	newSub := &anime_subs.ShowSub{Show: showName, Notified: hasAiredToday, Guild: false}
	if hasAiredToday {
		newSub.LastNotifiedAirUnix = show.AirTimeUnix
	}
	doc.Shows = append(doc.Shows, newSub)
	if err := db.AnimeSubs().Set(ctx, uid, false, doc.Shows); err != nil {
		return "Could not save subscription.", err
	}
	return fmt.Sprintf("Success! You have subscribed to DM notifications for `%s`.", showName), nil
}

func handleSub(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ApplicationCommandData()
	opt := commands.ParseOption(data.Options, "anime")
	animeInput := ""
	if opt != nil {
		animeInput = opt.StringValue()
	}
	desc, logErr := subCore(ctx, db, commands.UserID(i), animeInput)
	if logErr != nil {
		commands.LogToBotLog(ctx, db, s, i.GuildID, fmt.Sprintf("[animesubs] %v", logErr))
	}
	commands.RespondEmbed(s, i, embedTitleAnimeSubs, desc)
}

func unsubCore(ctx context.Context, db *database.Client, uid, animeInput string) (responseDesc string, logErr error) {
	animeInput = strings.TrimSpace(animeInput)
	if animeInput == "" {
		return "Please provide an anime name to unsubscribe from.", nil
	}
	doc, err := db.AnimeSubs().GetByID(ctx, uid)
	if err != nil {
		return "Could not load your subscriptions.", err
	}
	if doc == nil || len(doc.Shows) == 0 {
		return fmt.Sprintf("You are not subscribed to `%s`.", animeInput), nil
	}

	var newShows []*anime_subs.ShowSub
	found := false
	for _, sub := range doc.Shows {
		if sub == nil {
			continue
		}
		if strings.EqualFold(sub.Show, animeInput) {
			found = true
			continue
		}
		newShows = append(newShows, sub)
	}
	if !found {
		return fmt.Sprintf("You are not subscribed to `%s`.", animeInput), nil
	}

	if err := db.AnimeSubs().Set(ctx, uid, false, newShows); err != nil {
		return "Could not update subscriptions.", err
	}
	return fmt.Sprintf("Success! You have unsubscribed from `%s`.", animeInput), nil
}

func handleUnsub(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ApplicationCommandData()
	opt := commands.ParseOption(data.Options, "anime")
	animeInput := ""
	if opt != nil {
		animeInput = opt.StringValue()
	}
	desc, logErr := unsubCore(ctx, db, commands.UserID(i), animeInput)
	if logErr != nil {
		commands.LogToBotLog(ctx, db, s, i.GuildID, fmt.Sprintf("[animesubs] %v", logErr))
	}
	commands.RespondEmbed(s, i, embedTitleAnimeSubs, desc)
}

func clearsubsCore(ctx context.Context, db *database.Client, uid string) (responseDesc string, logErr error) {
	doc, err := db.AnimeSubs().GetByID(ctx, uid)
	if err != nil {
		return "Could not load your subscriptions.", err
	}
	if doc == nil || len(doc.Shows) == 0 {
		return "You have no active anime subscriptions.", nil
	}
	if err := db.AnimeSubs().Set(ctx, uid, false, nil); err != nil {
		return "Could not clear subscriptions.", err
	}
	return "Success! All your anime subscriptions have been cleared.", nil
}

func handleClearsubs(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate) {
	desc, logErr := clearsubsCore(ctx, db, commands.UserID(i))
	if logErr != nil {
		commands.LogToBotLog(ctx, db, s, i.GuildID, fmt.Sprintf("[animesubs] %v", logErr))
	}
	commands.RespondEmbed(s, i, embedTitleAnimeSubs, desc)
}

func handleSubs(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate) {
	uid := commands.UserID(i)
	doc, err := db.AnimeSubs().GetByID(ctx, uid)
	if err != nil {
		commands.RespondEmbed(s, i, embedTitleAnimeSubs, "Could not load your subscriptions.")
		return
	}
	if doc == nil || len(doc.Shows) == 0 {
		commands.RespondEmbed(s, i, embedTitleAnimeSubs, "You have no active anime subscriptions.")
		return
	}
	list := docToSubList(doc)
	if len(list) == 0 {
		commands.RespondEmbed(s, i, embedTitleAnimeSubs, "You have no active anime subscriptions.")
		return
	}
	desc, components := subsFirstPageContent(list, uid)
	if len(components) > 0 {
		commands.RespondEmbedWithFieldsAndComponents(s, i, embedTitleAnimeSubs, desc, nil, components, hintUnsub)
	} else {
		commands.RespondEmbed(s, i, embedTitleAnimeSubs, desc, hintUnsub)
	}
}

// RenderSubsPage renders one page of anime subscriptions for pagination.
func RenderSubsPage(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate, page, total int, authorID string) {
	if authorID == "" {
		_ = commands.RespondEphemeral(s, i, "Could not load subscriptions.")
		return
	}
	doc, err := db.AnimeSubs().GetByID(ctx, authorID)
	if err != nil {
		_ = commands.RespondEphemeral(s, i, "Could not load your subscriptions.")
		return
	}
	if doc == nil || len(doc.Shows) == 0 {
		_ = commands.RespondEphemeral(s, i, "You have no active anime subscriptions. Run **/subs** again.")
		return
	}
	var list []*anime_subs.ShowSub
	for _, sub := range doc.Shows {
		if sub != nil {
			list = append(list, sub)
		}
	}
	if len(list) == 0 {
		_ = commands.RespondEphemeral(s, i, "You have no active anime subscriptions. Run **/subs** again.")
		return
	}
	n := len(list)
	totalPages := max((n+discord.MaxEmbedFields-1)/discord.MaxEmbedFields, 1)
	if page >= totalPages {
		page = totalPages - 1
	}
	if page < 0 {
		page = 0
	}
	start := page * discord.MaxEmbedFields
	desc := subsDescriptionForPage(list, start, page, totalPages, n)
	components := commands.PaginationComponents("subs", page, n, authorID)
	emb := commands.NewEmbed(s, embedTitleAnimeSubs, desc, hintUnsub)
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Embeds:     []*discordgo.MessageEmbed{emb},
			Components: components,
		},
	})
}
