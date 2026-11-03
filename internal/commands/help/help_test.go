package help

import (
	"slices"
	"strings"
	"testing"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/interaction"
	"ReZeroTsu/internal/interaction/interactiontest"

	"github.com/bwmarrin/discordgo"
)

var noRun = func(*interaction.Context) error { return nil }

func slash(name, desc, module string, perm commands.PermissionLevel, installs []discordgo.ApplicationIntegrationType) *commands.Command {
	return &commands.Command{Name: name, Desc: desc, Module: module, Permission: perm, Installs: installs, Run: noRun,
		Visibility: commands.Public, Response: commands.Direct}
}

func sampleCommands() []*commands.Command {
	both := commands.BothInstalls()
	return []*commands.Command{
		slash("roll", "Roll dice.", "general", commands.PermEveryone, both),
		slash("schedule", "Show the schedule.", "schedule", commands.PermEveryone, both),
		{Name: "subscription", Module: "schedule", Installs: both, Visibility: commands.Private, Response: commands.Deferred,
			Subcommands: []*commands.Subcommand{{Name: "add", Desc: "Subscribe to a show.", Run: noRun}, {Name: "list", Desc: "List subscriptions.", Run: noRun}}},
		slash("prune", "Delete messages.", "channel", commands.PermMod, nil),
		slash("settings", "Server settings.", "settings", commands.PermMod, nil),
		slash("secret", "Owner only.", "general", commands.PermOwner, nil),
		{Name: "Show avatar", Kind: commands.KindUser, Module: "general", Installs: both, Run: noRun, Visibility: commands.Public, Response: commands.Direct},
	}
}

func useCommands(t *testing.T, cmds []*commands.Command) {
	t.Helper()
	old := listCommands
	listCommands = func() []*commands.Command { return cmds }
	t.Cleanup(func() { listCommands = old })
}

func guildInteraction(userInstallOnly bool) *discordgo.Interaction {
	i := &discordgo.Interaction{Type: discordgo.InteractionApplicationCommand, GuildID: "g", ChannelID: "c",
		Data:   discordgo.ApplicationCommandInteractionData{Name: "help"},
		Member: &discordgo.Member{User: &discordgo.User{ID: "author"}}}
	if userInstallOnly {
		i.AuthorizingIntegrationOwners = map[discordgo.ApplicationIntegrationType]string{discordgo.ApplicationIntegrationUserInstall: "author"}
	}
	return i
}

func runHelpWith(t *testing.T, i *discordgo.Interaction) *interactiontest.Fake {
	t.Helper()
	fake := &interactiontest.Fake{}
	if err := runHelp(interaction.NewContext(fake, i, nil, interaction.Public, interaction.Direct)); err != nil {
		t.Fatal(err)
	}
	return fake
}

func embedText(r interaction.Response) string {
	var b strings.Builder
	b.WriteString(r.Content)
	for _, e := range r.Embeds {
		b.WriteString(e.Title + "\n" + e.Description + "\n")
		for _, f := range e.Fields {
			b.WriteString(f.Name + "\n" + f.Value + "\n")
		}
	}
	return b.String()
}

func findPicker(t *testing.T, r interaction.Response) discordgo.SelectMenu {
	t.Helper()
	for _, c := range r.Components {
		if row, ok := c.(discordgo.ActionsRow); ok {
			for _, in := range row.Components {
				if m, ok := in.(discordgo.SelectMenu); ok {
					return m
				}
			}
		}
	}
	t.Fatalf("no category picker in %+v", r.Components)
	return discordgo.SelectMenu{}
}

func TestHelpHasNoSubcommands(t *testing.T) {
	for _, c := range commands.AllCommands() {
		if c.Name == "help" {
			if len(c.Options) != 0 || len(c.Subcommands) != 0 || len(c.Groups) != 0 {
				t.Errorf("/help must take no arguments: %+v", c)
			}
			if c.Visibility != commands.Public {
				t.Error("/help must be public")
			}
			return
		}
	}
	t.Fatal("/help is not registered")
}

func TestHelpOpensPubliclyOnAnOverviewWithACategoryPicker(t *testing.T) {
	useCommands(t, sampleCommands())
	fake := runHelpWith(t, guildInteraction(false))
	calls := fake.Calls()
	if len(calls) != 1 || calls[0].Op != "Reply" || calls[0].Visibility != interaction.Public {
		t.Fatalf("calls = %+v, want one public Reply", calls)
	}
	m := findPicker(t, calls[0].Response)
	var values []string
	for _, o := range m.Options {
		values = append(values, o.Value)
	}
	for _, want := range []string{"general", "schedule", "channel", "settings"} {
		found := false
		for _, v := range values {
			found = found || v == want
		}
		if !found {
			t.Errorf("picker lacks category %q: %v", want, values)
		}
	}
}

func TestAnimeComesBeforeTheModeratorCategories(t *testing.T) {
	useCommands(t, append(sampleCommands(), slash("say", "Post a message.", "misc", commands.PermMod, nil)))
	m := findPicker(t, runHelpWith(t, guildInteraction(false)).Calls()[0].Response)
	var labels []string
	for _, o := range m.Options {
		labels = append(labels, o.Label)
	}
	anime, misc := slices.Index(labels, "Anime"), slices.Index(labels, "Misc")
	if anime < 0 || misc < 0 || anime > misc || slices.Contains(labels, "Schedule") {
		t.Errorf("picker labels = %v, want Anime before Misc and no Schedule", labels)
	}
}

func TestOverviewLabelsModeratorCategoriesForEveryone(t *testing.T) {
	useCommands(t, sampleCommands())
	// A plain member with no permissions at all sees the categories and the label.
	text := embedText(runHelpWith(t, guildInteraction(false)).Calls()[0].Response)
	if !strings.Contains(text, "Channel") || !strings.Contains(text, "Moderators only") {
		t.Errorf("overview = %q, want the Channel category labelled Moderators only", text)
	}
}

func selectCategory(t *testing.T, customID, presser, value string, onPrivate bool) *interactiontest.Fake {
	t.Helper()
	i := &discordgo.Interaction{Type: discordgo.InteractionMessageComponent, GuildID: "g", ChannelID: "c",
		Data:    discordgo.MessageComponentInteractionData{CustomID: customID, ComponentType: discordgo.SelectMenuComponent, Values: []string{value}},
		Member:  &discordgo.Member{User: &discordgo.User{ID: presser}},
		Message: &discordgo.Message{ID: "m"}}
	if onPrivate {
		i.Message.Flags = discordgo.MessageFlagsEphemeral
	}
	fake := &interactiontest.Fake{}
	if err := handlePick(interaction.NewContext(fake, i, nil, interaction.Private, interaction.Direct), nil); err != nil {
		t.Fatal(err)
	}
	return fake
}

func TestCategoryShowsCommandsAsMentionsWithPlainFallback(t *testing.T) {
	useCommands(t, sampleCommands())
	t.Cleanup(func() { commands.SetCommandIDs(nil) })

	fake := selectCategory(t, pickerID("author", scopeServer), "author", "schedule", false)
	text := embedText(fake.Calls()[0].Response)
	for _, want := range []string{"/schedule", "/subscription add", "/subscription list", "Subscribe to a show."} {
		if !strings.Contains(text, want) {
			t.Errorf("fallback text lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "</") {
		t.Errorf("no IDs known, yet a mention was rendered:\n%s", text)
	}

	commands.SetCommandIDs([]*discordgo.ApplicationCommand{{ID: "111", Name: "schedule", Type: discordgo.ChatApplicationCommand}, {ID: "222", Name: "subscription", Type: discordgo.ChatApplicationCommand}})
	text = embedText(selectCategory(t, pickerID("author", scopeServer), "author", "schedule", false).Calls()[0].Response)
	for _, want := range []string{"</schedule:111>", "</subscription add:222>", "</subscription list:222>"} {
		if !strings.Contains(text, want) {
			t.Errorf("mention text lacks %q:\n%s", want, text)
		}
	}
}

func TestModeratorCategoryIsLabelledAndListedForAPlainMember(t *testing.T) {
	useCommands(t, sampleCommands())
	text := embedText(selectCategory(t, pickerID("author", scopeServer), "author", "channel", false).Calls()[0].Response)
	if !strings.Contains(text, "Moderators only") || !strings.Contains(text, "/prune") {
		t.Errorf("category = %q", text)
	}
}

func TestOwnerAndRightClickCommandsAreNeverListed(t *testing.T) {
	useCommands(t, sampleCommands())
	all := embedText(selectCategory(t, pickerID("author", scopeServer), "author", "general", false).Calls()[0].Response)
	if strings.Contains(all, "secret") || strings.Contains(all, "Show avatar") {
		t.Errorf("category = %q", all)
	}
}

func TestUserInstallListsOnlyUserInstallableCommands(t *testing.T) {
	useCommands(t, sampleCommands())
	fake := runHelpWith(t, guildInteraction(true))
	m := findPicker(t, fake.Calls()[0].Response)
	for _, o := range m.Options {
		if o.Value == "channel" || o.Value == "settings" {
			t.Errorf("user install picker offers server-only category %q", o.Value)
		}
	}
	if !strings.HasSuffix(m.CustomID, scopeUser) {
		t.Fatalf("picker %q does not remember the user install scope", m.CustomID)
	}
	// Choosing a category later still lists only user-installable commands.
	text := embedText(selectCategory(t, m.CustomID, "author", "schedule", false).Calls()[0].Response)
	if !strings.Contains(text, "/schedule") {
		t.Errorf("category = %q", text)
	}
	text = embedText(selectCategory(t, m.CustomID, "author", "channel", false).Calls()[0].Response)
	if strings.Contains(text, "/prune") {
		t.Errorf("user install category lists a server-only command: %q", text)
	}
}

func TestDMListsOnlyUserInstallableCommands(t *testing.T) {
	useCommands(t, sampleCommands())
	i := guildInteraction(false)
	i.GuildID, i.Member, i.User = "", nil, &discordgo.User{ID: "author"}
	m := findPicker(t, runHelpWith(t, i).Calls()[0].Response)
	for _, o := range m.Options {
		if o.Value == "channel" {
			t.Error("DM picker offers a server-only category")
		}
	}
}

func TestAuthorsPickUpdatesTheMessageItself(t *testing.T) {
	useCommands(t, sampleCommands())
	calls := selectCategory(t, pickerID("author", scopeServer), "author", "general", false).Calls()
	if len(calls) != 1 || calls[0].Op != "UpdateMessage" {
		t.Fatalf("calls = %+v, want UpdateMessage", calls)
	}
	findPicker(t, calls[0].Response) // the picker stays so the author can keep browsing
}

func TestOtherMembersPickGetsAPrivateCopyAndLeavesTheMessageAlone(t *testing.T) {
	useCommands(t, sampleCommands())
	calls := selectCategory(t, pickerID("author", scopeServer), "other", "schedule", false).Calls()
	if len(calls) != 1 || calls[0].Op != "Reply" || calls[0].Visibility != interaction.Private {
		t.Fatalf("calls = %+v, want one private Reply", calls)
	}
	if !strings.Contains(embedText(calls[0].Response), "/schedule") {
		t.Error("the copy lacks the requested category")
	}
	// The copy's own picker belongs to the member who asked, so using it pages the copy.
	m := findPicker(t, calls[0].Response)
	if !strings.HasPrefix(m.CustomID, pickerID("other", scopeServer)) {
		t.Errorf("copy picker %q is not owned by the presser", m.CustomID)
	}
}

func TestPickOnAPrivateCopyUpdatesTheCopy(t *testing.T) {
	useCommands(t, sampleCommands())
	calls := selectCategory(t, pickerID("other", scopeServer), "other", "general", true).Calls()
	if len(calls) != 1 || calls[0].Op != "UpdateMessage" {
		t.Fatalf("calls = %+v, want UpdateMessage", calls)
	}
}

func TestPickingTheOverviewReturnsToIt(t *testing.T) {
	useCommands(t, sampleCommands())
	text := embedText(selectCategory(t, pickerID("author", scopeServer), "author", overviewKey, false).Calls()[0].Response)
	if !strings.Contains(text, "Pick a category") {
		t.Errorf("overview = %q", text)
	}
}

func TestStalePickIsRefusedPrivately(t *testing.T) {
	useCommands(t, sampleCommands())
	for name, id := range map[string]string{"bad id": "help:", "unknown category": pickerID("author", scopeServer)} {
		t.Run(name, func(t *testing.T) {
			calls := selectCategory(t, id, "author", "nonexistent", false).Calls()
			if len(calls) != 1 || calls[0].Op != "Reply" || calls[0].Visibility != interaction.Private {
				t.Fatalf("calls = %+v, want one private Reply", calls)
			}
		})
	}
}
