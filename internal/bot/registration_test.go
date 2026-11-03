package bot

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"ReZeroTsu/internal/commands"

	"github.com/bwmarrin/discordgo"
)

// modCommands are the commands ADR-0001 puts behind Manage Server; every other registered command is for everyone.
var modCommands = map[string]bool{
	"settings": true, "voicerole": true, "raffle": true, "reddit": true, "reactrole": true, "say": true, "prune": true,
	"Add reaction role": true, "Remove reaction roles": true, "Edit message": true,
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

// The moderator-role, mod-only and reacts-module settings are no longer read or written: servers set access in
// Discord's command permissions (ADR-0001). This fails if Go code mentions them again.
func TestRemovedModeratorSettingsAreNotReferenced(t *testing.T) {
	banned := regexp.MustCompile(`\b(CommandRoles|ModOnly|ReactsModule|ModeratorRefusal|command_roles|mod_only|reacts_module)\b`)
	root := filepath.Join("..", "..")
	self := "registration_test.go"
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == ".claude") {
			return fs.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || d.Name() == self {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if m := banned.Find(b); m != nil {
			t.Errorf("%s mentions %q, a removed moderator setting", path, m)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
