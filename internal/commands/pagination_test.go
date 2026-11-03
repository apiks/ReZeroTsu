package commands

import (
	"context"
	"strings"
	"testing"

	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/interaction"
	"ReZeroTsu/internal/interaction/interactiontest"

	"github.com/bwmarrin/discordgo"
)

// registerPages adds a test-only page renderer that reports the page and data owner it was asked for.
func registerPages(t *testing.T, cmd string) {
	t.Helper()
	RegisterPaginationRenderer(cmd, func(_ context.Context, _ *database.Client, _ *discordgo.Session, _ *discordgo.InteractionCreate, page, total int, authorID string) Page {
		if total == 0 {
			return Page{Refusal: "nothing to show"}
		}
		return Page{Response: interaction.Response{
			Content:    "page " + string(rune('0'+page)) + " of " + authorID,
			Components: PaginationComponents(cmd, page, total, authorID),
		}}
	})
	t.Cleanup(func() { delete(paginationRenderers, cmd) })
}

func pageButton(cmd string, page, total int, author string) string {
	return BuildPaginationCustomID(cmd, page, total, author)
}

func publicMessage() *discordgo.Message { return &discordgo.Message{ID: "m"} }
func privateMessage() *discordgo.Message {
	return &discordgo.Message{ID: "m", Flags: discordgo.MessageFlagsEphemeral}
}

func TestAuthorPressPagesTheMessageItself(t *testing.T) {
	registerPages(t, "t-pages")
	fake := &interactiontest.Fake{}
	dispatchPress(t, fake, componentInteraction(press(pageButton("t-pages", 1, 60, "author")), "g", "author", publicMessage()))

	calls := fake.Calls()
	if len(calls) != 1 || calls[0].Op != "UpdateMessage" {
		t.Fatalf("want one UpdateMessage, got %v", fake.Ops())
	}
	if got := calls[0].Response.Content; got != "page 1 of author" {
		t.Errorf("content = %q", got)
	}
}

func TestOtherMembersPressGetsAPrivateCopyAtTheRequestedPage(t *testing.T) {
	registerPages(t, "t-pages")
	fake := &interactiontest.Fake{}
	dispatchPress(t, fake, componentInteraction(press(pageButton("t-pages", 1, 60, "author")), "g", "stranger", publicMessage()))

	calls := fake.Calls()
	if len(calls) != 1 || calls[0].Op != "Reply" || calls[0].Visibility != interaction.Private {
		t.Fatalf("want one private Reply, got %+v", calls)
	}
	if got := calls[0].Response.Content; got != "page 1 of author" {
		t.Errorf("the copy shows what the message shows at the requested page, got %q", got)
	}
	if strings.Contains(calls[0].Response.Content, "Only the user who ran the command") {
		t.Error("the author-only refusal is gone")
	}
}

func TestPressOnAPrivateCopyPagesTheCopy(t *testing.T) {
	registerPages(t, "t-pages")
	fake := &interactiontest.Fake{}
	// The stranger holds a private copy whose buttons still carry the author's ID.
	dispatchPress(t, fake, componentInteraction(press(pageButton("t-pages", 2, 60, "author")), "g", "stranger", privateMessage()))

	calls := fake.Calls()
	if len(calls) != 1 || calls[0].Op != "UpdateMessage" {
		t.Fatalf("want the copy updated in place, got %v", fake.Ops())
	}
	if got := calls[0].Response.Content; got != "page 2 of author" {
		t.Errorf("content = %q", got)
	}
}

func TestPaginationWithoutAuthorPagesForEveryone(t *testing.T) {
	registerPages(t, "t-pages")
	fake := &interactiontest.Fake{}
	dispatchPress(t, fake, componentInteraction(press(pageButton("t-pages", 0, 60, "")), "g", "anyone", publicMessage()))
	if ops := fake.Ops(); len(ops) != 1 || ops[0] != "UpdateMessage" {
		t.Errorf("a list with no author pages in place for anyone, got %v", ops)
	}
}

func TestPaginationRefusalsArePrivate(t *testing.T) {
	registerPages(t, "t-pages")
	registerPages(t, "show-raffles")
	tests := []struct {
		name string
		id   string
		i    func(id string) *discordgo.InteractionCreate
		want string
	}{
		{"malformed ID", "pag:broken", func(id string) *discordgo.InteractionCreate {
			return componentInteraction(press(id), "g", "u", publicMessage())
		}, msgInvalidButton},
		{"unknown list", pageButton("t-gone", 0, 5, "u"), func(id string) *discordgo.InteractionCreate {
			return componentInteraction(press(id), "g", "u", publicMessage())
		}, msgInvalidButton},
		{"server-only list pressed in a DM", pageButton("show-raffles", 0, 5, ""), func(id string) *discordgo.InteractionCreate {
			return componentInteraction(press(id), "", "u", publicMessage())
		}, msgFailedLoad},
		{"renderer has nothing to show", pageButton("t-pages", 0, 0, "u"), func(id string) *discordgo.InteractionCreate {
			return componentInteraction(press(id), "g", "u", publicMessage())
		}, "nothing to show"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &interactiontest.Fake{}
			dispatchPress(t, fake, tt.i(tt.id))
			calls := fake.Calls()
			if len(calls) != 1 || calls[0].Op != "Reply" || calls[0].Visibility != interaction.Private || calls[0].Response.Content != tt.want {
				t.Errorf("want one private reply %q, got %+v", tt.want, calls)
			}
		})
	}
}

func TestPaginationClampsThePageBeforeRendering(t *testing.T) {
	registerPages(t, "t-pages")
	fake := &interactiontest.Fake{}
	// 60 items is 3 pages of 25, so page 9 is page 2; schedule-like negative pages become 0.
	dispatchPress(t, fake, componentInteraction(press(pageButton("t-pages", 9, 60, "u")), "g", "u", publicMessage()))
	if got := fake.Calls()[0].Response.Content; got != "page 2 of u" {
		t.Errorf("content = %q, want page 2", got)
	}
	fake = &interactiontest.Fake{}
	dispatchPress(t, fake, componentInteraction(press(pageButton("t-pages", -1, 60, "u")), "g", "u", publicMessage()))
	if got := fake.Calls()[0].Response.Content; got != "page 0 of u" {
		t.Errorf("content = %q, want page 0", got)
	}
}
