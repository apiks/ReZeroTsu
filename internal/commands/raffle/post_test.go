package raffle

import (
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"ReZeroTsu/internal/database/guilds"
	"ReZeroTsu/internal/interaction"
	"ReZeroTsu/internal/interaction/interactiontest"

	"github.com/bwmarrin/discordgo"
)

func nameOption(value string) *discordgo.ApplicationCommandInteractionDataOption {
	return &discordgo.ApplicationCommandInteractionDataOption{Name: "raffle", Type: discordgo.ApplicationCommandOptionString, Value: value}
}

func TestPostTakesARaffleNameThatAutocompletes(t *testing.T) {
	post := subcommand(t, "post")
	if len(post.Options) != 1 || post.Options[0].Name != "raffle" || !post.Options[0].Required || !post.Options[0].Autocomplete || post.AutocompleteOption == nil {
		t.Errorf("post options = %+v", post.Options)
	}
}

func TestPostPutsAButtonMessageOnARaffleThatHasNone(t *testing.T) {
	st := useManage(t, &guilds.Raffle{Name: "Grand Raffle", ParticipantIDs: []string{"a", "b"}, WinnerID: "w"})
	fake := invokeManage(t, "post", nameOption("Grand Raffle"))

	posted := lastCall(t, fake, "PostMessage")
	body := text(posted.Response)
	if posted.ChannelID != "c" || !strings.Contains(body, "Grand Raffle") || !strings.Contains(body, "2") || !strings.Contains(body, "<@w>") {
		t.Errorf("posted %+v", posted)
	}
	if got := buttonIDs(posted.Response); strings.Join(got, ",") != "raffle:enter,raffle:leave" {
		t.Errorf("buttons = %v", got)
	}
	if slices.Contains(fake.Ops(), "EditMessage") {
		t.Errorf("there was no earlier message to edit: %v", fake.Ops())
	}
	got := st.raffles[0]
	if got.ChannelID != "c" || got.MessageID != "posted" || !slices.Equal(got.ParticipantIDs, []string{"a", "b"}) || got.WinnerID != "w" {
		t.Errorf("stored raffle = %+v", got)
	}
	if reply := lastCall(t, fake, "EditReply").Response; !strings.Contains(text(reply), "https://discord.com/channels/g/c/posted") {
		t.Errorf("reply %q has no link", text(reply))
	}
	if first := fake.Calls()[0]; first.Op != "Defer" || first.Visibility != interaction.Private {
		t.Errorf("first call = %+v, want a private Defer", first)
	}
}

func TestPostMovesAnExistingButtonMessageAndMarksTheOldOneMoved(t *testing.T) {
	st := useManage(t, &guilds.Raffle{Name: "Grand Raffle", ParticipantIDs: []string{"a"}, ChannelID: "c9", MessageID: "old"})
	fake := invokeManage(t, "post", nameOption("grand raffle"))

	edit := lastCall(t, fake, "EditMessage")
	if edit.ChannelID != "c9" || edit.MessageID != "old" || !strings.Contains(text(edit.Response), "This raffle moved: https://discord.com/channels/g/c/posted") {
		t.Errorf("old message edit = %+v", edit)
	}
	if ids := buttonIDs(edit.Response); len(ids) != 2 {
		t.Fatalf("old message buttons = %v", ids)
	}
	for _, row := range edit.Response.Components {
		for _, c := range row.(discordgo.ActionsRow).Components {
			if !c.(discordgo.Button).Disabled {
				t.Errorf("button %+v is enabled on the moved message", c)
			}
		}
	}
	got := st.raffles[0]
	if got.ChannelID != "c" || got.MessageID != "posted" || !slices.Equal(got.ParticipantIDs, []string{"a"}) {
		t.Errorf("stored raffle = %+v", got)
	}
	if st.raffles[0].Name != "Grand Raffle" || len(st.raffles) != 1 {
		t.Errorf("stored raffles = %+v", st.raffles)
	}
}

func TestPostOnALegacyReactionRaffleKeepsItsEntrantsAndReactionMessage(t *testing.T) {
	st := useManage(t, &guilds.Raffle{Name: "Old", ParticipantIDs: []string{"a", "b", "c"}, ReactMessageID: "react1"})
	fake := invokeManage(t, "post", nameOption("Old"))
	got := st.raffles[0]
	if got.ReactMessageID != "react1" || len(got.ParticipantIDs) != 3 || got.MessageID != "posted" {
		t.Errorf("stored raffle = %+v", got)
	}
	if slices.Contains(fake.Ops(), "EditMessage") || len(st.fakeStore.updateLog) != 0 {
		t.Errorf("ops %v, entrant updates %v: the reaction message and entrants are untouched", fake.Ops(), st.updateLog)
	}
}

func TestPostRefusesAnUnknownRaffleWithoutPosting(t *testing.T) {
	useManage(t, &guilds.Raffle{Name: "Grand Raffle"})
	fake := invokeManage(t, "post", nameOption("nope"))
	if slices.Contains(fake.Ops(), "PostMessage") {
		t.Errorf("ops %v", fake.Ops())
	}
	if got := text(lastCall(t, fake, "EditReply").Response); !strings.Contains(got, "No such raffle") {
		t.Errorf("reply %q", got)
	}
}

func TestPostExplainsAMissingPermissionAndStoresNothing(t *testing.T) {
	st := useManage(t, &guilds.Raffle{Name: "Grand Raffle", ChannelID: "c9", MessageID: "old"})
	fake := &interactiontest.Fake{Fail: map[string]error{"PostMessage": &discordgo.RESTError{Response: &http.Response{StatusCode: 403}}}}
	runManage(t, fake, "post", nameOption("Grand Raffle"))
	got := text(lastCall(t, fake, "EditReply").Response)
	if !strings.Contains(got, "<#c>") || !strings.Contains(got, "Send Messages") || st.raffles[0].MessageID != "old" {
		t.Errorf("reply %q, message %q", got, st.raffles[0].MessageID)
	}
	if slices.Contains(fake.Ops(), "EditMessage") {
		t.Error("the old message must keep its buttons when the new one could not be posted")
	}
}

func TestPostRemovesTheMessageItPostedWhenTheRaffleCannotBeUpdated(t *testing.T) {
	st := useManage(t, &guilds.Raffle{Name: "Grand Raffle", ChannelID: "c9", MessageID: "old"})
	st.failMessage = errors.New("db down")
	fake := invokeManage(t, "post", nameOption("Grand Raffle"))
	if del := lastCall(t, fake, "DeleteMessage"); del.ChannelID != "c" || del.MessageID != "posted" {
		t.Errorf("deleted %+v", del)
	}
	if slices.Contains(fake.Ops(), "EditMessage") {
		t.Error("the old message must keep its buttons when the new one is not stored")
	}
	if got := text(lastCall(t, fake, "EditReply").Response); got != interaction.SomethingWentWrong {
		t.Errorf("reply %q", got)
	}
}

func TestAFailedEditOfTheOldMessageDoesNotFailThePost(t *testing.T) {
	st := useManage(t, &guilds.Raffle{Name: "Grand Raffle", ChannelID: "c9", MessageID: "old"})
	fake := &interactiontest.Fake{Fail: map[string]error{"EditMessage": errors.New("unknown message")}}
	runManage(t, fake, "post", nameOption("Grand Raffle"))
	if st.raffles[0].MessageID != "posted" {
		t.Errorf("stored message %q", st.raffles[0].MessageID)
	}
	if got := text(lastCall(t, fake, "EditReply").Response); !strings.Contains(got, "channels/g/c/posted") {
		t.Errorf("reply %q", got)
	}
}

func TestPostAutocompleteMatchesAnywhereInTheName(t *testing.T) {
	useManage(t, &guilds.Raffle{Name: "Summer Grand Raffle"}, &guilds.Raffle{Name: "Winter"})
	i := &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{Type: discordgo.InteractionApplicationCommandAutocomplete, GuildID: "g"}}
	got := subcommand(t, "post").AutocompleteOption(t.Context(), nil, nil, i, "raffle", "GRAND")
	if len(got) != 1 || got[0].Value != "Summer Grand Raffle" {
		t.Errorf("choices = %+v", got)
	}
	if all := subcommand(t, "post").AutocompleteOption(t.Context(), nil, nil, i, "raffle", ""); len(all) != 2 {
		t.Errorf("empty input offered %+v", all)
	}
}
