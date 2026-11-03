package bot

import (
	"reflect"
	"slices"
	"testing"

	"ReZeroTsu/internal/commands"

	"github.com/bwmarrin/discordgo"
)

// userInstallable is the set of commands that work under a user install: the everyday commands and Show avatar.
var userInstallable = map[string]bool{
	"schedule": true, "subscription": true, "reminder": true, "roll": true, "pick": true, "joke": true,
	"avatar": true, "help": true, "about": true, "invite": true, "ping": true, "Show avatar": true,
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
