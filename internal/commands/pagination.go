package commands

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/discord"
	"ReZeroTsu/internal/logger"

	"github.com/bwmarrin/discordgo"
)

const (
	msgInvalidButton   = "Invalid button. Run the command again."
	msgOnlyAuthorPages = "Only the user who ran the command can change pages."
	msgFailedLoad      = "Could not load. Please run the command again."
)

// RespondEmbedWithFieldsAndComponents edits the deferred response with embed, optional fields, and components (e.g. pagination buttons).
func RespondEmbedWithFieldsAndComponents(s *discordgo.Session, i *discordgo.InteractionCreate, title, description string, fields []*discordgo.MessageEmbedField, components []discordgo.MessageComponent, footer ...string) {
	emb := NewEmbedWithFields(s, title, description, fields, footer...)
	edit := &discordgo.WebhookEdit{Embeds: &[]*discordgo.MessageEmbed{emb}}
	if len(components) > 0 {
		edit.Components = &components
	}
	if _, err := s.InteractionResponseEdit(i.Interaction, edit); err != nil {
		args := append(discord.RESTAttrs(err), "guild_id", i.GuildID, "channel_id", i.ChannelID, "err", err)
		logger.For("commands").Error("RespondEmbedWithFieldsAndComponents edit failed", args...)
	}
}

const PaginationPrefix = "pag:"

// BuildPaginationCustomID builds custom_id: pag:<cmd>:<page>:<total>[:authorID].
func BuildPaginationCustomID(cmd string, page, total int, authorID string) string {
	if authorID != "" {
		return fmt.Sprintf("%s%s:%d:%d:%s", PaginationPrefix, cmd, page, total, authorID)
	}
	return fmt.Sprintf("%s%s:%d:%d", PaginationPrefix, cmd, page, total)
}

// ParsePaginationCustomID parses custom_id; authorID empty if absent.
func ParsePaginationCustomID(customID string) (cmd string, page, total int, authorID string, ok bool) {
	if !strings.HasPrefix(customID, PaginationPrefix) {
		return "", 0, 0, "", false
	}
	rest := customID[len(PaginationPrefix):]
	parts := strings.SplitN(rest, ":", 4)
	if len(parts) < 3 {
		return "", 0, 0, "", false
	}
	cmd = parts[0]
	var err error
	page, err = strconv.Atoi(parts[1])
	if err != nil {
		return "", 0, 0, "", false
	}
	total, err = strconv.Atoi(parts[2])
	if err != nil {
		return "", 0, 0, "", false
	}
	if len(parts) > 3 {
		authorID = parts[3]
	}
	return cmd, page, total, authorID, true
}

// BuildSchedulePaginationComponents builds Previous/Next for schedule; dayIndex 0=Sunday .. 6=Saturday.
func BuildSchedulePaginationComponents(dayIndex int, authorID string) []discordgo.MessageComponent {
	if dayIndex < 0 {
		dayIndex = 0
	}
	if dayIndex > 6 {
		dayIndex = 6
	}
	prevID := BuildPaginationCustomID("schedule", dayIndex-1, 7, authorID)
	nextID := BuildPaginationCustomID("schedule", dayIndex+1, 7, authorID)
	row := discordgo.ActionsRow{
		Components: []discordgo.MessageComponent{
			discordgo.Button{
				CustomID: prevID,
				Label:    "Previous",
				Style:    discordgo.SecondaryButton,
				Disabled: dayIndex <= 0,
			},
			discordgo.Button{
				CustomID: nextID,
				Label:    "Next",
				Style:    discordgo.SecondaryButton,
				Disabled: dayIndex >= 6,
			},
		},
	}
	return []discordgo.MessageComponent{row}
}

// PaginationComponents builds Previous/Next buttons; page is 0-indexed, authorID optional.
func PaginationComponents(cmd string, page, totalCount int, authorID string) []discordgo.MessageComponent {
	pageSize := discord.MaxEmbedFields
	totalPages := max((totalCount+pageSize-1)/pageSize, 1)
	if page < 0 {
		page = 0
	}
	if page >= totalPages {
		page = totalPages - 1
	}
	prevID := BuildPaginationCustomID(cmd, page-1, totalCount, authorID)
	nextID := BuildPaginationCustomID(cmd, page+1, totalCount, authorID)
	prevDisabled := page <= 0
	nextDisabled := (page+1)*pageSize >= totalCount
	row := discordgo.ActionsRow{
		Components: []discordgo.MessageComponent{
			discordgo.Button{
				CustomID: prevID,
				Label:    "Previous",
				Style:    discordgo.SecondaryButton,
				Disabled: prevDisabled,
			},
			discordgo.Button{
				CustomID: nextID,
				Label:    "Next",
				Style:    discordgo.SecondaryButton,
				Disabled: nextDisabled,
			},
		},
	}
	return []discordgo.MessageComponent{row}
}

// PageRenderer renders one page: re-fetches data, builds embed, responds.
type PageRenderer func(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate, page, total int, authorID string)

var paginationRenderers = make(map[string]PageRenderer)

// RegisterPaginationRenderer registers a page renderer for a command. Call from init().
func RegisterPaginationRenderer(cmd string, fn PageRenderer) {
	paginationRenderers[cmd] = fn
}

// HandlePagination handles a pagination button; validates author when authorID present, dispatches to renderer.
func HandlePagination(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate) bool {
	if i.Type != discordgo.InteractionMessageComponent {
		return false
	}
	customID := i.MessageComponentData().CustomID
	if !HasPaginationPrefix(customID) {
		return false
	}
	cmd, page, total, authorID, ok := ParsePaginationCustomID(customID)
	if !ok {
		_ = RespondEphemeral(s, i, msgInvalidButton)
		return true
	}
	var totalPages int
	if cmd == "schedule" {
		totalPages = total
	} else {
		totalPages = max((total+discord.MaxEmbedFields-1)/discord.MaxEmbedFields, 1)
	}
	if page < 0 {
		page = 0
	}
	if page >= totalPages {
		page = totalPages - 1
	}
	if authorID != "" {
		interactorID := UserID(i)
		if interactorID != authorID {
			_ = RespondEphemeral(s, i, msgOnlyAuthorPages)
			return true
		}
	}
	render, ok := paginationRenderers[cmd]
	if !ok {
		_ = RespondEphemeral(s, i, msgInvalidButton)
		return true
	}
	switch cmd {
	case "show-raffles", "my-raffles", "reacts-autorole", "reddit-feeds":
		if i.GuildID == "" || i.Member == nil {
			_ = RespondEphemeral(s, i, msgFailedLoad)
			return true
		}
	}
	render(ctx, db, s, i, page, total, authorID)
	return true
}

func HasPaginationPrefix(customID string) bool {
	return len(customID) >= len(PaginationPrefix) && customID[:len(PaginationPrefix)] == PaginationPrefix
}
