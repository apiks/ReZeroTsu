package react

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"

	"ReZeroTsu/internal/database/guilds"
	"ReZeroTsu/internal/discord"
	"ReZeroTsu/internal/interaction"

	"github.com/bwmarrin/discordgo"
)

func restCode(code int) error {
	return &discordgo.RESTError{Message: &discordgo.APIErrorMessage{Code: code}}
}

func TestThePickerExplainsReactionsItCouldNotTakeOff(t *testing.T) {
	st := useStore(t, map[string]*guilds.ReactJoin{
		"m1": {ChannelID: "c1", RoleEmojiMap: []map[string][]string{{roleA: {"😀"}}}},
	})
	id := firstPickerID(t, "user")
	ic, fake := pickRemove("user", id, "m1")
	fake.Fail = map[string]error{"RemoveOwnReaction": &discordgo.RESTError{Response: &http.Response{StatusCode: 403}}}
	if err := handleRemovePicker(ic, nil); err != nil {
		t.Fatal(err)
	}
	if len(st.entries) != 0 {
		t.Errorf("entries left: %v", st.entries)
	}
	only(t, fake, "UpdateMessage")
	f := only(t, fake, "FollowUp")
	if f.Visibility != interaction.Private {
		t.Errorf("follow-up visibility = %v, want private", f.Visibility)
	}
	if got := f.Response.Content; !strings.Contains(got, "Removed the reaction roles, but I couldn't take my reactions off the message") || !strings.Contains(got, "Read Message History") {
		t.Errorf("follow-up %q", got)
	}
}

func TestThePickerStaysSilentWhenTheChannelIsGone(t *testing.T) {
	useStore(t, map[string]*guilds.ReactJoin{
		"m1": {ChannelID: "c1", RoleEmojiMap: []map[string][]string{{roleA: {"😀"}}}},
	})
	id := firstPickerID(t, "user")
	ic, fake := pickRemove("user", id, "m1")
	fake.Fail = map[string]error{"RemoveOwnReaction": restCode(discordgo.ErrCodeUnknownChannel)}
	if err := handleRemovePicker(ic, nil); err != nil {
		t.Fatal(err)
	}
	for _, op := range fake.Ops() {
		if op == "FollowUp" {
			t.Error("explained a channel that is gone")
		}
	}
}

func TestAddingToAMessageWithTwentyReactionsIsExplained(t *testing.T) {
	st := useStore(t, nil)
	fake := worldFake()
	fake.Fail = map[string]error{"AddReaction": restCode(discord.MaxReactionsCode)}
	if err := submit(t, fake, formID, "🎉", roleA); err != nil {
		t.Fatal(err)
	}
	if got := replyText(t, fake); got != msgMaxReacts {
		t.Errorf("reply %q", got)
	}
	if st.saves != 0 {
		t.Error("a binding was stored")
	}
}

func TestAddingACustomEmojiNamesUseExternalEmojis(t *testing.T) {
	useStore(t, nil)
	fake := worldFake()
	fake.Fail = map[string]error{"AddReaction": &discordgo.RESTError{Response: &http.Response{StatusCode: 403}}}
	if err := submit(t, fake, formID, "<:party:123>", roleA); err != nil {
		t.Fatal(err)
	}
	if got := replyText(t, fake); !strings.Contains(got, "Use External Emojis") {
		t.Errorf("reply %q", got)
	}
	fake = worldFake()
	fake.Fail = map[string]error{"AddReaction": &discordgo.RESTError{Response: &http.Response{StatusCode: 403}}}
	if err := submit(t, fake, formID, "🎉", roleA); err != nil {
		t.Fatal(err)
	}
	if got := replyText(t, fake); strings.Contains(got, "Use External Emojis") {
		t.Errorf("standard emoji reply %q", got)
	}
}

func TestAPageOfManyBindingsStaysUnderTheEmbedTotal(t *testing.T) {
	m := map[string]*guilds.ReactJoin{}
	for i := 0; i < 30; i++ {
		var rm []map[string][]string
		for r := 0; r < 4; r++ {
			rm = append(rm, map[string][]string{fmt.Sprintf("%018d", 1000+r): {"party:123456789012345678", "😀", "wave:223456789012345678"}})
		}
		m[fmt.Sprintf("%018d", i+1)] = &guilds.ReactJoin{ChannelID: "123456789012345678", RoleEmojiMap: rm}
	}
	ids := boundMessages(m)
	for page := 0; page < 2; page++ {
		e := renderPage(m, ids, page, "user", "123456789012345678").Embeds[0]
		total := utf8.RuneCountInString(e.Title) + utf8.RuneCountInString(e.Description) + utf8.RuneCountInString(e.Footer.Text)
		for _, f := range e.Fields {
			total += utf8.RuneCountInString(f.Name) + utf8.RuneCountInString(f.Value)
			if utf8.RuneCountInString(f.Value) > discord.MaxEmbedFieldValueLength {
				t.Errorf("a value has %d characters", utf8.RuneCountInString(f.Value))
			}
		}
		if total > 6000 {
			t.Errorf("page %d has %d characters", page, total)
		}
		if page == 0 && !strings.HasSuffix(e.Fields[0].Value, "…") {
			t.Errorf("a long value was not clipped: %q", e.Fields[0].Value)
		}
	}
}

func TestARemovalReplyWithHugeLegacyEntriesFitsOneMessage(t *testing.T) {
	var roles []map[string][]string
	for r := 0; r < 30; r++ {
		roles = append(roles, map[string][]string{fmt.Sprintf("%018d", 1000+r): {"alpha:123456789012345678", "beta:123456789012345678", "gamma:123456789012345678"}})
	}
	useStore(t, map[string]*guilds.ReactJoin{"m1": {ChannelID: "c1", RoleEmojiMap: roles}})
	fake := worldFake()
	fake.Fail = map[string]error{"RemoveOwnReaction": &discordgo.RESTError{Response: &http.Response{StatusCode: 403}}}
	if err := rightClick(t, fake, "Remove reaction roles", target()); err != nil {
		t.Fatal(err)
	}
	got := replyText(t, fake)
	if n := utf8.RuneCountInString(got); n > discord.MaxContentLength {
		t.Errorf("reply has %d characters", n)
	}
	if !strings.Contains(got, "…and ") || !strings.Contains(got, " more") || !strings.Contains(got, "I couldn't take my reactions off") {
		t.Errorf("reply %q", got)
	}
}

func TestClipLinesKeepsEverythingThatFits(t *testing.T) {
	if got := clipLines([]string{"a", "b"}, 10); got != "a\nb" {
		t.Errorf("got %q", got)
	}
}
