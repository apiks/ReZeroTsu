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
func registerPages(t *testing.T, cmd string) { registerPagesAs(t, cmd, true) }

func registerPagesAs(t *testing.T, cmd string, copySafe bool) {
	t.Helper()
	register := RegisterPaginationRenderer
	if copySafe {
		register = RegisterCopySafePaginationRenderer
	}
	register(cmd, func(_ context.Context, _ *database.Client, _ *discordgo.Session, _ *discordgo.InteractionCreate, page, total int, authorID string) Page {
		if total == 0 {
			return Page{Refusal: "nothing to show"}
		}
		return Page{Response: interaction.Response{
			Content:    "page " + string(rune('0'+page)) + " of " + authorID,
			Components: PaginationComponents(cmd, page, total, authorID),
		}}
	})
	t.Cleanup(func() {
		delete(paginationRenderers, cmd)
		delete(copySafeRenderers, cmd)
		delete(serverOnlyRenderers, cmd)
	})
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

func TestCopySafeViewWithoutAuthorPagesForEveryone(t *testing.T) {
	registerPages(t, "t-pages")
	fake := &interactiontest.Fake{}
	dispatchPress(t, fake, componentInteraction(press(pageButton("t-pages", 0, 60, "")), "g", "anyone", publicMessage()))
	if ops := fake.Ops(); len(ops) != 1 || ops[0] != "UpdateMessage" {
		t.Errorf("a copy-safe view with no author pages in place for anyone, got %v", ops)
	}
}

func TestRestrictedViewRefusesOtherMembersWithoutRendering(t *testing.T) {
	rendered := false
	RegisterPaginationRenderer("t-private", func(context.Context, *database.Client, *discordgo.Session, *discordgo.InteractionCreate, int, int, string) Page {
		rendered = true
		return Page{Response: interaction.Response{Content: "secret"}}
	})
	t.Cleanup(func() { delete(paginationRenderers, "t-private") })
	for _, author := range []string{"author", ""} {
		fake := &interactiontest.Fake{}
		dispatchPress(t, fake, componentInteraction(press(pageButton("t-private", 1, 60, author)), "g", "stranger", publicMessage()))
		calls := fake.Calls()
		if len(calls) != 1 || calls[0].Op != "Reply" || calls[0].Visibility != interaction.Private || calls[0].Response.Content != msgOnlyAuthorPages {
			t.Errorf("author %q: want one private refusal, got %+v", author, calls)
		}
	}
	if rendered {
		t.Error("a refused press must not render the restricted view")
	}
}

func TestRestrictedViewStillPagesInPlaceForItsAuthor(t *testing.T) {
	registerPagesAs(t, "t-private", false)
	fake := &interactiontest.Fake{}
	dispatchPress(t, fake, componentInteraction(press(pageButton("t-private", 1, 60, "author")), "g", "author", publicMessage()))
	if ops := fake.Ops(); len(ops) != 1 || ops[0] != "UpdateMessage" {
		t.Errorf("the author pages in place, got %v", ops)
	}
}

func TestServerOnlyViewIsRefusedWithoutAServerAndRendersInOne(t *testing.T) {
	rendered := 0
	RegisterServerPaginationRenderer("t-server", func(context.Context, *database.Client, *discordgo.Session, *discordgo.InteractionCreate, int, int, string) Page {
		rendered++
		return Page{Response: interaction.Response{Content: "page"}}
	})
	t.Cleanup(func() { delete(paginationRenderers, "t-server"); delete(serverOnlyRenderers, "t-server") })

	fake := &interactiontest.Fake{}
	dispatchPress(t, fake, componentInteraction(press(pageButton("t-server", 1, 60, "author")), "", "author", publicMessage()))
	calls := fake.Calls()
	if len(calls) != 1 || calls[0].Op != "Reply" || calls[0].Visibility != interaction.Private || calls[0].Response.Content != msgFailedLoad || rendered != 0 {
		t.Errorf("no server: want one private refusal and no render, got %+v (rendered %d)", calls, rendered)
	}

	fake = &interactiontest.Fake{}
	dispatchPress(t, fake, componentInteraction(press(pageButton("t-server", 1, 60, "author")), "g", "author", publicMessage()))
	if ops := fake.Ops(); len(ops) != 1 || ops[0] != "UpdateMessage" || rendered != 1 {
		t.Errorf("in a server: want one UpdateMessage and one render, got %v (rendered %d)", ops, rendered)
	}
}
