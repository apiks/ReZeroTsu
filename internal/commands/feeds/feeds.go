package feeds

import (
	"context"
	"slices"
	"sort"
	"strconv"
	"strings"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/database/guilds"
	"ReZeroTsu/internal/discord"
	"ReZeroTsu/internal/logger"
	"ReZeroTsu/internal/reddit"

	"github.com/bwmarrin/discordgo"
)

const (
	embedTitleReddit = "Reddit feeds"
	hintRemoveReddit = "Use /remove-reddit-feed to remove a feed."
)

var postTypeChoices = []string{"hot", "rising", "new"}

func init() {
	commands.Add(&commands.Command{
		Name:       "add-reddit-feed",
		Desc:       "Adds a Reddit RSS feed to a channel.",
		Permission: commands.PermMod,
		Context:    commands.ContextGuildOnly,
		Module:     "reddit",
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionChannel, Name: "channel", Description: "The channel to add the feed to.", Required: true, ChannelTypes: []discordgo.ChannelType{discordgo.ChannelTypeGuildText, discordgo.ChannelTypeGuildPublicThread, discordgo.ChannelTypeGuildPrivateThread}},
			{Type: discordgo.ApplicationCommandOptionString, Name: "subreddit", Description: "The subreddit (e.g. r/anime or anime).", Required: true},
			{Type: discordgo.ApplicationCommandOptionString, Name: "post-type", Description: "Feed sort: hot, rising, or new. Default: hot.", Required: false, Autocomplete: true},
			{Type: discordgo.ApplicationCommandOptionString, Name: "author", Description: "Filter to posts from this user (optional).", Required: false},
			{Type: discordgo.ApplicationCommandOptionString, Name: "title", Description: "Filter to posts starting with this title (optional).", Required: false},
			{Type: discordgo.ApplicationCommandOptionBoolean, Name: "pin", Description: "Auto-pin the latest post. Default: false.", Required: false},
		},
		Handler:            handleAddRedditFeed,
		AutocompleteOption: autocompletePostType,
	})
	commands.Add(&commands.Command{
		Name:       "remove-reddit-feed",
		Desc:       "Removes a Reddit RSS feed from a channel.",
		Permission: commands.PermMod,
		Context:    commands.ContextGuildOnly,
		Module:     "reddit",
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionChannel, Name: "channel", Description: "The channel to remove the feed from.", Required: true, ChannelTypes: []discordgo.ChannelType{discordgo.ChannelTypeGuildText, discordgo.ChannelTypeGuildPublicThread, discordgo.ChannelTypeGuildPrivateThread}, Autocomplete: true},
			{Type: discordgo.ApplicationCommandOptionString, Name: "subreddit", Description: "The subreddit to remove.", Required: true, Autocomplete: true},
			{Type: discordgo.ApplicationCommandOptionString, Name: "post-type", Description: "Post type: hot, rising, or new. Omit to remove all three.", Required: false, Autocomplete: true},
			{Type: discordgo.ApplicationCommandOptionString, Name: "author", Description: "Author filter of the feed (optional).", Required: false},
			{Type: discordgo.ApplicationCommandOptionString, Name: "title", Description: "Title filter of the feed (optional).", Required: false},
		},
		Handler:            handleRemoveRedditFeed,
		AutocompleteOption: autocompleteRemoveRedditFeed,
	})
	commands.Add(&commands.Command{
		Name:       "reddit-feeds",
		Desc:       "Lists all Reddit RSS feeds for this server.",
		Permission: commands.PermMod,
		Context:    commands.ContextGuildOnly,
		Module:     "reddit",
		Options:    nil,
		Handler:    handleRedditFeeds,
	})
	commands.RegisterPaginationRenderer("reddit-feeds", RenderRedditFeedsPage)
}

func normalizeSubreddit(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "/r/")
	s = strings.TrimPrefix(s, "r/")
	return strings.ToLower(s)
}

func normalizeAuthor(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "/u/")
	s = strings.TrimPrefix(s, "u/")
	return s
}

func autocompletePostType(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate, optionName, focusedValue string) []*discordgo.ApplicationCommandOptionChoice {
	focusedLower := strings.ToLower(strings.TrimSpace(focusedValue))
	var choices []*discordgo.ApplicationCommandOptionChoice
	for _, name := range postTypeChoices {
		if focusedLower != "" && !strings.HasPrefix(name, focusedLower) {
			continue
		}
		choices = append(choices, &discordgo.ApplicationCommandOptionChoice{Name: name, Value: name})
	}
	if len(choices) == 0 {
		for _, name := range postTypeChoices {
			choices = append(choices, &discordgo.ApplicationCommandOptionChoice{Name: name, Value: name})
		}
	}
	return choices
}

func autocompleteRemoveRedditFeed(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate, optionName, focusedValue string) []*discordgo.ApplicationCommandOptionChoice {
	switch optionName {
	case "post-type":
		return autocompletePostType(ctx, db, s, i, optionName, focusedValue)
	case "subreddit":
		return autocompleteRemoveSubreddit(ctx, db, s, i, focusedValue)
	case "channel":
		return autocompleteRemoveChannel(ctx, db, s, i, focusedValue)
	default:
		return nil
	}
}

func autocompleteRemoveSubreddit(ctx context.Context, db *database.Client, _ *discordgo.Session, i *discordgo.InteractionCreate, focusedValue string) []*discordgo.ApplicationCommandOptionChoice {
	if i.GuildID == "" {
		return nil
	}
	feeds, err := db.Guilds().GetFeeds(ctx, i.GuildID)
	if err != nil || len(feeds) == 0 {
		return nil
	}
	seen := make(map[string]struct{})
	for _, f := range feeds {
		sub := normalizeSubreddit(f.Subreddit)
		if sub != "" {
			seen[sub] = struct{}{}
		}
	}
	var subs []string
	for sub := range seen {
		subs = append(subs, sub)
	}
	focusedLower := strings.ToLower(strings.TrimSpace(focusedValue))
	if focusedLower != "" {
		var filtered []string
		for _, sub := range subs {
			subLower := strings.ToLower(sub)
			rSub := "r/" + subLower
			if strings.HasPrefix(rSub, focusedLower) || strings.HasPrefix(subLower, focusedLower) {
				filtered = append(filtered, sub)
			}
		}
		subs = filtered
	}
	sort.Strings(subs)
	const maxChoices = 25
	if len(subs) > maxChoices {
		subs = subs[:maxChoices]
	}
	choices := make([]*discordgo.ApplicationCommandOptionChoice, len(subs))
	for j, sub := range subs {
		name := "r/" + sub
		if len(name) > 100 {
			name = name[:100]
		}
		choices[j] = &discordgo.ApplicationCommandOptionChoice{Name: name, Value: sub}
	}
	return choices
}

func autocompleteRemoveChannel(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate, focusedValue string) []*discordgo.ApplicationCommandOptionChoice {
	if i.GuildID == "" {
		return nil
	}
	feeds, err := db.Guilds().GetFeeds(ctx, i.GuildID)
	if err != nil || len(feeds) == 0 {
		return nil
	}
	seen := make(map[string]struct{})
	for _, f := range feeds {
		if f.ChannelID != "" {
			seen[f.ChannelID] = struct{}{}
		}
	}
	type channelChoice struct {
		id   string
		name string
	}
	var list []channelChoice
	for channelID := range seen {
		display := channelID
		ch, err := s.Channel(channelID)
		if err == nil && ch != nil && ch.Name != "" {
			display = "#" + ch.Name
		} else {
			display = "#" + channelID
		}
		list = append(list, channelChoice{id: channelID, name: display})
	}
	focusedLower := strings.ToLower(strings.TrimSpace(focusedValue))
	if focusedLower != "" {
		focusedTrim := strings.TrimPrefix(focusedLower, "#")
		var filtered []channelChoice
		for _, c := range list {
			nameLower := strings.ToLower(c.name)
			nameNoHash := strings.TrimPrefix(nameLower, "#")
			idLower := strings.ToLower(c.id)
			if strings.HasPrefix(nameLower, focusedLower) || strings.HasPrefix(nameNoHash, focusedTrim) || strings.HasPrefix(idLower, focusedLower) {
				filtered = append(filtered, c)
			}
		}
		list = filtered
	}
	sort.Slice(list, func(a, b int) bool { return list[a].name < list[b].name })
	const maxChoices = 25
	if len(list) > maxChoices {
		list = list[:maxChoices]
	}
	choices := make([]*discordgo.ApplicationCommandOptionChoice, len(list))
	for j, c := range list {
		name := c.name
		if len(name) > 100 {
			name = name[:100]
		}
		choices[j] = &discordgo.ApplicationCommandOptionChoice{Name: name, Value: c.id}
	}
	return choices
}

func handleAddRedditFeed(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.GuildID == "" {
		commands.RespondEmbed(s, i, embedTitleReddit, "This command can only be used in a server.")
		return
	}
	data := i.ApplicationCommandData()
	optChannel := commands.ParseOption(data.Options, "channel")
	optSubreddit := commands.ParseOption(data.Options, "subreddit")
	if optChannel == nil || optSubreddit == nil || optSubreddit.StringValue() == "" {
		commands.RespondEmbed(s, i, embedTitleReddit, "Channel and subreddit are required.")
		return
	}
	ch := optChannel.ChannelValue(s)
	if ch == nil || ch.ID == "" {
		commands.RespondEmbed(s, i, embedTitleReddit, "Invalid or missing channel.")
		return
	}
	channelID := ch.ID
	subreddit := normalizeSubreddit(optSubreddit.StringValue())
	if subreddit == "" {
		commands.RespondEmbed(s, i, embedTitleReddit, "Please provide a valid subreddit (e.g. r/anime or anime).")
		return
	}
	if err := reddit.ValidateSubreddit(subreddit); err != nil {
		commands.RespondEmbed(s, i, embedTitleReddit, err.Error())
		return
	}
	postType := "hot"
	if opt := commands.ParseOption(data.Options, "post-type"); opt != nil && opt.StringValue() != "" {
		postType = strings.ToLower(strings.TrimSpace(opt.StringValue()))
	}
	if !slices.Contains(postTypeChoices, postType) {
		commands.RespondEmbed(s, i, embedTitleReddit, "Invalid post type. Use one of: "+strings.Join(postTypeChoices, ", ")+".")
		return
	}
	author := ""
	if opt := commands.ParseOption(data.Options, "author"); opt != nil {
		author = strings.ToLower(normalizeAuthor(opt.StringValue()))
	}
	title := ""
	if opt := commands.ParseOption(data.Options, "title"); opt != nil {
		title = strings.ToLower(strings.TrimSpace(opt.StringValue()))
	}
	pin := false
	if opt := commands.ParseOption(data.Options, "pin"); opt != nil {
		pin = opt.BoolValue()
	}
	_, err := reddit.Fetch(ctx, subreddit, postType)
	if err != nil {
		if reddit.IsPermanent(err) {
			commands.RespondEmbed(s, i, embedTitleReddit, "That subreddit or feed is unavailable (private, removed, or not found).")
			return
		}
		commands.RespondEmbed(s, i, embedTitleReddit, "Could not verify the feed. Please try again.")
		return
	}
	feed := guilds.FeedEntry{
		Subreddit: subreddit,
		Title:     title,
		Author:    author,
		Pin:       pin,
		PostType:  postType,
		ChannelID: channelID,
	}
	if err := db.Guilds().AddFeed(ctx, i.GuildID, feed); err != nil {
		logger.For("commands").Error("reddit AddFeed failed", "guild_id", i.GuildID, "err", err)
		commands.RespondEmbed(s, i, embedTitleReddit, "Could not save the feed.")
		commands.LogToBotLog(ctx, db, s, i.GuildID, "add-reddit-feed error: "+err.Error())
		return
	}
	content := "Success! This Reddit feed has been added. If there are valid posts they will start appearing shortly."
	if _, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &content}); err != nil {
		args := append(discord.RESTAttrs(err), "guild_id", i.GuildID, "channel_id", i.ChannelID, "err", err)
		logger.For("commands").Error("reddit edit failed", args...)
	}
}

func handleRemoveRedditFeed(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.GuildID == "" {
		commands.RespondEmbed(s, i, embedTitleReddit, "This command can only be used in a server.")
		return
	}
	data := i.ApplicationCommandData()
	optChannel := commands.ParseOption(data.Options, "channel")
	optSubreddit := commands.ParseOption(data.Options, "subreddit")
	if optChannel == nil || optSubreddit == nil || optSubreddit.StringValue() == "" {
		commands.RespondEmbed(s, i, embedTitleReddit, "Channel and subreddit are required.")
		return
	}
	ch := optChannel.ChannelValue(s)
	if ch == nil || ch.ID == "" {
		commands.RespondEmbed(s, i, embedTitleReddit, "Invalid or missing channel.")
		return
	}
	channelID := ch.ID
	subreddit := normalizeSubreddit(optSubreddit.StringValue())
	if subreddit == "" {
		commands.RespondEmbed(s, i, embedTitleReddit, "Please provide a valid subreddit.")
		return
	}
	feeds, err := db.Guilds().GetFeeds(ctx, i.GuildID)
	if err != nil {
		logger.For("commands").Error("reddit GetFeeds failed", "guild_id", i.GuildID, "err", err)
		commands.RespondEmbed(s, i, embedTitleReddit, "Could not load feeds.")
		return
	}
	if len(feeds) == 0 {
		commands.RespondEmbed(s, i, embedTitleReddit, "There are no set Reddit feeds.")
		return
	}
	postTypes := postTypeChoices
	if opt := commands.ParseOption(data.Options, "post-type"); opt != nil && opt.StringValue() != "" {
		pt := strings.ToLower(strings.TrimSpace(opt.StringValue()))
		if !slices.Contains(postTypeChoices, pt) {
			commands.RespondEmbed(s, i, embedTitleReddit, "Invalid post type. Use one of: "+strings.Join(postTypeChoices, ", ")+".")
			return
		}
		postTypes = []string{pt}
	}
	author := ""
	if opt := commands.ParseOption(data.Options, "author"); opt != nil {
		author = strings.ToLower(normalizeAuthor(opt.StringValue()))
	}
	title := ""
	if opt := commands.ParseOption(data.Options, "title"); opt != nil {
		title = strings.ToLower(strings.TrimSpace(opt.StringValue()))
	}
	var removed int64
	for _, pt := range postTypes {
		n, err := db.Guilds().RemoveFeed(ctx, i.GuildID, channelID, subreddit, pt, author, title)
		if err != nil {
			logger.For("commands").Error("reddit RemoveFeed failed", "guild_id", i.GuildID, "err", err)
			commands.RespondEmbed(s, i, embedTitleReddit, "Could not remove the feed.")
			return
		}
		removed += n
		_, _ = db.FeedChecks().DeleteByFeed(ctx, i.GuildID, channelID, subreddit, pt, author, title)
	}
	var msg string
	if removed == 0 {
		msg = "No matching feed found. Check channel, subreddit, and if you used author/title, that they match exactly."
	} else {
		msg = "Success! This Reddit feed has been removed."
		if len(postTypes) == 3 {
			msg = "Success! This Reddit feed has been removed for all types (hot, rising, new) if it existed."
		}
	}
	if _, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &msg}); err != nil {
		args := append(discord.RESTAttrs(err), "guild_id", i.GuildID, "channel_id", i.ChannelID, "err", err)
		logger.For("commands").Error("reddit edit failed", args...)
	}
}

func feedFieldsForPage(feeds []guilds.FeedEntry, start int, s *discordgo.Session, guildID string) []*discordgo.MessageEmbedField {
	fields := make([]*discordgo.MessageEmbedField, 0, discord.MaxEmbedFields)
	end := min(start+discord.MaxEmbedFields, len(feeds))
	for j := start; j < end; j++ {
		f := feeds[j]
		name := "r/" + f.Subreddit
		if guildID != "" && s != nil {
			ch, err := s.Channel(f.ChannelID)
			if err == nil && ch != nil && ch.Name != "" {
				name = "#" + ch.Name + " • r/" + f.Subreddit
			}
		}
		if len(name) > discord.MaxEmbedFieldNameLength {
			name = name[:discord.MaxEmbedFieldNameLength-1] + "…"
		}
		value := f.PostType
		if f.Author != "" {
			value += " | u/" + f.Author
		}
		if f.Title != "" {
			title := f.Title
			if len(title) > 80 {
				title = title[:77] + "…"
			}
			value += " | " + title
		}
		if f.Pin {
			value += " | Pinned"
		}
		if len(value) > discord.MaxEmbedFieldValueLength {
			value = value[:discord.MaxEmbedFieldValueLength-1] + "…"
		}
		fields = append(fields, &discordgo.MessageEmbedField{Name: name, Value: value})
	}
	return fields
}

func handleRedditFeeds(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.GuildID == "" {
		commands.RespondEmbed(s, i, embedTitleReddit, "This command can only be used in a server.")
		return
	}
	feeds, err := db.Guilds().GetFeeds(ctx, i.GuildID)
	if err != nil {
		logger.For("commands").Error("reddit GetFeeds failed", "guild_id", i.GuildID, "err", err)
		commands.RespondEmbed(s, i, embedTitleReddit, "Could not load feeds.")
		return
	}
	if len(feeds) == 0 {
		commands.RespondEmbed(s, i, embedTitleReddit, "There are no set Reddit feeds.")
		return
	}
	n := len(feeds)
	fields := feedFieldsForPage(feeds, 0, s, i.GuildID)
	description := ""
	var components []discordgo.MessageComponent
	if n > discord.MaxEmbedFields {
		totalPages := (n + discord.MaxEmbedFields - 1) / discord.MaxEmbedFields
		description = "Page 1 of " + strconv.Itoa(totalPages) + ". Showing 1–" + strconv.Itoa(min(discord.MaxEmbedFields, n)) + " of " + strconv.Itoa(n) + "."
		components = commands.PaginationComponents("reddit-feeds", 0, n, commands.UserID(i))
	}
	if len(components) > 0 {
		commands.RespondEmbedWithFieldsAndComponents(s, i, embedTitleReddit, description, fields, components, hintRemoveReddit)
	} else {
		commands.RespondEmbedWithFields(s, i, embedTitleReddit, description, fields, hintRemoveReddit)
	}
}

// RenderRedditFeedsPage renders one page of reddit-feeds for pagination.
func RenderRedditFeedsPage(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate, page, total int, authorID string) {
	feeds, err := db.Guilds().GetFeeds(ctx, i.GuildID)
	if err != nil {
		logger.For("commands").Error("reddit GetFeeds page failed", "guild_id", i.GuildID, "err", err)
		_ = commands.RespondEphemeral(s, i, "Could not load feeds.")
		return
	}
	n := len(feeds)
	if n == 0 {
		_ = commands.RespondEphemeral(s, i, "There are no set Reddit feeds.")
		return
	}
	totalPages := (n + discord.MaxEmbedFields - 1) / discord.MaxEmbedFields
	if page >= totalPages {
		page = totalPages - 1
	}
	start := page * discord.MaxEmbedFields
	fields := feedFieldsForPage(feeds, start, s, i.GuildID)
	end := start + len(fields)
	description := "Page " + strconv.Itoa(page+1) + " of " + strconv.Itoa(totalPages) + ". Showing " + strconv.Itoa(start+1) + "–" + strconv.Itoa(end) + " of " + strconv.Itoa(n) + "."
	components := commands.PaginationComponents("reddit-feeds", page, n, authorID)
	emb := commands.NewEmbedWithFields(s, embedTitleReddit, description, fields, hintRemoveReddit)
	err = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Embeds:     []*discordgo.MessageEmbed{emb},
			Components: components,
		},
	})
	if err != nil {
		args := append(discord.RESTAttrs(err), "guild_id", i.GuildID, "channel_id", i.ChannelID, "err", err)
		logger.For("commands").Error("reddit RenderRedditFeedsPage respond failed", args...)
	}
}
