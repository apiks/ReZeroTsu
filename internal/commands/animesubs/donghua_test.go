package animesubs

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"ReZeroTsu/internal/animeschedule"
	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/database/anime_subs"
	"ReZeroTsu/internal/database/guilds"
	"ReZeroTsu/internal/interaction"
	"ReZeroTsu/internal/interaction/interactiontest"

	"github.com/bwmarrin/discordgo"
)

// TestMain keeps every test off the database: a server that is not under test shows donghua, as an unset server does.
func TestMain(m *testing.M) {
	guildSettings = func(context.Context, *database.Client, string) (*guilds.GuildSettings, error) {
		return &guilds.GuildSettings{Donghua: true}, nil
	}
	os.Exit(m.Run())
}

// where is how /subscription add is invoked: in a server the bot is a member of, in a DM, or from a user install in a
// server the bot has not joined.
type where int

const (
	inServer where = iota
	inDM
	inUnjoinedServer
)

func (w where) String() string {
	return [...]string{"a server", "a DM", "a server the bot has not joined"}[w]
}

// interactionAt is an interaction made by "user" at w.
func interactionAt(w where) *discordgo.Interaction {
	i := &discordgo.Interaction{ChannelID: "c", User: &discordgo.User{ID: "user"}}
	switch w {
	case inServer:
		i.GuildID = "g"
		i.Member = &discordgo.Member{User: i.User}
		i.User = nil
	case inUnjoinedServer:
		i.GuildID = "g"
		i.AuthorizingIntegrationOwners = map[discordgo.ApplicationIntegrationType]string{discordgo.ApplicationIntegrationUserInstall: "user"}
	}
	return i
}

// serverDonghua replaces the settings lookup with a server that shows or hides donghua (or fails to read), and
// reports how often it was read.
func serverDonghua(t *testing.T, shown bool, readErr error) *int {
	t.Helper()
	reads := 0
	prev := guildSettings
	guildSettings = func(context.Context, *database.Client, string) (*guilds.GuildSettings, error) {
		reads++
		if readErr != nil {
			return nil, readErr
		}
		return &guilds.GuildSettings{Donghua: shown}, nil
	}
	t.Cleanup(func() { guildSettings = prev })
	return &reads
}

// suggestionsAt asks /subscription add for suggestions as user at w.
func suggestionsAt(t *testing.T, w where, st subsStore, typed string) []string {
	t.Helper()
	old := storeOf
	storeOf = func(*database.Client) subsStore { return st }
	t.Cleanup(func() { storeOf = old })
	i := interactionAt(w)
	i.Type = discordgo.InteractionApplicationCommandAutocomplete
	i.Data = discordgo.ApplicationCommandInteractionData{Name: "subscription", Options: []*discordgo.ApplicationCommandInteractionDataOption{
		{Name: "add", Type: discordgo.ApplicationCommandOptionSubCommand, Options: []*discordgo.ApplicationCommandInteractionDataOption{
			{Name: "anime", Type: discordgo.ApplicationCommandOptionString, Value: typed, Focused: true}}}}}
	return names(subcommand(t, "add").AutocompleteOption(t.Context(), nil, nil, &discordgo.InteractionCreate{Interaction: i}, "anime", typed))
}

// addAt runs /subscription add for typed at w and returns the reply text.
func addAt(t *testing.T, w where, typed string) string {
	t.Helper()
	fake := &interactiontest.Fake{}
	cmd := subscriptionCommand(t)
	i := interactionAt(w)
	i.Type = discordgo.InteractionApplicationCommand
	i.Data = discordgo.ApplicationCommandInteractionData{Name: "subscription", Options: animeOption(typed)}
	ic := interaction.NewContext(fake, i, nil, cmd.Visibility, cmd.Response)
	if err := ic.Defer(0); err != nil {
		t.Fatal(err)
	}
	if err := subcommand(t, "add").Run(ic); err != nil {
		t.Fatal(err)
	}
	return text(reply(t, fake))
}

// seedDonghua loads one show of each kind a server may hide: a donghua on the timetable, a Chinese TV series the
// timetable flags as donghua, an upcoming donghua, and plain shows to stay.
func seedDonghua(t *testing.T) {
	t.Helper()
	soulLand := entry("Soul Land", "soul-land", "Ep 2", at(time.Hour))
	soulLand.Donghua, soulLand.MediaType = true, animeschedule.MediaDonghua
	chineseTV := entry("Martial Peak", "martial-peak", "Ep 3", at(2*time.Hour))
	chineseTV.Donghua = true // a Chinese TV series decodes as TV but the timetable flags it
	seedUpcoming(t, []animeschedule.UpcomingShow{
		{Name: "Upcoming Donghua", Route: "upcoming-donghua", Donghua: true, MediaType: animeschedule.MediaDonghua},
		{Name: "Upcoming Chinese Movie", Route: "upcoming-chinese-movie", Donghua: true},
		{Name: "Upcoming Plain", Route: "upcoming-plain"},
	}, entry("Airing Plain", "airing-plain", "Ep 2", at(3*time.Hour)), soulLand, chineseTV)
}

var allDonghuaOffered = "Airing Plain|Soul Land · Donghua|Martial Peak|Upcoming Donghua · premiere date TBA · Donghua|Upcoming Chinese Movie · premiere date TBA|Upcoming Plain · premiere date TBA"

func TestSuggestionsLeaveOutEveryDonghuaInAServerThatHidesDonghua(t *testing.T) {
	seedDonghua(t)
	serverDonghua(t, false, nil)
	// The member already follows a donghua and a plain show: the donghua still goes, the plain show stays.
	st := newFakeStore().with("user", "Soul Land", "Airing Plain")
	got := strings.Join(suggestionsAt(t, inServer, st, ""), "|")
	// The shows not followed come first and the followed ones last.
	if want := "Upcoming Plain · premiere date TBA|Airing Plain · subscribed"; got != want {
		t.Errorf("suggestions = %q, want %q", got, want)
	}
}

func TestSuggestionsOfferDonghuaWhereItIsNotHidden(t *testing.T) {
	for _, tc := range []struct {
		name      string
		w         where
		shown     bool
		readErr   error
		wantReads int
	}{
		{"server that shows donghua", inServer, true, nil, 1},
		{"server whose settings cannot be read", inServer, false, errors.New("mongo down"), 1},
		{"DM, whatever any server sets", inDM, false, nil, 0},
		{"user install in a server the bot has not joined", inUnjoinedServer, false, nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seedDonghua(t)
			reads := serverDonghua(t, tc.shown, tc.readErr)
			got := strings.Join(suggestionsAt(t, tc.w, newFakeStore(), ""), "|")
			if got != allDonghuaOffered {
				t.Errorf("suggestions = %q\nwant %q", got, allDonghuaOffered)
			}
			if *reads != tc.wantReads {
				t.Errorf("settings read %d times, want %d", *reads, tc.wantReads)
			}
		})
	}
}

func TestTypedNameResolvesNoDonghuaInAServerThatHidesDonghua(t *testing.T) {
	commands.SetCommandIDs([]*discordgo.ApplicationCommand{{ID: "123", Name: "subscription", Type: discordgo.ChatApplicationCommand}})
	t.Cleanup(func() { commands.SetCommandIDs(nil) })
	const pointer = "This server hides donghua. Run </subscription add:123> in a DM with ZeroTsu to follow it."
	for _, typed := range []string{"Soul Land", "soul-land", "Martial Peak", "Upcoming Donghua", "upcoming-chinese-movie"} {
		t.Run(typed, func(t *testing.T) {
			seedDonghua(t)
			serverDonghua(t, false, nil)
			st := newFakeStore().with("user", "Soul Land")
			useStore(t, st)
			before := len(st.docs["user"])
			got := addAt(t, inServer, typed)
			if !strings.Contains(got, "I could not find that airing or upcoming show") || !strings.Contains(got, pointer) {
				t.Errorf("reply %q lacks the not-found message and %q", got, pointer)
			}
			if len(st.docs["user"]) != before || len(st.calls) != 0 {
				t.Errorf("a hidden donghua changed the store: docs=%v calls=%v", st.names("user"), st.calls)
			}
		})
	}
}

func TestTypedNameGetsNoDMPointerWhenItMatchesNothingOrAShownShow(t *testing.T) {
	seedDonghua(t)
	serverDonghua(t, false, nil)
	useStore(t, newFakeStore())
	if got := addAt(t, inServer, "No Such Show"); !strings.Contains(got, "could not find") || strings.Contains(got, "hides donghua") {
		t.Errorf("an unknown name: reply %q must be the plain not-found message", got)
	}
	if got := addAt(t, inServer, "Upcoming Plain"); !strings.Contains(got, "Subscribed to **Upcoming Plain**") {
		t.Errorf("a plain show in a server that hides donghua: reply %q", got)
	}
}

func TestTypedDonghuaNameSubscribesWhereDonghuaIsOffered(t *testing.T) {
	for _, tc := range []struct {
		name    string
		w       where
		shown   bool
		readErr error
	}{
		{"server that shows donghua", inServer, true, nil},
		{"server whose settings cannot be read", inServer, false, errors.New("mongo down")},
		{"DM", inDM, false, nil},
		{"user install in a server the bot has not joined", inUnjoinedServer, false, nil},
	} {
		for _, typed := range []string{"Soul Land", "Upcoming Donghua"} {
			t.Run(tc.name+"/"+typed, func(t *testing.T) {
				seedDonghua(t)
				serverDonghua(t, tc.shown, tc.readErr)
				st := newFakeStore()
				useStore(t, st)
				if got := addAt(t, tc.w, typed); !strings.Contains(got, "Subscribed to **"+typed+"**") {
					t.Errorf("reply %q, want a subscription to %s", got, typed)
				}
				if len(st.docs["user"]) != 1 {
					t.Errorf("stored %v, want the donghua", st.names("user"))
				}
			})
		}
	}
}

// A followed donghua is never touched by the server's setting: list, remove, the Remove picker and clear ignore it.
func TestFollowedDonghuaStaysManageableInAServerThatHidesDonghua(t *testing.T) {
	seedDonghua(t)
	serverDonghua(t, false, nil)
	st := newFakeStore().with("user", "Soul Land", "Airing Plain")
	useStore(t, st)

	if got := text(reply(t, invoke(t, "list"))); !strings.Contains(got, "Soul Land") {
		t.Errorf("list reply %q lacks the followed donghua", got)
	}
	picker := removePicker(docToSubList(&anime_subs.AnimeSubs{Shows: st.docs["user"]}), 0, "user")
	if opts := picker.(discordgo.ActionsRow).Components[0].(discordgo.SelectMenu).Options; len(opts) != 2 || opts[0].Value != "Soul Land" {
		t.Errorf("Remove picker options = %+v, want both followed shows", opts)
	}
	if got := text(reply(t, invoke(t, "remove", animeOption("Soul Land")...))); !strings.Contains(got, "unsubscribed from `Soul Land`") {
		t.Errorf("remove reply %q", got)
	}
	if got := strings.Join(st.names("user"), ","); got != "Airing Plain" {
		t.Errorf("after remove: %q", got)
	}
	st.with("user", "Soul Land")
	if r := reply(t, invoke(t, "clear")); len(r.Components) == 0 || !strings.Contains(text(r), "Remove all 2 subscriptions?") {
		t.Errorf("clear reply = %q, want the confirmation", text(r))
	}
}
