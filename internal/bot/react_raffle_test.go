package bot

import (
	"context"
	"slices"
	"testing"

	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/database/guilds"

	"github.com/bwmarrin/discordgo"
)

// fakeReactRaffleStore is an in-memory store for the reaction raffle handlers.
type fakeReactRaffleStore struct {
	raffles []*guilds.Raffle
}

func (f *fakeReactRaffleStore) GetRaffles(context.Context, string) ([]*guilds.Raffle, error) {
	return f.raffles, nil
}

func (f *fakeReactRaffleStore) UpdateRaffleParticipant(_ context.Context, _, name, userID string, remove bool) (bool, error) {
	for _, r := range f.raffles {
		if r.Name != name {
			continue
		}
		if remove {
			r.ParticipantIDs = slices.DeleteFunc(r.ParticipantIDs, func(id string) bool { return id == userID })
		} else if !slices.Contains(r.ParticipantIDs, userID) {
			r.ParticipantIDs = append(r.ParticipantIDs, userID)
		}
		return true, nil
	}
	return false, nil
}

func useReactRaffleStore(t *testing.T, st *fakeReactRaffleStore) {
	t.Helper()
	old := reactRaffleStoreOf
	reactRaffleStoreOf = func(*database.Client) reactRaffleStore { return st }
	t.Cleanup(func() { reactRaffleStoreOf = old })
}

func botSession() *discordgo.Session {
	s := &discordgo.Session{State: discordgo.NewState()}
	s.State.User = &discordgo.User{ID: "bot"}
	return s
}

func reactionAdd(guildID, messageID, userID, emoji string) *discordgo.MessageReactionAdd {
	return &discordgo.MessageReactionAdd{MessageReaction: &discordgo.MessageReaction{
		GuildID: guildID, MessageID: messageID, UserID: userID, Emoji: discordgo.Emoji{Name: emoji},
	}}
}

func reactionRemove(guildID, messageID, userID, emoji string) *discordgo.MessageReactionRemove {
	return &discordgo.MessageReactionRemove{MessageReaction: &discordgo.MessageReaction{
		GuildID: guildID, MessageID: messageID, UserID: userID, Emoji: discordgo.Emoji{Name: emoji},
	}}
}

func reactRaffles() *fakeReactRaffleStore {
	return &fakeReactRaffleStore{raffles: []*guilds.Raffle{
		{Name: "Legacy", ReactMessageID: "react-msg", ParticipantIDs: []string{"early"}},
		{Name: "Buttons", MessageID: "button-msg"},
	}}
}

func TestSlotReactionEntersTheReactionRaffle(t *testing.T) {
	st := reactRaffles()
	useReactRaffleStore(t, st)
	OnMessageReactionAdd(t.Context(), nil)(botSession(), reactionAdd("g", "react-msg", "user", "🎰"))
	if !slices.Equal(st.raffles[0].ParticipantIDs, []string{"early", "user"}) {
		t.Errorf("entrants = %v", st.raffles[0].ParticipantIDs)
	}
}

func TestRemovingTheSlotReactionLeavesTheReactionRaffle(t *testing.T) {
	st := reactRaffles()
	useReactRaffleStore(t, st)
	OnMessageReactionRemove(t.Context(), nil)(botSession(), reactionRemove("g", "react-msg", "early", "🎰"))
	if len(st.raffles[0].ParticipantIDs) != 0 {
		t.Errorf("entrants = %v", st.raffles[0].ParticipantIDs)
	}
}

func TestSlotReactionsOnOtherMessagesChangeNoRaffle(t *testing.T) {
	st := reactRaffles()
	useReactRaffleStore(t, st)
	OnMessageReactionAdd(t.Context(), nil)(botSession(), reactionAdd("g", "other-msg", "user", "🎰"))
	// A button raffle's message is not a reaction message.
	OnMessageReactionAdd(t.Context(), nil)(botSession(), reactionAdd("g", "button-msg", "user", "🎰"))
	OnMessageReactionRemove(t.Context(), nil)(botSession(), reactionRemove("g", "other-msg", "early", "🎰"))
	if !slices.Equal(st.raffles[0].ParticipantIDs, []string{"early"}) || len(st.raffles[1].ParticipantIDs) != 0 {
		t.Errorf("entrants = %v and %v", st.raffles[0].ParticipantIDs, st.raffles[1].ParticipantIDs)
	}
}

func TestOtherEmojiAreNotSlotReactionsAndChangeNoEntrants(t *testing.T) {
	st := reactRaffles()
	for _, emoji := range []string{"👍", "🎰🎰", ""} {
		if handleSlotReaction(t.Context(), st, "g", "react-msg", "user", emoji, false) {
			t.Errorf("%q was treated as the slot emoji", emoji)
		}
	}
	if !slices.Equal(st.raffles[0].ParticipantIDs, []string{"early"}) {
		t.Errorf("entrants = %v", st.raffles[0].ParticipantIDs)
	}
}

func TestTheHandlersIgnoreOtherGuildsAndTheBotsOwnReaction(t *testing.T) {
	st := reactRaffles()
	useReactRaffleStore(t, st)
	OnMessageReactionAdd(t.Context(), nil)(botSession(), reactionAdd("", "react-msg", "user", "🎰"))
	OnMessageReactionAdd(t.Context(), nil)(botSession(), reactionAdd("g", "react-msg", "bot", "🎰"))
	OnMessageReactionRemove(t.Context(), nil)(botSession(), reactionRemove("g", "react-msg", "bot", "🎰"))
	if !slices.Equal(st.raffles[0].ParticipantIDs, []string{"early"}) {
		t.Errorf("entrants = %v", st.raffles[0].ParticipantIDs)
	}
}
