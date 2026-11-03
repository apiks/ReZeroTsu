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
	"ReZeroTsu/internal/interaction"

	"github.com/bwmarrin/discordgo"
)

const (
	embedTitleAnimeSubs  = "Anime subscriptions"
	maxUserSubscriptions = 100
	hintUnsub            = "Use /subscription remove to unsubscribe."
)

func init() {
	commands.RegisterPaginationRenderer("subs", RenderSubsPage)

	commands.AddMessageCommand(&commands.MessageCommand{
		Name:       "clearsubs",
		Aliases:    []string{"clearanimesubs", "subsclear", "unsuball"},
		Permission: commands.PermEveryone,
		Context:    commands.ContextDMOnly,
		MessageHandler: func(ctx context.Context, db *database.Client, s *discordgo.Session, m *discordgo.MessageCreate, args string, runtime commands.BotRuntime) {
			desc, logErr := clearsubsCore(ctx, db.AnimeSubs(), m.Author.ID)
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
			desc, logErr := subCore(ctx, db.AnimeSubs(), m.Author.ID, strings.TrimSpace(args))
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
			desc, logErr := unsubCore(ctx, db.AnimeSubs(), m.Author.ID, strings.TrimSpace(args))
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

// findShowIn returns the show whose display title matches input under anime_subs.MatchKey (case-insensitive and
// trimmed like the notifier matches names). Failing that, the first show whose romaji, English or native name
// matches; a title cut to Discord's choice limit also matches. It returns the zero value and false if none.
func findShowIn(shows []animeschedule.ShowEntry, input string) (animeschedule.ShowEntry, bool) {
	key := anime_subs.MatchKey(input)
	if key == "" {
		return animeschedule.ShowEntry{}, false
	}
	for _, show := range shows {
		if anime_subs.MatchKey(show.Name) == key || anime_subs.MatchKey(clip(show.Name)) == key {
			return show, true
		}
	}
	for _, show := range shows {
		for _, n := range show.AllNames() {
			if anime_subs.MatchKey(n) == key {
				return show, true
			}
		}
	}
	return animeschedule.ShowEntry{}, false
}

// findShowByName returns the airing ShowEntry matching name by any of its four names, or zero value and false.
func findShowByName(name string) (animeschedule.ShowEntry, bool) {
	return findShowIn(animeschedule.AiringShows(), name)
}

// choiceName is the autocomplete label: "Title (English)" when the English name differs from the title, else the
// title, cut to Discord's limit.
func choiceName(e animeschedule.ShowEntry) string {
	name := e.Name
	if en := strings.TrimSpace(e.English); en != "" && anime_subs.MatchKey(en) != anime_subs.MatchKey(e.Name) {
		name = fmt.Sprintf("%s (%s)", e.Name, en)
	}
	return clip(name)
}

// autocompleteAdd offers this week's airing shows matched anywhere in any of their four names. A choice's value is
// the display title, which is what subscriptions are keyed by.
func autocompleteAdd(_ context.Context, _ *database.Client, _ *discordgo.Session, _ *discordgo.InteractionCreate, _, typed string) []*discordgo.ApplicationCommandOptionChoice {
	matched := commands.MatchAnywhere(animeschedule.AiringShows(), typed, animeschedule.ShowEntry.AllNames)
	out := make([]*discordgo.ApplicationCommandOptionChoice, 0, len(matched))
	for _, e := range matched {
		out = append(out, &discordgo.ApplicationCommandOptionChoice{Name: choiceName(e), Value: clip(e.Name)})
	}
	return out
}

// subsStore is the narrow persistence surface of the subscription commands. It deliberately has no
// whole-document rewrite: every change is a single-statement operation.
type subsStore interface {
	GetByID(ctx context.Context, id string) (*anime_subs.AnimeSubs, error)
	AddShowIfAbsent(ctx context.Context, id string, sub *anime_subs.ShowSub) (bool, error)
	RemoveShows(ctx context.Context, id string, showNames []string) error
	ClearShows(ctx context.Context, id string) error
}

// showLookup resolves user input to an airing show; replaced in tests.
var showLookup = findShowByName

func subCore(ctx context.Context, store subsStore, uid, animeInput string) (responseDesc string, logErr error) {
	animeInput = strings.TrimSpace(animeInput)
	if animeInput == "" {
		return "Please provide an anime name (exact romaji from /schedule or AnimeSchedule.net).", nil
	}
	show, ok := showLookup(animeInput)
	if !ok {
		return "That is not a valid airing show name. Use the exact romaji title from `/schedule` or AnimeSchedule.net.", nil
	}
	showName := show.Name

	doc, err := store.GetByID(ctx, uid)
	if err != nil {
		return "Could not load your subscriptions.", err
	}
	if doc != nil {
		for _, sub := range doc.Shows {
			if sub != nil && anime_subs.MatchKey(sub.Show) == anime_subs.MatchKey(showName) {
				return fmt.Sprintf("You are already subscribed to `%s`.", showName), nil
			}
		}
	}
	if doc != nil && len(doc.Shows) >= maxUserSubscriptions {
		return fmt.Sprintf("You've reached the maximum number of subscriptions (%d). Unsubscribe from some shows to add more.", maxUserSubscriptions), nil
	}

	now := time.Now().UTC()
	var airedToday animeschedule.ShowEntry
	hasAiredToday := false
	if weekday := int(now.Weekday()); weekday >= 0 && weekday <= 6 {
		airedToday, hasAiredToday = airedTodayEntry(animeschedule.GetDayShows(weekday, true), showName, now.Unix())
	}

	newSub := &anime_subs.ShowSub{Show: showName, Notified: hasAiredToday, Guild: false}
	if hasAiredToday {
		newSub.LastNotifiedAirUnix = airedToday.AirTimeUnix
	}
	added, err := store.AddShowIfAbsent(ctx, uid, newSub)
	if err != nil {
		return "Could not save subscription.", err
	}
	if !added {
		return fmt.Sprintf("You are already subscribed to `%s`.", showName), nil
	}
	return fmt.Sprintf("Success! You have subscribed to DM notifications for `%s`.", showName), nil
}

func unsubCore(ctx context.Context, store subsStore, uid, animeInput string) (responseDesc string, logErr error) {
	animeInput = strings.TrimSpace(animeInput)
	if animeInput == "" {
		return "Please provide an anime name to unsubscribe from.", nil
	}
	doc, err := store.GetByID(ctx, uid)
	if err != nil {
		return "Could not load your subscriptions.", err
	}
	if doc == nil || len(doc.Shows) == 0 {
		return fmt.Sprintf("You are not subscribed to `%s`.", animeInput), nil
	}

	// Resolve the stored titles (exact case) the input names, then remove exactly those.
	var stored []string
	for _, sub := range doc.Shows {
		if sub != nil && anime_subs.MatchKey(sub.Show) == anime_subs.MatchKey(animeInput) {
			stored = append(stored, sub.Show)
		}
	}
	if len(stored) == 0 {
		return fmt.Sprintf("You are not subscribed to `%s`.", animeInput), nil
	}

	if err := store.RemoveShows(ctx, uid, stored); err != nil {
		return "Could not update subscriptions.", err
	}
	return fmt.Sprintf("Success! You have unsubscribed from `%s`.", animeInput), nil
}

func clearsubsCore(ctx context.Context, store subsStore, uid string) (responseDesc string, logErr error) {
	doc, err := store.GetByID(ctx, uid)
	if err != nil {
		return "Could not load your subscriptions.", err
	}
	if doc == nil || len(doc.Shows) == 0 {
		return "You have no active anime subscriptions.", nil
	}
	if err := store.ClearShows(ctx, uid); err != nil {
		return "Could not clear subscriptions.", err
	}
	return "Success! All your anime subscriptions have been cleared.", nil
}

// RenderSubsPage renders one page of anime subscriptions for pagination.
func RenderSubsPage(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate, page, total int, authorID string) commands.Page {
	if authorID == "" {
		return commands.Page{Refusal: "Could not load subscriptions."}
	}
	doc, err := db.AnimeSubs().GetByID(ctx, authorID)
	if err != nil {
		return commands.Page{Refusal: "Could not load your subscriptions."}
	}
	if doc == nil || len(doc.Shows) == 0 {
		return commands.Page{Refusal: "You have no active anime subscriptions. Run **/subs** again."}
	}
	var list []*anime_subs.ShowSub
	for _, sub := range doc.Shows {
		if sub != nil {
			list = append(list, sub)
		}
	}
	if len(list) == 0 {
		return commands.Page{Refusal: "You have no active anime subscriptions. Run **/subs** again."}
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
	return commands.Page{Response: interaction.Response{Embeds: []*discordgo.MessageEmbed{emb}, Components: components}}
}

// airedTodayEntry returns today's entry for the named show if it has already aired at nowUnix. Names are matched
// with anime_subs.MatchKey like the notifier does, and the entry is today's own, so the seeded air time is today's
// even for a show the schedule lists on several weekdays.
func airedTodayEntry(todayShows []animeschedule.ShowEntry, showName string, nowUnix int64) (animeschedule.ShowEntry, bool) {
	key := anime_subs.MatchKey(showName)
	for _, e := range todayShows {
		if anime_subs.MatchKey(e.Name) == key && e.AirTimeUnix <= nowUnix {
			return e, true
		}
	}
	return animeschedule.ShowEntry{}, false
}
