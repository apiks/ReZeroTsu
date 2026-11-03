package schedule

import (
	"strings"
	"testing"

	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/interaction"
	"ReZeroTsu/internal/interaction/interactiontest"

	"github.com/bwmarrin/discordgo"
)

func pickerOf(t *testing.T, r interaction.Response) discordgo.SelectMenu {
	t.Helper()
	if len(r.Components) != 2 {
		t.Fatalf("want a picker row above the button row, got %d rows", len(r.Components))
	}
	return r.Components[0].(discordgo.ActionsRow).Components[0].(discordgo.SelectMenu)
}

// choosePick chooses date in the picker of the message of the author in customID as user.
func choosePick(t *testing.T, customID, date, user string, msg *discordgo.Message) []interactiontest.Call {
	t.Helper()
	fake := &interactiontest.Fake{}
	i := &discordgo.Interaction{Type: discordgo.InteractionMessageComponent, ChannelID: "chan", Message: msg, User: &discordgo.User{ID: user},
		Data: discordgo.MessageComponentInteractionData{CustomID: customID, ComponentType: discordgo.SelectMenuComponent, Values: []string{date}}}
	ic := interaction.NewContext(fake, i, (*database.Client)(nil), interaction.Private, interaction.Direct)
	if err := handlePick(ic, nil); err != nil {
		t.Fatal(err)
	}
	return fake.Calls()
}

func TestPickerListsAllLoadedDaysMarksTodayAndPreselectsTheShownDay(t *testing.T) {
	seedWeeks(t)
	menu := pickerOf(t, openNavDay(t, "friday"))
	if len(menu.Options) != 21 || len(menu.CustomID) >= 100 || !strings.Contains(menu.CustomID, "user") {
		t.Fatalf("want 21 options and a short custom ID carrying the author, got %d options, %q", len(menu.Options), menu.CustomID)
	}
	if menu.Options[0].Value != loadedFirst || menu.Options[0].Label != "Mon 26 Oct" || menu.Options[20].Value != loadedLast {
		t.Errorf("options should run in order from %s to %s: first %+v", loadedFirst, loadedLast, menu.Options[0])
	}
	var today, defaults []string
	for _, o := range menu.Options {
		if strings.Contains(o.Label, "(today)") {
			today = append(today, o.Label)
		}
		if o.Default {
			defaults = append(defaults, o.Value)
		}
	}
	if len(today) != 1 || today[0] != "Wed 4 Nov (today)" {
		t.Errorf("today marks = %v, want Wed 4 Nov (today)", today)
	}
	if len(defaults) != 1 || defaults[0] != "2026-11-06" {
		t.Errorf("preselected = %v, want 2026-11-06", defaults)
	}
}

func TestChoosingADayShowsItsPageWithUpdatedControls(t *testing.T) {
	seedWeeks(t)
	menu := pickerOf(t, openNavDay(t, "friday"))
	calls := choosePick(t, menu.CustomID, "2026-11-12", "user", publicMsg())
	if len(calls) != 1 || calls[0].Op != "UpdateMessage" {
		t.Fatalf("want one UpdateMessage, got %+v", calls)
	}
	r := calls[0].Response
	if title := r.Embeds[0].Title; !strings.HasPrefix(title, "Thursday, 12 Nov 2026") {
		t.Errorf("title = %q", title)
	}
	for _, o := range pickerOf(t, r).Options {
		if o.Default != (o.Value == "2026-11-12") {
			t.Errorf("option %s default=%v", o.Value, o.Default)
		}
	}
}

func TestPickerChoiceByAnotherMemberGetsAPrivateCopyAndOnAPrivateCopyUpdatesIt(t *testing.T) {
	seedWeeks(t)
	id := pickerCustomID("author")
	calls := choosePick(t, id, "2026-11-05", "stranger", publicMsg())
	if len(calls) != 1 || calls[0].Op != "Reply" || calls[0].Visibility != interaction.Private {
		t.Fatalf("other member: want a private copy, got %+v", calls)
	}
	private := &discordgo.Message{ID: "m", Flags: discordgo.MessageFlagsEphemeral}
	calls = choosePick(t, id, "2026-11-05", "stranger", private)
	if len(calls) != 1 || calls[0].Op != "UpdateMessage" {
		t.Errorf("choice on a private copy: want it updated, got %+v", calls)
	}
}

func TestPickerChoiceOutsideTheLoadedRangeIsRefusedPrivately(t *testing.T) {
	seedWeeks(t)
	for _, day := range []string{staleDay, "2026-11-16", "garbage"} {
		calls := choosePick(t, pickerCustomID("user"), day, "user", publicMsg())
		if len(calls) != 1 || calls[0].Visibility != interaction.Private || calls[0].Response.Content != msgDayNotLoaded {
			t.Errorf("%s: want the private refusal %q, got %+v", day, msgDayNotLoaded, calls)
		}
	}
}
