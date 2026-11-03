package commands

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/interaction"
	"ReZeroTsu/internal/interaction/interactiontest"

	"github.com/bwmarrin/discordgo"
)

func definition(t *testing.T, name string) *discordgo.ApplicationCommand {
	t.Helper()
	for _, d := range Definitions() {
		if d.Name == name {
			return d
		}
	}
	t.Fatalf("no definition named %q", name)
	return nil
}

func noRun(*interaction.Context) error { return nil }

func TestFlatCommandsRegisterExactlyAsBefore(t *testing.T) {
	register(t, &Command{Name: "t-flat", Desc: "d", Visibility: Public, Response: Direct, Run: noRun,
		Options: []*discordgo.ApplicationCommandOption{{Name: "o", Description: "od", Type: discordgo.ApplicationCommandOptionString}}})
	register(t, &Command{Name: "t-flat-admin", Desc: "d", Permission: PermMod, Context: ContextGuildOnly, Visibility: Private, Response: Direct, Run: noRun})

	manageServer := int64(discordgo.PermissionManageGuild)
	serverOnly := &[]discordgo.ApplicationIntegrationType{discordgo.ApplicationIntegrationGuildInstall}
	want := &discordgo.ApplicationCommand{Name: "t-flat", Description: "d", IntegrationTypes: serverOnly,
		Contexts: &[]discordgo.InteractionContextType{discordgo.InteractionContextGuild, discordgo.InteractionContextBotDM},
		Options:  []*discordgo.ApplicationCommandOption{{Name: "o", Description: "od", Type: discordgo.ApplicationCommandOptionString}}}
	if got := definition(t, "t-flat"); !reflect.DeepEqual(got, want) {
		t.Errorf("flat = %+v, want %+v", got, want)
	}
	wantMod := &discordgo.ApplicationCommand{Name: "t-flat-admin", Description: "d", DefaultMemberPermissions: &manageServer, IntegrationTypes: serverOnly,
		Contexts: &[]discordgo.InteractionContextType{discordgo.InteractionContextGuild}}
	if got := definition(t, "t-flat-admin"); !reflect.DeepEqual(got, wantMod) {
		t.Errorf("admin = %+v, want %+v", got, wantMod)
	}
}

func TestGroupsAndSubcommandsRegisterAsDeclared(t *testing.T) {
	strOpt := &discordgo.ApplicationCommandOption{Name: "show", Description: "s", Type: discordgo.ApplicationCommandOptionString, Autocomplete: true}
	register(t, &Command{Name: "t-tree", Desc: "tree", Visibility: Private, Response: Direct,
		Subcommands: []*Subcommand{
			{Name: "add", Desc: "add one", Options: []*discordgo.ApplicationCommandOption{strOpt}, Run: noRun},
			{Name: "list", Desc: "list all", Run: noRun},
		},
		Groups: []*SubcommandGroup{{Name: "admin", Desc: "admin things", Subcommands: []*Subcommand{{Name: "purge", Desc: "purge", Run: noRun}}}},
	})

	got := definition(t, "t-tree").Options
	if len(got) != 3 {
		t.Fatalf("options = %d, want 3", len(got))
	}
	if got[0].Type != discordgo.ApplicationCommandOptionSubCommand || got[0].Name != "add" || got[0].Description != "add one" ||
		len(got[0].Options) != 1 || got[0].Options[0] != strOpt {
		t.Errorf("add = %+v", got[0])
	}
	if got[1].Type != discordgo.ApplicationCommandOptionSubCommand || got[1].Name != "list" || len(got[1].Options) != 0 {
		t.Errorf("list = %+v", got[1])
	}
	g := got[2]
	if g.Type != discordgo.ApplicationCommandOptionSubCommandGroup || g.Name != "admin" || len(g.Options) != 1 ||
		g.Options[0].Type != discordgo.ApplicationCommandOptionSubCommand || g.Options[0].Name != "purge" {
		t.Errorf("group = %+v", g)
	}
}

func TestRightClickKindsRegisterWithTheirType(t *testing.T) {
	register(t, &Command{Name: "T user", Kind: KindUser, Visibility: Private, Response: Direct, Run: noRun})
	register(t, &Command{Name: "T message", Kind: KindMessage, Visibility: Private, Response: Direct, Run: noRun})
	if d := definition(t, "T user"); d.Type != discordgo.UserApplicationCommand || d.Description != "" || len(d.Options) != 0 {
		t.Errorf("user = %+v", d)
	}
	if d := definition(t, "T message"); d.Type != discordgo.MessageApplicationCommand || d.Description != "" {
		t.Errorf("message = %+v", d)
	}
}

func TestDefaultPermissionsInstallTypesAndContextsRegisterAsDeclared(t *testing.T) {
	manage := int64(discordgo.PermissionManageGuild)
	register(t, &Command{Name: "t-mod", Desc: "d", Visibility: Private, Response: Direct, Run: noRun,
		DefaultMemberPermissions: &manage, Installs: []discordgo.ApplicationIntegrationType{discordgo.ApplicationIntegrationGuildInstall}, Context: ContextGuildOnly})
	register(t, &Command{Name: "t-both", Desc: "d", Visibility: Private, Response: Direct, Run: noRun,
		Installs: []discordgo.ApplicationIntegrationType{discordgo.ApplicationIntegrationGuildInstall, discordgo.ApplicationIntegrationUserInstall}, Context: ContextBoth})

	mod := definition(t, "t-mod")
	if mod.DefaultMemberPermissions == nil || *mod.DefaultMemberPermissions != manage {
		t.Errorf("default permissions = %v, want Manage Server", mod.DefaultMemberPermissions)
	}
	if mod.IntegrationTypes == nil || !reflect.DeepEqual(*mod.IntegrationTypes, []discordgo.ApplicationIntegrationType{discordgo.ApplicationIntegrationGuildInstall}) {
		t.Errorf("install types = %v, want server only", mod.IntegrationTypes)
	}
	both := definition(t, "t-both")
	if !reflect.DeepEqual(*both.IntegrationTypes, []discordgo.ApplicationIntegrationType{discordgo.ApplicationIntegrationGuildInstall, discordgo.ApplicationIntegrationUserInstall}) {
		t.Errorf("install types = %v, want server and user", *both.IntegrationTypes)
	}
	wantCtx := []discordgo.InteractionContextType{discordgo.InteractionContextGuild, discordgo.InteractionContextBotDM, discordgo.InteractionContextPrivateChannel}
	if !reflect.DeepEqual(*both.Contexts, wantCtx) {
		t.Errorf("contexts = %v, want guild, bot DM and private channels", *both.Contexts)
	}
}

func TestAddRejectsInconsistentGroupsAndRightClickCommands(t *testing.T) {
	sub := &Subcommand{Name: "s", Desc: "d", Run: noRun}
	opt := []*discordgo.ApplicationCommandOption{{Name: "o"}}
	base := func(c Command) *Command { c.Name, c.Visibility, c.Response = "x", Public, Direct; return &c }
	tests := map[string]*Command{
		"run beside subcommands":      base(Command{Run: noRun, Subcommands: []*Subcommand{sub}}),
		"options beside subcommands":  base(Command{Options: opt, Subcommands: []*Subcommand{sub}}),
		"subcommand without handler":  base(Command{Subcommands: []*Subcommand{{Name: "s", Desc: "d"}}}),
		"subcommand without name":     base(Command{Subcommands: []*Subcommand{{Desc: "d", Run: noRun}}}),
		"empty group":                 base(Command{Groups: []*SubcommandGroup{{Name: "g", Desc: "d"}}}),
		"right-click with options":    base(Command{Kind: KindUser, Run: noRun, Options: opt}),
		"right-click with subcommand": base(Command{Kind: KindMessage, Run: noRun, Subcommands: []*Subcommand{sub}}),
		"right-click without Run":     base(Command{Kind: KindUser}),
		"install types without value": base(Command{Run: noRun, Installs: []discordgo.ApplicationIntegrationType{}}),
	}
	for name, cmd := range tests {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Error("Add did not panic")
				}
				delete(registry, "x")
			}()
			Add(cmd)
		})
	}
}

func subInteraction(name, guildID string, opts ...*discordgo.ApplicationCommandInteractionDataOption) *discordgo.InteractionCreate {
	i := slashInteraction(name, guildID, 0)
	d := i.Interaction.Data.(discordgo.ApplicationCommandInteractionData)
	d.Options = opts
	i.Interaction.Data = d
	return i
}

func TestDispatchSendsASubcommandToItsOwnHandler(t *testing.T) {
	var ran []string
	var seen *interaction.Context
	h := func(tag string) func(*interaction.Context) error {
		return func(ic *interaction.Context) error {
			ran = append(ran, tag)
			seen = ic
			return ic.Reply(interaction.Response{Content: tag})
		}
	}
	register(t, &Command{Name: "t-tree", Desc: "d", Visibility: Public, Response: Direct,
		Subcommands: []*Subcommand{{Name: "add", Desc: "d", Run: h("add")}, {Name: "slow", Desc: "d", Visibility: Private, Response: Deferred, Run: h("slow")}},
		Groups:      []*SubcommandGroup{{Name: "admin", Desc: "d", Subcommands: []*Subcommand{{Name: "purge", Desc: "d", Run: h("purge")}}}},
	})

	val := &discordgo.ApplicationCommandInteractionDataOption{Name: "show", Type: discordgo.ApplicationCommandOptionString, Value: "Frieren"}
	sub := func(name string, opts ...*discordgo.ApplicationCommandInteractionDataOption) *discordgo.ApplicationCommandInteractionDataOption {
		return &discordgo.ApplicationCommandInteractionDataOption{Name: name, Type: discordgo.ApplicationCommandOptionSubCommand, Options: opts}
	}

	fake := &interactiontest.Fake{}
	if err := run(t, "t-tree", fake, subInteraction("t-tree", "g", sub("add", val))); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ran, []string{"add"}) || strings.Join(fake.Ops(), ",") != "Reply" || fake.Calls()[0].Visibility != interaction.Public {
		t.Errorf("ran %v ops %v, want add alone with a public Reply", ran, fake.Ops())
	}
	if seen.Subcommand != "add" || seen.Group != "" || len(seen.Command.Options) != 1 || seen.Command.Options[0].Name != "show" {
		t.Errorf("context = group %q sub %q options %v, want add with its own options", seen.Group, seen.Subcommand, seen.Command.Options)
	}

	fake = &interactiontest.Fake{}
	group := &discordgo.ApplicationCommandInteractionDataOption{Name: "admin", Type: discordgo.ApplicationCommandOptionSubCommandGroup, Options: []*discordgo.ApplicationCommandInteractionDataOption{sub("purge")}}
	if err := run(t, "t-tree", fake, subInteraction("t-tree", "g", group)); err != nil {
		t.Fatal(err)
	}
	if ran[len(ran)-1] != "purge" || seen.Group != "admin" || seen.Subcommand != "purge" {
		t.Errorf("ran %v group %q sub %q, want purge in admin", ran, seen.Group, seen.Subcommand)
	}

	fake = &interactiontest.Fake{}
	if err := run(t, "t-tree", fake, subInteraction("t-tree", "g", sub("slow"))); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(fake.Ops(), ","); got != "Defer,Reply" || fake.Calls()[0].Visibility != interaction.Private {
		t.Errorf("ops = %s vis %v, want the subcommand's own private deferred shape", got, fake.Calls()[0].Visibility)
	}
}

func TestDispatchRefusesAnUnknownSubcommandPrivately(t *testing.T) {
	register(t, &Command{Name: "t-tree", Desc: "d", Visibility: Public, Response: Direct,
		Subcommands: []*Subcommand{{Name: "add", Desc: "d", Run: noRun}}})
	fake := &interactiontest.Fake{}
	bad := &discordgo.ApplicationCommandInteractionDataOption{Name: "gone", Type: discordgo.ApplicationCommandOptionSubCommand}
	if err := run(t, "t-tree", fake, subInteraction("t-tree", "g", bad)); err != nil {
		t.Fatal(err)
	}
	calls := fake.Calls()
	if len(calls) != 1 || calls[0].Op != "Reply" || calls[0].Visibility != interaction.Private {
		t.Errorf("calls = %+v, want one private Reply", calls)
	}
}

func TestDispatchGivesARightClickCommandItsTarget(t *testing.T) {
	var gotUser *discordgo.User
	var gotMessage *discordgo.Message
	register(t, &Command{Name: "T who", Kind: KindUser, Visibility: Private, Response: Direct,
		Run: func(ic *interaction.Context) error {
			gotUser = ic.TargetUser()
			return ic.Reply(interaction.Response{Content: "ok"})
		}})
	register(t, &Command{Name: "T what", Kind: KindMessage, Visibility: Private, Response: Direct,
		Run: func(ic *interaction.Context) error {
			gotMessage = ic.TargetMessage()
			return ic.Reply(interaction.Response{Content: "ok"})
		}})

	user := &discordgo.User{ID: "target-user"}
	msg := &discordgo.Message{ID: "target-msg"}
	resolved := &discordgo.ApplicationCommandInteractionDataResolved{
		Users: map[string]*discordgo.User{"target-user": user}, Messages: map[string]*discordgo.Message{"target-msg": msg}}
	for name, target := range map[string]string{"T who": "target-user", "T what": "target-msg"} {
		i := slashInteraction(name, "g", 0)
		i.Interaction.Data = discordgo.ApplicationCommandInteractionData{Name: name, TargetID: target, Resolved: resolved}
		if err := run(t, name, &interactiontest.Fake{}, i); err != nil {
			t.Fatal(err)
		}
	}
	if gotUser != user || gotMessage != msg {
		t.Errorf("targets = %v / %v, want the resolved user and message", gotUser, gotMessage)
	}
}

func TestAFormOpensFromACommandAndItsSubmissionReachesItsHandler(t *testing.T) {
	register(t, &Command{Name: "t-say", Desc: "d", Visibility: Private, Response: Direct,
		Run: func(ic *interaction.Context) error {
			return ic.OpenForm(interaction.Form{CustomID: "t-say-form:1", Title: "Say",
				Components: []discordgo.MessageComponent{interaction.TextBox("body", "Message", discordgo.TextInputParagraph, true)}})
		}})
	var typed string
	registerComponent(t, "t-say-form:", func(ic *interaction.Context, _ *discordgo.Session) error {
		typed = ic.FormValue("body")
		return ic.Reply(interaction.Response{Content: "posted"})
	})

	fake := &interactiontest.Fake{}
	if err := run(t, "t-say", fake, slashInteraction("t-say", "g", 0)); err != nil {
		t.Fatal(err)
	}
	calls := fake.Calls()
	if len(calls) != 1 || calls[0].Op != "OpenForm" || calls[0].Form.CustomID != "t-say-form:1" || len(calls[0].Form.Components) != 1 {
		t.Fatalf("calls = %+v, want one OpenForm with the form", calls)
	}

	submit := componentInteraction(discordgo.ModalSubmitInteractionData{CustomID: "t-say-form:1", Components: []discordgo.MessageComponent{
		&discordgo.Label{Label: "Message", Component: &discordgo.TextInput{CustomID: "body", Value: "hello"}}}}, "g", "u", nil)
	submit.Interaction.Type = discordgo.InteractionModalSubmit
	fake = &interactiontest.Fake{}
	dispatchPress(t, fake, submit)
	if typed != "hello" || strings.Join(fake.Ops(), ",") != "Reply" {
		t.Errorf("typed %q ops %v, want the typed text and one Reply", typed, fake.Ops())
	}
}

func TestAutocompleteReachesASubcommandsOwnOptionAndAnswersThroughThePort(t *testing.T) {
	var asked string
	register(t, &Command{Name: "t-tree", Desc: "d", Visibility: Private, Response: Direct,
		Subcommands: []*Subcommand{{Name: "add", Desc: "d", Run: noRun,
			Options: []*discordgo.ApplicationCommandOption{{Name: "show", Description: "s", Type: discordgo.ApplicationCommandOptionString, Autocomplete: true}},
			AutocompleteOption: func(_ context.Context, _ *database.Client, _ *discordgo.Session, _ *discordgo.InteractionCreate, option, value string) []*discordgo.ApplicationCommandOptionChoice {
				asked = option + "=" + value
				return []*discordgo.ApplicationCommandOptionChoice{{Name: "Frieren", Value: "Frieren"}}
			}}}})

	focused := &discordgo.ApplicationCommandInteractionDataOption{Name: "show", Type: discordgo.ApplicationCommandOptionString, Value: "fri", Focused: true}
	sub := &discordgo.ApplicationCommandInteractionDataOption{Name: "add", Type: discordgo.ApplicationCommandOptionSubCommand, Options: []*discordgo.ApplicationCommandInteractionDataOption{focused}}
	fake := &interactiontest.Fake{}
	handleAutocomplete(context.Background(), nil, fake, nil, subInteraction("t-tree", "g", sub))
	calls := fake.Calls()
	if asked != "show=fri" || len(calls) != 1 || calls[0].Op != "Suggest" || len(calls[0].Choices) != 1 || calls[0].Choices[0].Name != "Frieren" {
		t.Errorf("asked %q calls %+v, want the subcommand's autocomplete answered with one Suggest", asked, calls)
	}
}

func TestAutocompleteWithNothingToOfferAnswersWithAnEmptyList(t *testing.T) {
	fake := &interactiontest.Fake{}
	handleAutocomplete(context.Background(), nil, fake, nil, subInteraction("t-missing", "g"))
	calls := fake.Calls()
	if len(calls) != 1 || calls[0].Op != "Suggest" || calls[0].Choices == nil || len(calls[0].Choices) != 0 {
		t.Errorf("calls = %+v, want one Suggest with an empty, non-nil list", calls)
	}
}
