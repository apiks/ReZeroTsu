package schedule

import (
	"context"
	"errors"
	"testing"

	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/database/anime_subs"
	"ReZeroTsu/internal/database/guilds"
	"ReZeroTsu/internal/interaction"
	"ReZeroTsu/internal/interaction/interactiontest"

	"github.com/bwmarrin/discordgo"
)

// memberNames replaces the member lookup with members whose stored preferred names are stored (a member missing from
// it has no document), or a failed read when err is set, and returns the reads per member.
func memberNames(t *testing.T, stored map[string]string, err error) map[string]int {
	t.Helper()
	reads := map[string]int{}
	prev := memberSubs
	memberSubs = func(_ context.Context, _ *database.Client, userID string) (*anime_subs.AnimeSubs, error) {
		reads[userID]++
		if err != nil {
			return nil, err
		}
		name, ok := stored[userID]
		if !ok {
			return nil, nil
		}
		return &anime_subs.AnimeSubs{ID: userID, PreferredName: name}, nil
	}
	t.Cleanup(func() { memberSubs = prev })
	return reads
}

// namesUnder is the two shows' names a page reads under each stored preferred name.
var namesUnder = map[string][2]string{
	"":        {"Frieren Title", "Apothecary Title"},
	"title":   {"Frieren Title", "Apothecary Title"},
	"romaji":  {"Sousou no Frieren", "Kusuriya no Hitorigoto"},
	"english": {"Frieren: Beyond Journey's End", "Apothecary Title"},
	"native":  {"葬送のフリーレン", "薬屋のひとりごと"},
}

func TestScheduleWithoutServerMembershipCallsShowsByTheMembersPreferredName(t *testing.T) {
	for _, stored := range []string{"", "title", "romaji", "english", "native"} {
		t.Run("preferred name "+stored, func(t *testing.T) {
			seedNamedShows(t)
			// The server's setting is never read without server membership.
			serverReads := serverNames(t, &guilds.GuildSettings{Donghua: true, PreferredName: "romaji"}, nil)
			reads := memberNames(t, map[string]string{"user": stored}, nil)
			want := namesUnder[stored]
			wantNames(t, "DM", runScheduleAs(t, "", false), want[0], want[1])
			wantNames(t, "user install", runScheduleAs(t, "guild", true), want[0], want[1])
			if *serverReads != 0 || reads["user"] != 2 {
				t.Errorf("server reads %d, member reads %d; want 0 and one per page", *serverReads, reads["user"])
			}
		})
	}
}

func TestScheduleInAServerFollowsTheServersPreferredNameNotTheMembers(t *testing.T) {
	seedNamedShows(t)
	serverNames(t, &guilds.GuildSettings{Donghua: true, PreferredName: "english"}, nil)
	reads := memberNames(t, map[string]string{"user": "native", "stranger": "native"}, nil)
	wantNames(t, "/schedule", runScheduleAs(t, "guild", false), "Frieren: Beyond Journey's End", "Apothecary Title")
	calls := pressNavIn(t, "sched:2026-11-05:user:sub", "stranger", "guild", publicMsg())
	wantNames(t, "another member's press", calls[0].Response.Embeds[0], "Frieren: Beyond Journey's End", "Apothecary Title")
	if len(reads) != 0 {
		t.Errorf("member preferred names were read in a server: %v", reads)
	}
}

func TestScheduleInAServerWithUnreadableSettingsShowsTitlesNotTheMembersName(t *testing.T) {
	seedNamedShows(t)
	serverNames(t, nil, errors.New("boom"))
	memberNames(t, map[string]string{"user": "native"}, nil)
	wantNames(t, "/schedule", runScheduleAs(t, "guild", false), "Frieren Title", "Apothecary Title")
}

// Where there is no server setting, a page press reads the presser's preferred name: the author's press updates the
// page with the author's, and another member's private copy uses their own.
func TestSchedulePagePressesWithoutAServerSettingReadThePressersPreferredName(t *testing.T) {
	for _, where := range []struct {
		name    string
		guildID string
	}{{"DM", ""}, {"user install", "guild"}} {
		t.Run(where.name, func(t *testing.T) {
			seedNamedShows(t)
			memberNames(t, map[string]string{"user": "native", "stranger": "english"}, nil)
			userInstall := where.guildID != ""
			press := func(user string, msg *discordgo.Message) []interactiontest.Call {
				return pressNavAs(t, "sched:2026-11-05:user:sub", user, where.guildID, userInstall, msg)
			}

			calls := press("user", publicMsg())
			if len(calls) != 1 || calls[0].Op != "UpdateMessage" {
				t.Fatalf("author's press: want the page updated, got %+v", calls)
			}
			wantNames(t, "author's press", calls[0].Response.Embeds[0], "葬送のフリーレン", "薬屋のひとりごと")

			calls = press("stranger", publicMsg())
			if len(calls) != 1 || calls[0].Op != "Reply" || calls[0].Visibility != interaction.Private {
				t.Fatalf("another member's press: want a private copy, got %+v", calls)
			}
			wantNames(t, "another member's copy", calls[0].Response.Embeds[0], "Frieren: Beyond Journey's End", "Apothecary Title")
			if _, next := buttons(t, calls[0].Response); next.CustomID != "sched:2026-11-06:user:sub" {
				t.Errorf("the copy's Next is %q, want the unchanged custom ID", next.CustomID)
			}
		})
	}
}

func TestScheduleShowsTitlesWhenTheMembersPreferredNameCannotBeRead(t *testing.T) {
	seedNamedShows(t)
	memberNames(t, nil, errors.New("boom"))
	wantNames(t, "DM", runScheduleAs(t, "", false), "Frieren Title", "Apothecary Title")
	calls := pressNavIn(t, "sched:2026-11-05:user:sub", "user", "", publicMsg())
	wantNames(t, "author's press", calls[0].Response.Embeds[0], "Frieren Title", "Apothecary Title")
}
