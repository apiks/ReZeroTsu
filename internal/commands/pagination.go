package commands

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/discord"
	"ReZeroTsu/internal/interaction"

	"github.com/bwmarrin/discordgo"
)

const (
	msgInvalidButton = "Invalid button. Run the command again."
	msgFailedLoad    = "Could not load. Please run the command again."

	msgOnlyAuthorPages = "Only the user who ran the command can change pages."
)

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

// Page is what a renderer produces for one page: the content to show, or a Refusal to show only to the presser
// (nothing to list, data could not load).
type Page struct {
	Response interaction.Response
	Refusal  string
}

// PageRenderer renders one page: re-fetches the data, builds the embed and its buttons, and returns them. It
// never answers the interaction; the pagination handler decides whether the page replaces the message or goes
// to a private copy. authorID is whose message it is; the data shown belongs to them, whoever pressed. s and i
// are only for legacy renderers that still read through the raw session.
type PageRenderer func(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate, page, total int, authorID string) Page

var paginationRenderers = make(map[string]PageRenderer)

// copySafeRenderers holds the commands whose view anyone may run, so a private copy for another member leaks
// nothing.
var copySafeRenderers = make(map[string]bool)

// serverOnlyRenderers holds the commands whose view needs a server and a member.
var serverOnlyRenderers = make(map[string]bool)

// RegisterPaginationRenderer registers a page renderer for a command whose view is restricted (moderator setup,
// someone's personal list): only the author pages it, anyone else gets msgOnlyAuthorPages. Call from init().
func RegisterPaginationRenderer(cmd string, fn PageRenderer) {
	paginationRenderers[cmd] = fn
	delete(copySafeRenderers, cmd)
	delete(serverOnlyRenderers, cmd)
}

// RegisterServerPaginationRenderer registers a restricted page renderer, as RegisterPaginationRenderer does, for a
// view that exists only in a server: a press with no server or member is refused before anything renders. Call
// from init().
func RegisterServerPaginationRenderer(cmd string, fn PageRenderer) {
	RegisterPaginationRenderer(cmd, fn)
	serverOnlyRenderers[cmd] = true
}

// RegisterCopySafePaginationRenderer registers a page renderer for a command anyone may run, so another member's
// press gets a private copy of the page. Call from init().
func RegisterCopySafePaginationRenderer(cmd string, fn PageRenderer) {
	paginationRenderers[cmd] = fn
	copySafeRenderers[cmd] = true
	delete(serverOnlyRenderers, cmd)
}

func init() {
	AddComponent(PaginationPrefix, handlePagination)
}

// handlePagination handles a page button. The author's press (or any press on a private copy) replaces the
// message the button is on. Anyone else's press gets a private copy of the requested page, whose buttons page
// the copy, when the command is copy-safe, and a private refusal otherwise.
func handlePagination(ic *interaction.Context, s *discordgo.Session) error {
	cmd, page, total, authorID, ok := ParsePaginationCustomID(ic.CustomID)
	if !ok {
		return ic.Refuse(msgInvalidButton)
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
	// Guard home: a restricted list (moderator setup, personal lists) is refused to anyone but its author before
	// anything renders. Only copy-safe views reach the private-copy path below.
	if !copySafeRenderers[cmd] && ic.Invoker.ID != authorID {
		return ic.Refuse(msgOnlyAuthorPages)
	}
	render, ok := paginationRenderers[cmd]
	if !ok {
		return ic.Refuse(msgInvalidButton)
	}
	if serverOnlyRenderers[cmd] && (ic.GuildID == "" || ic.Member == nil) {
		return ic.Refuse(msgFailedLoad)
	}
	p := render(ic.Context(), ic.DB, s, &discordgo.InteractionCreate{Interaction: ic.Interaction}, page, total, authorID)
	if p.Refusal != "" {
		return ic.Refuse(p.Refusal)
	}
	if authorID == "" || ic.Invoker.ID == authorID || ic.OnPrivateMessage() {
		return ic.UpdateMessage(p.Response)
	}
	p.Response.Visibility = interaction.Private
	return ic.Reply(p.Response)
}
