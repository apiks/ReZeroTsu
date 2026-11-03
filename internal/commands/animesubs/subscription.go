package animesubs

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/database/anime_subs"
	"ReZeroTsu/internal/discord"
	"ReZeroTsu/internal/interaction"
	"ReZeroTsu/internal/logger"

	"github.com/bwmarrin/discordgo"
)

const (
	// pageKey names the subscription list's page renderer. The DM prefix list keeps its own "subs" renderer until
	// the prefix commands retire (ticket 3.7).
	pageKey = "subscription"
	// pickerPrefix starts the custom ID of the Remove picker: sub-rm:<authorID>:<page>.
	pickerPrefix = "sub-rm:"

	msgNoSubscriptions  = "You have no active anime subscriptions."
	msgOnlyOwnerRemoves = "Only the member whose list this is can remove from it."
	msgStalePicker      = "Could not read that choice. Run /subscription list again."
	maxChoiceLength     = 100
)

// storeOf is the subscription store behind the commands; tests replace it.
var storeOf = func(db *database.Client) subsStore { return db.AnimeSubs() }

func init() {
	commands.Add(&commands.Command{
		Name:       "subscription",
		Desc:       "Get a DM when a new episode of an anime airs.",
		Visibility: commands.Private,
		Response:   commands.Deferred,
		Permission: commands.PermEveryone,
		Context:    commands.ContextBoth,
		Module:     "schedule",
		Installs:   commands.BothInstalls(),
		Subcommands: []*commands.Subcommand{
			{
				Name: "add",
				Desc: "Subscribe to DMs for an airing anime.",
				Options: []*discordgo.ApplicationCommandOption{{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "anime",
					Description: "The romaji title of an ongoing anime from AnimeSchedule.net or /schedule",
					Required:    true,
				}},
				Run: runAdd,
			},
			{
				Name: "remove",
				Desc: "Stop getting DMs for an anime.",
				Options: []*discordgo.ApplicationCommandOption{{
					Type:         discordgo.ApplicationCommandOptionString,
					Name:         "anime",
					Description:  "One of your subscriptions",
					Required:     true,
					Autocomplete: true,
				}},
				Run:                runRemove,
				AutocompleteOption: autocompleteOwn,
			},
			{Name: "list", Desc: "Show your subscriptions.", Run: runList},
			{Name: "clear", Desc: "Remove all your subscriptions.", Run: runClear},
		},
	})
	// Restricted: a list belongs to its author, so another member's press is refused, not given a copy.
	commands.RegisterPaginationRenderer(pageKey, RenderSubscriptionPage)
	commands.AddComponent(pickerPrefix, handleRemovePicker)
}

// answer replaces the deferred placeholder with a titled embed.
func answer(ic *interaction.Context, desc string) error {
	return ic.EditReply(interaction.Response{Embeds: []*discordgo.MessageEmbed{commands.NewEmbed(nil, embedTitleAnimeSubs, desc)}})
}

// finish shows a core's message and logs the error it carries. A failure is logged here, with the invoker's
// context, because the new form has no session to post to the server's bot log.
func finish(ic *interaction.Context, desc string, logErr error) error {
	if logErr != nil {
		logger.For("animesubs").Error("subscription command failed", "guild_id", ic.GuildID, "err", logErr)
	}
	return answer(ic, desc)
}

func animeArg(ic *interaction.Context) string {
	if opt := commands.ParseOption(ic.Command.Options, "anime"); opt != nil {
		return opt.StringValue()
	}
	return ""
}

// runAdd keeps today's rules through subCore: the show must be airing, one entry per show by match key, the
// subscription limit, and seeding from today's aired entry.
func runAdd(ic *interaction.Context) error {
	desc, logErr := subCore(ic.Context(), storeOf(ic.DB), ic.Invoker.ID, animeArg(ic))
	return finish(ic, desc, logErr)
}

func runRemove(ic *interaction.Context) error {
	desc, logErr := unsubCore(ic.Context(), storeOf(ic.DB), ic.Invoker.ID, animeArg(ic))
	return finish(ic, desc, logErr)
}

func runClear(ic *interaction.Context) error {
	desc, logErr := clearsubsCore(ic.Context(), storeOf(ic.DB), ic.Invoker.ID)
	return finish(ic, desc, logErr)
}

func runList(ic *interaction.Context) error {
	list, err := loadList(ic.Context(), storeOf(ic.DB), ic.Invoker.ID)
	if err != nil {
		return finish(ic, "Could not load your subscriptions.", err)
	}
	if len(list) == 0 {
		return answer(ic, msgNoSubscriptions)
	}
	return ic.EditReply(renderPage(list, 0, ic.Invoker.ID))
}

func loadList(ctx context.Context, store subsStore, uid string) ([]*anime_subs.ShowSub, error) {
	doc, err := store.GetByID(ctx, uid)
	if err != nil || doc == nil {
		return nil, err
	}
	return docToSubList(doc), nil
}

// autocompleteOwn offers only the invoker's own subscriptions, matched anywhere in the title.
func autocompleteOwn(ctx context.Context, db *database.Client, _ *discordgo.Session, i *discordgo.InteractionCreate, _, typed string) []*discordgo.ApplicationCommandOptionChoice {
	list, err := loadList(ctx, storeOf(db), commands.UserID(i))
	if err != nil {
		logger.For("animesubs").Error("subscription autocomplete failed", "err", err)
		return nil
	}
	matched := commands.MatchAnywhere(list, typed, func(s *anime_subs.ShowSub) []string { return []string{s.Show} })
	out := make([]*discordgo.ApplicationCommandOptionChoice, 0, len(matched))
	for _, s := range matched {
		out = append(out, &discordgo.ApplicationCommandOptionChoice{Name: clip(s.Show), Value: clip(s.Show)})
	}
	return out
}

// clip cuts text to Discord's 100-character limit for choice names, option labels and option values.
func clip(s string) string {
	r := []rune(s)
	if len(r) <= maxChoiceLength {
		return s
	}
	return string(r[:maxChoiceLength])
}

// renderPage builds one page of the list: the embed, Previous/Next when there is more than one page, and the
// Remove picker for the entries on this page. page is clamped to the list.
func renderPage(list []*anime_subs.ShowSub, page int, authorID string) interaction.Response {
	n := len(list)
	totalPages := max((n+discord.MaxEmbedFields-1)/discord.MaxEmbedFields, 1)
	page = min(max(page, 0), totalPages-1)
	start := page * discord.MaxEmbedFields
	end := min(start+discord.MaxEmbedFields, n)

	var components []discordgo.MessageComponent
	if n > discord.MaxEmbedFields {
		components = append(components, commands.PaginationComponents(pageKey, page, n, authorID)...)
	}
	components = append(components, removePicker(list[start:end], page, authorID))
	emb := commands.NewEmbed(nil, embedTitleAnimeSubs, subsDescriptionForPage(list, start, page, totalPages, n), "Pick a show below to unsubscribe from it.")
	return interaction.Response{Embeds: []*discordgo.MessageEmbed{emb}, Components: components}
}

func removePicker(entries []*anime_subs.ShowSub, page int, authorID string) discordgo.MessageComponent {
	var options []discordgo.SelectMenuOption
	seen := make(map[string]bool, len(entries))
	for _, s := range entries {
		v := clip(s.Show)
		if seen[v] {
			continue
		}
		seen[v] = true
		options = append(options, discordgo.SelectMenuOption{Label: v, Value: v})
	}
	return discordgo.ActionsRow{Components: []discordgo.MessageComponent{discordgo.SelectMenu{
		MenuType:    discordgo.StringSelectMenu,
		CustomID:    fmt.Sprintf("%s%s:%d", pickerPrefix, authorID, page),
		Placeholder: "Remove a subscription",
		Options:     options,
	}}}
}

// RenderSubscriptionPage renders one page of the invoker's subscription list for pagination.
func RenderSubscriptionPage(ctx context.Context, db *database.Client, _ *discordgo.Session, _ *discordgo.InteractionCreate, page, _ int, authorID string) commands.Page {
	if authorID == "" {
		return commands.Page{Refusal: "Could not load subscriptions."}
	}
	list, err := loadList(ctx, storeOf(db), authorID)
	if err != nil {
		logger.For("animesubs").Error("subscription page failed", "err", err)
		return commands.Page{Refusal: "Could not load your subscriptions."}
	}
	if len(list) == 0 {
		return commands.Page{Refusal: msgNoSubscriptions + " Run /subscription list again."}
	}
	return commands.Page{Response: renderPage(list, page, authorID)}
}

// handleRemovePicker removes the chosen subscription and re-renders the page. Guard home: only the member whose
// list it is may remove from it, checked before the store is touched, whatever message the picker sits on.
func handleRemovePicker(ic *interaction.Context, _ *discordgo.Session) error {
	parts := strings.SplitN(strings.TrimPrefix(ic.CustomID, pickerPrefix), ":", 2)
	if len(parts) != 2 || len(ic.Values) != 1 {
		return ic.Refuse(msgStalePicker)
	}
	authorID := parts[0]
	page, err := strconv.Atoi(parts[1])
	if err != nil {
		return ic.Refuse(msgStalePicker)
	}
	if ic.Invoker.ID != authorID {
		return ic.Refuse(msgOnlyOwnerRemoves)
	}

	store := storeOf(ic.DB)
	list, err := loadList(ic.Context(), store, authorID)
	if err != nil {
		return finishRefusal(ic, "Could not load your subscriptions.", err)
	}
	var stored []string
	for _, s := range list {
		if clip(s.Show) == ic.Values[0] {
			stored = append(stored, s.Show)
		}
	}
	if len(stored) > 0 {
		if err := store.RemoveShows(ic.Context(), authorID, stored); err != nil {
			return finishRefusal(ic, "Could not update subscriptions.", err)
		}
		if list, err = loadList(ic.Context(), store, authorID); err != nil {
			return finishRefusal(ic, "Could not load your subscriptions.", err)
		}
	}
	if len(list) == 0 {
		// Explicit empty components: an omitted list would leave the old picker on the message.
		return ic.UpdateMessage(interaction.Response{
			Embeds:     []*discordgo.MessageEmbed{commands.NewEmbed(nil, embedTitleAnimeSubs, msgNoSubscriptions)},
			Components: []discordgo.MessageComponent{},
		})
	}
	return ic.UpdateMessage(renderPage(list, page, authorID))
}

func finishRefusal(ic *interaction.Context, text string, err error) error {
	logger.For("animesubs").Error("subscription picker failed", "guild_id", ic.GuildID, "err", err)
	return ic.Refuse(text)
}
