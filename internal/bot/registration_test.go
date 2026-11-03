package bot

import (
	"reflect"
	"slices"
	"testing"
	"unicode/utf8"

	"ReZeroTsu/internal/commands"

	"github.com/bwmarrin/discordgo"
)

// modCommands are the commands ADR-0001 puts behind Manage Server; every other registered command is for everyone.
var modCommands = map[string]bool{
	"settings": true, "voicerole": true, "raffle": true, "reddit": true, "reactrole": true, "say": true, "prune": true,
	"Add reaction role": true, "Remove reaction roles": true, "Edit message": true,
}

// userInstallable is the set of commands that work under a user install: the everyday commands and Show avatar.
var userInstallable = map[string]bool{
	"schedule": true, "subscription": true, "reminder": true, "roll": true, "pick": true, "joke": true,
	"avatar": true, "help": true, "about": true, "invite": true, "ping": true, "Show avatar": true,
}

func TestRegistrationPayloadCarriesManageServerOnEveryModCommandOnly(t *testing.T) {
	seen := map[string]bool{}
	for _, d := range commands.Definitions() {
		seen[d.Name] = true
		got := d.DefaultMemberPermissions
		if modCommands[d.Name] {
			if got == nil || *got != discordgo.PermissionManageGuild {
				t.Errorf("%q default member permissions = %v, want Manage Server", d.Name, got)
			}
		} else if got != nil {
			t.Errorf("%q default member permissions = %d, want none for an everyone command", d.Name, *got)
		}
	}
	for name := range modCommands {
		if !seen[name] {
			t.Errorf("mod command %q is not registered", name)
		}
	}
	for _, gone := range []string{"add-moderator-role", "remove-moderator-role", "moderator-roles", "mod-only", "react-module"} {
		if seen[gone] {
			t.Errorf("removed command %q is still registered", gone)
		}
	}
}

// Discord's default member permission is the only gate on a mod command, and Discord does not apply it in bot DMs
// or under a user install. A mod command therefore registers for servers only.
func TestModCommandsRegisterForServersOnly(t *testing.T) {
	for _, d := range commands.Definitions() {
		if !modCommands[d.Name] {
			continue
		}
		if d.Contexts == nil || !reflect.DeepEqual(*d.Contexts, []discordgo.InteractionContextType{discordgo.InteractionContextGuild}) {
			t.Errorf("%q contexts = %v, want the server only", d.Name, d.Contexts)
		}
		if d.IntegrationTypes == nil || !reflect.DeepEqual(*d.IntegrationTypes, []discordgo.ApplicationIntegrationType{discordgo.ApplicationIntegrationGuildInstall}) {
			t.Errorf("%q install types = %v, want server install only", d.Name, d.IntegrationTypes)
		}
	}
}

func TestOnlyTheEverydayCommandsRegisterAsUserInstallable(t *testing.T) {
	bothInstalls := []discordgo.ApplicationIntegrationType{discordgo.ApplicationIntegrationGuildInstall, discordgo.ApplicationIntegrationUserInstall}
	serverOnly := []discordgo.ApplicationIntegrationType{discordgo.ApplicationIntegrationGuildInstall}

	seen := map[string]bool{}
	for _, d := range commands.Definitions() {
		seen[d.Name] = true
		if d.IntegrationTypes == nil || d.Contexts == nil {
			t.Errorf("%q registers without install types or contexts", d.Name)
			continue
		}
		if userInstallable[d.Name] {
			if !reflect.DeepEqual(*d.IntegrationTypes, bothInstalls) {
				t.Errorf("%q install types = %v, want server and user", d.Name, *d.IntegrationTypes)
			}
			for _, want := range []discordgo.InteractionContextType{discordgo.InteractionContextGuild, discordgo.InteractionContextBotDM, discordgo.InteractionContextPrivateChannel} {
				if !slices.Contains(*d.Contexts, want) {
					t.Errorf("%q contexts = %v, missing %v", d.Name, *d.Contexts, want)
				}
			}
			continue
		}
		if !reflect.DeepEqual(*d.IntegrationTypes, serverOnly) {
			t.Errorf("%q install types = %v, want server install only", d.Name, *d.IntegrationTypes)
		}
		if slices.Contains(*d.Contexts, discordgo.InteractionContextPrivateChannel) {
			t.Errorf("%q contexts = %v, must not include private channels", d.Name, *d.Contexts)
		}
	}
	for name := range userInstallable {
		if !seen[name] {
			t.Errorf("%q is not registered", name)
		}
	}
}

func TestShowAvatarRegistersAsAUserRightClickCommand(t *testing.T) {
	for _, d := range commands.Definitions() {
		if d.Name == "Show avatar" {
			if d.Type != discordgo.UserApplicationCommand || d.Description != "" {
				t.Errorf("Show avatar = type %v, description %q; want a user command with no description", d.Type, d.Description)
			}
			return
		}
	}
	t.Fatal("Show avatar is not registered")
}

// Discord rejects the whole bulk overwrite with a 400 when any one name or description is over its limit, so a
// single long description takes every command down on deploy.
func TestRegistrationPayloadStaysWithinDiscordTextLimits(t *testing.T) {
	const maxName, maxDescription, maxChoice = 32, 100, 100
	check := func(where, kind, text string, limit int) {
		if n := utf8.RuneCountInString(text); n > limit {
			t.Errorf("%s %s is %d characters, Discord allows %d: %q", where, kind, n, limit, text)
		}
	}
	var walk func(path string, opts []*discordgo.ApplicationCommandOption)
	walk = func(path string, opts []*discordgo.ApplicationCommandOption) {
		for _, o := range opts {
			where := path + " " + o.Name
			check(where, "name", o.Name, maxName)
			check(where, "description", o.Description, maxDescription)
			for _, c := range o.Choices {
				check(where+" choice", "name", c.Name, maxChoice)
				if s, ok := c.Value.(string); ok {
					check(where+" choice", "value", s, maxChoice)
				}
			}
			walk(where, o.Options)
		}
	}
	for _, d := range commands.Definitions() {
		check(d.Name, "name", d.Name, maxName)
		check(d.Name, "description", d.Description, maxDescription)
		walk(d.Name, d.Options)
	}
}

func TestMergedCommandsAreRegisteredAndTheOldNamesAreGone(t *testing.T) {
	registered := map[string]bool{}
	for _, d := range commands.Definitions() {
		registered[d.Name] = true
	}
	for _, name := range []string{"settings", "voicerole", "reddit"} {
		if !registered[name] {
			t.Errorf("/%s is not registered", name)
		}
	}
	for _, name := range []string{
		"bot-log", "donghua", "ping-message", "new-episodes", "daily-schedule",
		"add-voice", "remove-voice", "voice-roles",
		"add-reddit-feed", "remove-reddit-feed", "reddit-feeds",
	} {
		if registered[name] {
			t.Errorf("old command /%s is still registered", name)
		}
	}
}
