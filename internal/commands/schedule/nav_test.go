package schedule

import (
	"strings"
	"testing"
	"time"

	"ReZeroTsu/internal/animeschedule"
	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/interaction"
	"ReZeroTsu/internal/interaction/interactiontest"

	"github.com/bwmarrin/discordgo"
)

const (
	loadedFirst = "2026-10-26" // last week's Monday, with the clock on Wednesday 2026-11-04
	loadedLast  = "2026-11-15" // next week's Sunday
	staleDay    = "2026-10-19"
)

func seedWeeks(t *testing.T) {
	t.Helper()
	setClock(t)
	days := map[string][]animeschedule.ShowEntry{}
	for d := mustDate(loadedFirst); !d.After(mustDate(loadedLast)); d = d.AddDate(0, 0, 1) {
		days[animeschedule.DateKey(d)] = []animeschedule.ShowEntry{{Name: "Show " + animeschedule.DateKey(d), Episode: "Episode 1", Track: "sub", AirTimeUnix: 1700000000}}
	}
	t.Cleanup(animeschedule.Seed(days))
	animeschedule.SetAPIKey("key")
	t.Cleanup(func() { animeschedule.SetAPIKey("") })
}

func mustDate(s string) time.Time {
	d, err := animeschedule.ParseDateKey(s)
	if err != nil {
		panic(err)
	}
	return d
}

// pressNav presses a navigation button as user on msg and returns what the member saw.
func pressNav(t *testing.T, customID, user string, msg *discordgo.Message) []interactiontest.Call {
	t.Helper()
	return pressNavIn(t, customID, user, "", msg)
}

// pressNavIn is pressNav from the server guildID.
func pressNavIn(t *testing.T, customID, user, guildID string, msg *discordgo.Message) []interactiontest.Call {
	t.Helper()
	return pressNavAs(t, customID, user, guildID, false, msg)
}

// pressNavAs is pressNavIn; userInstallOnly marks a user install in a server the bot has not joined.
func pressNavAs(t *testing.T, customID, user, guildID string, userInstallOnly bool, msg *discordgo.Message) []interactiontest.Call {
	t.Helper()
	fake := &interactiontest.Fake{}
	i := &discordgo.Interaction{Type: discordgo.InteractionMessageComponent, GuildID: guildID, ChannelID: "chan", Message: msg, User: &discordgo.User{ID: user},
		Data: discordgo.MessageComponentInteractionData{CustomID: customID, ComponentType: discordgo.ButtonComponent}}
	if userInstallOnly {
		i.AuthorizingIntegrationOwners = map[discordgo.ApplicationIntegrationType]string{discordgo.ApplicationIntegrationUserInstall: user}
	}
	ic := interaction.NewContext(fake, i, (*database.Client)(nil), interaction.Private, interaction.Direct)
	if err := handleNav(ic, nil); err != nil {
		t.Fatal(err)
	}
	calls := fake.Calls()
	if len(calls) == 0 {
		t.Fatalf("pressing %q answered nothing", customID)
	}
	return calls
}

func publicMsg() *discordgo.Message { return &discordgo.Message{ID: "m"} }

func buttons(t *testing.T, r interaction.Response) (prev, next discordgo.Button) {
	t.Helper()
	row := r.Components[len(r.Components)-1].(discordgo.ActionsRow)
	return row.Components[0].(discordgo.Button), row.Components[1].(discordgo.Button)
}

func openNavDay(t *testing.T, day string) interaction.Response {
	t.Helper()
	calls := runSchedule(t, day).Calls()
	return calls[len(calls)-1].Response
}

func TestNavigationCustomIDsCarryTheDateAndAuthorWithinDiscordsLimit(t *testing.T) {
	seedWeeks(t)
	prev, next := buttons(t, openNavDay(t, "sunday"))
	for _, b := range []discordgo.Button{prev, next} {
		if !strings.Contains(b.CustomID, "user") || len(b.CustomID) >= 100 {
			t.Errorf("custom ID %q must carry the author and stay under 100 characters", b.CustomID)
		}
	}
	if !strings.Contains(next.CustomID, "2026-11-09") || !strings.Contains(prev.CustomID, "2026-11-07") {
		t.Errorf("Sunday's buttons should carry the dates around it: prev %q next %q", prev.CustomID, next.CustomID)
	}
}

func TestNextOnASundayOpensTheFollowingMonday(t *testing.T) {
	seedWeeks(t)
	_, next := buttons(t, openNavDay(t, "sunday"))
	calls := pressNav(t, next.CustomID, "user", publicMsg())
	if len(calls) != 1 || calls[0].Op != "UpdateMessage" {
		t.Fatalf("want one UpdateMessage, got %+v", calls)
	}
	if title := calls[0].Response.Embeds[0].Title; !strings.HasPrefix(title, "Monday, 9 Nov 2026") {
		t.Errorf("title = %q, want Monday 9 Nov", title)
	}
}

func TestPreviousOnAMondayOpensTheSundayBefore(t *testing.T) {
	seedWeeks(t)
	prev, _ := buttons(t, openNavDay(t, "monday"))
	calls := pressNav(t, prev.CustomID, "user", publicMsg())
	// "monday" on Wednesday 4 Nov is the coming Monday, 9 Nov; the Sunday before it is 8 Nov.
	if title := calls[0].Response.Embeds[0].Title; !strings.HasPrefix(title, "Sunday, 8 Nov 2026") {
		t.Errorf("title = %q, want Sunday 8 Nov", title)
	}
}

func TestPreviousIsDisabledOnLastWeeksMondayAndNextOnNextWeeksSunday(t *testing.T) {
	seedWeeks(t)
	first := pressNav(t, "sched:"+loadedFirst+":user:sub", "user", publicMsg())[0].Response
	prev, next := buttons(t, first)
	if !prev.Disabled || next.Disabled {
		t.Errorf("first loaded day: prev disabled=%v next disabled=%v, want true/false", prev.Disabled, next.Disabled)
	}
	last := pressNav(t, "sched:"+loadedLast+":user:sub", "user", publicMsg())[0].Response
	prev, next = buttons(t, last)
	if prev.Disabled || !next.Disabled {
		t.Errorf("last loaded day: prev disabled=%v next disabled=%v, want false/true", prev.Disabled, next.Disabled)
	}
}

func TestPressForADateOutsideTheLoadedRangeIsRefusedPrivately(t *testing.T) {
	seedWeeks(t)
	for _, day := range []string{staleDay, "2026-11-16"} {
		calls := pressNav(t, "sched:"+day+":user:sub", "user", publicMsg())
		if len(calls) != 1 || calls[0].Visibility != interaction.Private || calls[0].Response.Content != msgDayNotLoaded {
			t.Errorf("%s: want the private refusal %q, got %+v", day, msgDayNotLoaded, calls)
		}
	}
}

// /schedule accepts an ISO date only inside the loaded range: last week's Monday to next week's Sunday.
func TestScheduleDayOutsideTheLoadedRangeIsRefusedPrivately(t *testing.T) {
	seedWeeks(t)
	for day, accepted := range map[string]bool{"2026-10-26": true, "2026-10-25": false, "2026-11-15": true, "2026-11-16": false, "2020-01-01": false} {
		calls := runSchedule(t, day).Calls()
		last := calls[len(calls)-1]
		if accepted {
			if last.Op != "EditReply" {
				t.Errorf("%s: want the page, got %+v", day, calls)
			}
			continue
		}
		if len(calls) != 3 || calls[1].Op != "DeleteReply" || last.Op != "FollowUp" || last.Visibility != interaction.Private || last.Response.Content != msgDayNotOnTimetable {
			t.Errorf("%s: want a private refusal %q, got last op %s with %d embeds", day, msgDayNotOnTimetable, last.Op, len(last.Response.Embeds))
		}
	}
}

func TestOnlyTheAuthorsPressUpdatesTheMessage(t *testing.T) {
	seedWeeks(t)
	id := "sched:2026-11-05:author:sub"

	calls := pressNav(t, id, "stranger", publicMsg())
	if len(calls) != 1 || calls[0].Op != "Reply" || calls[0].Visibility != interaction.Private {
		t.Fatalf("other member: want a private copy, got %+v", calls)
	}
	_, next := buttons(t, calls[0].Response)
	if !strings.HasSuffix(next.CustomID, ":author:sub") {
		t.Errorf("the copy's buttons %q should still page the author's data", next.CustomID)
	}

	private := &discordgo.Message{ID: "m", Flags: discordgo.MessageFlagsEphemeral}
	calls = pressNav(t, id, "stranger", private)
	if len(calls) != 1 || calls[0].Op != "UpdateMessage" {
		t.Errorf("press on a private copy: want it updated, got %+v", calls)
	}
}
