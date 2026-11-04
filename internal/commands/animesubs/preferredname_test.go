package animesubs

import (
	"errors"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
)

func nameChoice(value string) *discordgo.ApplicationCommandInteractionDataOption {
	return &discordgo.ApplicationCommandInteractionDataOption{Name: "name", Type: discordgo.ApplicationCommandOptionString, Value: value}
}

func TestNameSubcommandHasOneOptionalOptionOfTheFourPreferredNames(t *testing.T) {
	s := subcommand(t, "name")
	if s.Visibility != 0 || s.Response != 0 {
		t.Errorf("name overrides the private deferred reply: %v/%v", s.Visibility, s.Response)
	}
	if len(s.Options) != 1 {
		t.Fatalf("options = %+v, want one", s.Options)
	}
	o := s.Options[0]
	if o.Name != "name" || o.Type != discordgo.ApplicationCommandOptionString || o.Required || o.Autocomplete {
		t.Errorf("option = %+v, want an optional string option called name", o)
	}
	var got []string
	for _, c := range o.Choices {
		got = append(got, c.Name+"="+c.Value.(string))
	}
	if want := "Title=title,Romaji=romaji,English=english,Native=native"; strings.Join(got, ",") != want {
		t.Errorf("choices = %v, want %s", got, want)
	}
}

func TestNameSavesEachPreferredNameAndConfirmsIt(t *testing.T) {
	for _, tc := range []struct{ value, want string }{
		{"title", "Shows are now called by their title."},
		{"romaji", "Shows are now called by their Romaji name, else their title."},
		{"english", "Shows are now called by their English name, else their title."},
		{"native", "Shows are now called by their Native name, else their title."},
	} {
		t.Run(tc.value, func(t *testing.T) {
			st := newFakeStore().with("user", "Frieren")
			st.preferred["user"] = "romaji"
			useStore(t, st)
			r := reply(t, invoke(t, "name", nameChoice(tc.value)))
			if got := r.Embeds[0].Description; got != tc.want {
				t.Errorf("reply = %q, want %q", got, tc.want)
			}
			if st.preferred["user"] != tc.value {
				t.Errorf("stored %q, want %q", st.preferred["user"], tc.value)
			}
			if strings.Join(st.names("user"), ",") != "Frieren" {
				t.Errorf("subscriptions = %v, want them untouched", st.names("user"))
			}
		})
	}
}

func TestNameWithoutAChoiceStatesTheCurrentPreferredName(t *testing.T) {
	for _, tc := range []struct {
		name, stored string
		hasDoc       bool
		want         string
	}{
		{"no document", "", false, "Shows are called by their title."},
		{"no stored preferred name", "", true, "Shows are called by their title."},
		{"title", "title", true, "Shows are called by their title."},
		{"romaji", "romaji", true, "Shows are called by their Romaji name, else their title."},
		{"english", "english", true, "Shows are called by their English name, else their title."},
		{"native", "native", true, "Shows are called by their Native name, else their title."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newFakeStore()
			if tc.hasDoc {
				st.with("user", "Frieren")
			}
			if tc.stored != "" {
				st.preferred["user"] = tc.stored
			}
			useStore(t, st)
			r := reply(t, invoke(t, "name"))
			if got := r.Embeds[0].Description; got != tc.want {
				t.Errorf("reply = %q, want %q", got, tc.want)
			}
			if len(st.calls) != 0 {
				t.Errorf("stating the preferred name wrote %v", st.calls)
			}
		})
	}
}

func TestNameRefusesAFailedSave(t *testing.T) {
	st := newFakeStore()
	st.saveNameErr = errors.New("boom")
	useStore(t, st)
	r := reply(t, invoke(t, "name", nameChoice("english")))
	if got := r.Embeds[0].Description; got != "Could not save your preferred name." {
		t.Errorf("reply = %q, want the save failure", got)
	}
}

func TestNameRefusesAnUnreadableCurrentValue(t *testing.T) {
	st := newFakeStore()
	st.getErr = errors.New("boom")
	useStore(t, st)
	r := reply(t, invoke(t, "name"))
	if got := r.Embeds[0].Description; got != "Could not load your preferred name." {
		t.Errorf("reply = %q, want the load failure", got)
	}
}

func TestAMemberWithNoSubscriptionsCanSetAPreferredName(t *testing.T) {
	st := newFakeStore()
	useStore(t, st)
	reply(t, invoke(t, "name", nameChoice("native")))
	r := reply(t, invoke(t, "name"))
	if got := r.Embeds[0].Description; got != "Shows are called by their Native name, else their title." {
		t.Errorf("reply = %q, want Native kept for a member with no subscriptions", got)
	}
	if got := reply(t, invoke(t, "list")); !strings.Contains(text(got), "no active") {
		t.Errorf("list = %q, want no subscriptions", text(got))
	}
}

func TestClearKeepsThePreferredName(t *testing.T) {
	removeID := clearButtonID(t, "Remove all")
	st := newFakeStore().with("user", "A", "B")
	st.preferred["user"] = "english"
	useStore(t, st)
	if err := handleClearButton(press("user", removeID), nil); err != nil {
		t.Fatal(err)
	}
	if len(st.docs["user"]) != 0 {
		t.Fatalf("left %v", st.names("user"))
	}
	r := reply(t, invoke(t, "name"))
	if got := r.Embeds[0].Description; got != "Shows are called by their English name, else their title." {
		t.Errorf("after clear, reply = %q, want English kept", got)
	}
}
