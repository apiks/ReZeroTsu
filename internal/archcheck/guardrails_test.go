// Package archcheck holds source-scanning tests for mechanical rules that reviews kept catching by hand.
// It has no production code; the checks run as part of `go test ./...`.
package archcheck

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// directSendCalls are the discordgo methods that put a message in front of a user or read the channel to check
// whether one landed. Notification code reaches Discord only through the channel sender (see GLOSSARY.md), which
// uses the raw Discord adapter, so the shared send rules (no send after cancel, fallback, verification, detached
// "sent" writes) cannot be bypassed by one more copy of the send.
var directSendCalls = map[string]bool{
	"ChannelMessageSend":        true,
	"ChannelMessageSendComplex": true,
	"ChannelMessageSendEmbed":   true,
	"WebhookExecute":            true,
	"WebhookThreadExecute":      true,
	"UserChannelCreate":         true,
	"ChannelMessages":           true,
}

// sendAllowedFiles may call those methods directly: the raw Discord adapter, and the files that send one-off
// replies and logs that are not notifications.
var sendAllowedFiles = map[string]bool{
	"delivery.go": true, // discordgo adapter implementation
	"webhooks.go": true, // SendBatchToChannel, used by the raw Discord adapter
	"bot.go":      true, // new-guild log message
	"messages.go": true, // ping replies
}

// checkDirectSends reports direct Discord send calls in a file that is not allowed to make them.
func checkDirectSends(fset *token.FileSet, file *ast.File, name string) []string {
	if sendAllowedFiles[filepath.Base(name)] {
		return nil
	}
	var out []string
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if ok && directSendCalls[sel.Sel.Name] {
			out = append(out, fmt.Sprintf("%s: .%s called directly; post through a channel sender (see GLOSSARY.md)",
				fset.Position(call.Pos()), sel.Sel.Name))
		}
		return true
	})
	return out
}

// checkShowNameMatching reports show names compared by ad-hoc case folding. Subscribed show names and schedule
// show names must be compared with anime_subs.MatchKey, the key the notifier, cleanup and seeding all use;
// every other spelling of the comparison drifts from it on stray whitespace or Unicode case.
func checkShowNameMatching(fset *token.FileSet, file *ast.File, _ string) []string {
	var out []string
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok || pkg.Name != "strings" {
			return true
		}
		switch {
		case sel.Sel.Name == "EqualFold":
			out = append(out, fmt.Sprintf("%s: strings.EqualFold; compare show names with anime_subs.MatchKey", fset.Position(call.Pos())))
		case sel.Sel.Name == "ToLower" && mentionsShowName(call.Args):
			out = append(out, fmt.Sprintf("%s: strings.ToLower on a show name; key it with anime_subs.MatchKey", fset.Position(call.Pos())))
		}
		return true
	})
	return out
}

// mentionsShowName reports whether any argument refers to a show name: a .Show field (ShowSub) or one of the
// identifiers the commands use for show names.
func mentionsShowName(args []ast.Expr) bool {
	found := false
	for _, a := range args {
		ast.Inspect(a, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.SelectorExpr:
				if v.Sel.Name == "Show" {
					found = true
				}
			case *ast.Ident:
				if v.Name == "showName" || v.Name == "animeInput" {
					found = true
				}
			}
			return !found
		})
	}
	return found
}

// interactionResponseCalls are the discordgo session methods that answer an interaction. Handlers in the new form
// answer through interaction.Port; only its discordgo adapter calls these.
var interactionResponseCalls = map[string]bool{
	"InteractionRespond":        true,
	"InteractionResponseEdit":   true,
	"InteractionResponseDelete": true,
	"FollowupMessageCreate":     true,
	"FollowupMessageEdit":       true,
	"FollowupMessageDelete":     true,
}

// portAdapterFile is the one file allowed to call them for good.
const portAdapterFile = "interaction/discordgo.go"

// legacyResponseFiles may still call them: the command files that have not moved onto the port, relative to
// internal/. Each area spec (#33 to #36) removes the entries it migrates, and ticket 7.2 deletes the allowlist.
// A stale entry (a file that no longer makes such a call) fails the test, so the list only shrinks.
var legacyResponseFiles = map[string]bool{
	"commands/command.go":             true, // autocomplete answers and RespondEphemeral
	"commands/helpers.go":             true, // RespondEmbed helpers
	"commands/pagination.go":          true, // pagination buttons
	"commands/animesubs/animesubs.go": true,
	"commands/feeds/feeds.go":         true,
	"commands/help/help.go":           true,
	"commands/raffle/raffle.go":       true,
	"commands/react/react.go":         true,
	"commands/reminders/reminders.go": true,
	"commands/say/say.go":             true,
	"commands/schedule/schedule.go":   true,
}

// responseCalls lists every interaction-response call in a file, ignoring the allowlists.
func responseCalls(fset *token.FileSet, file *ast.File) []string {
	var out []string
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && interactionResponseCalls[sel.Sel.Name] {
			out = append(out, fmt.Sprintf("%s: .%s called on the session; answer through interaction.Port",
				fset.Position(call.Pos()), sel.Sel.Name))
		}
		return true
	})
	return out
}

// checkInteractionResponses reports session interaction-response calls outside the port adapter and the
// allowlist. name is the path relative to internal/.
func checkInteractionResponses(fset *token.FileSet, file *ast.File, name string) []string {
	if name == portAdapterFile || legacyResponseFiles[name] {
		return nil
	}
	return responseCalls(fset, file)
}

// checkCommandsDeclareVisibility reports commands.Add(&commands.Command{...}) literals with no Visibility field.
// A missing declaration would silently default; the rule is that every command states who sees its replies.
func checkCommandsDeclareVisibility(fset *token.FileSet, file *ast.File, _ string) []string {
	var out []string
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) != 1 || !isCommandsAdd(call.Fun) {
			return true
		}
		unary, ok := call.Args[0].(*ast.UnaryExpr)
		if !ok {
			return true
		}
		lit, ok := unary.X.(*ast.CompositeLit)
		if !ok {
			return true
		}
		for _, el := range lit.Elts {
			if kv, ok := el.(*ast.KeyValueExpr); ok {
				if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "Visibility" {
					return true
				}
			}
		}
		out = append(out, fmt.Sprintf("%s: command registered without a Visibility", fset.Position(call.Pos())))
		return true
	})
	return out
}

// isCommandsAdd matches commands.Add and, inside package commands, Add.
func isCommandsAdd(fun ast.Expr) bool {
	switch f := fun.(type) {
	case *ast.SelectorExpr:
		pkg, ok := f.X.(*ast.Ident)
		return ok && pkg.Name == "commands" && f.Sel.Name == "Add"
	case *ast.Ident:
		return f.Name == "Add"
	}
	return false
}

type check func(*token.FileSet, *ast.File, string) []string

// scanTree runs check over the non-test Go files under root (recursively). The name handed to check is the path
// relative to root, with forward slashes.
func scanTree(t *testing.T, root string, c check) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		out = append(out, c(fset, file, filepath.ToSlash(rel))...)
		return nil
	})
	if err != nil {
		t.Fatalf("scan %s: %v", root, err)
	}
	sort.Strings(out)
	return out
}

// scanDir runs check over the non-test Go files of dir (not recursive) and returns the sorted violations.
func scanDir(t *testing.T, dir string, c check) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		path := filepath.Join(dir, name)
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		out = append(out, c(fset, file, path)...)
	}
	sort.Strings(out)
	return out
}

func TestNotificationSendsGoThroughTheChannelSender(t *testing.T) {
	for _, v := range scanDir(t, "../bot", checkDirectSends) {
		t.Error(v)
	}
}

func TestInteractionResponsesGoThroughThePort(t *testing.T) {
	for _, v := range scanTree(t, "..", checkInteractionResponses) {
		t.Error(v)
	}
}

// The allowlist must shrink as commands migrate: an entry whose file no longer makes a session response call
// (or no longer exists) has to go.
func TestInteractionResponseAllowlistHasNoStaleEntries(t *testing.T) {
	for name := range legacyResponseFiles {
		path := filepath.Join("..", filepath.FromSlash(name))
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Errorf("%s: allowlisted but unreadable: %v", name, err)
			continue
		}
		if len(responseCalls(fset, file)) == 0 {
			t.Errorf("%s: allowlisted but makes no session response call; remove it from legacyResponseFiles", name)
		}
	}
}

func TestEveryRegisteredCommandDeclaresAVisibility(t *testing.T) {
	for _, v := range scanTree(t, "../commands", checkCommandsDeclareVisibility) {
		t.Error(v)
	}
}

func TestShowNamesAreComparedWithMatchKey(t *testing.T) {
	for _, dir := range []string{"../bot", "../commands/animesubs"} {
		for _, v := range scanDir(t, dir, checkShowNameMatching) {
			t.Error(v)
		}
	}
}

// The detectors above must keep detecting: a scan that silently finds nothing would pass forever.
func TestDetectorsFlagViolations(t *testing.T) {
	const violating = `package bot

import "strings"

func f(s session, sub sub, showName string) {
	s.ChannelMessageSendComplex("c", nil)
	s.WebhookExecute("id", "tok", false, nil)
	_ = strings.EqualFold(sub.Show, showName)
	_ = strings.ToLower(sub.Show)
	_ = strings.ToLower(showName)
}
`
	const clean = `package bot

import "strings"

func f(role role) {
	_ = strings.ToLower(role.Name)
	_ = strings.TrimSpace(role.Name)
	sender.post(ctx, msg)
}
`
	const responding = `package roll

func f(s session, i interaction) {
	_ = s.InteractionRespond(i, nil)
	_, _ = s.InteractionResponseEdit(i, nil)
	_, _ = s.FollowupMessageCreate(i, false, nil)
	_ = ic.Reply(resp)
}
`
	const registering = `package ping

func init() {
	commands.Add(&commands.Command{Name: "a", Visibility: commands.Public})
	commands.Add(&commands.Command{Name: "b"})
	commands.Add(&commands.Command{Name: "c", Handler: h})
	wg.Add(1)
}
`
	parse := func(src string) (*token.FileSet, *ast.File) {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, "sample.go", src, 0)
		if err != nil {
			t.Fatal(err)
		}
		return fset, file
	}
	tests := []struct {
		name  string
		src   string
		check check
		file  string
		want  int
	}{
		{"direct sends are flagged", violating, checkDirectSends, "feeds.go", 2},
		{"the raw Discord adapter may send", violating, checkDirectSends, "delivery.go", 0},
		{"EqualFold and show-name ToLower are flagged", violating, checkShowNameMatching, "feeds.go", 3},
		{"unrelated lowercasing and channel sender posts are fine", clean, checkShowNameMatching, "feeds.go", 0},
		{"channel sender posts are not direct sends", clean, checkDirectSends, "feeds.go", 0},
		{"session responses are flagged", responding, checkInteractionResponses, "commands/roll/rollnew.go", 3},
		{"the port adapter may respond", responding, checkInteractionResponses, "interaction/discordgo.go", 0},
		{"allowlisted legacy files may respond", responding, checkInteractionResponses, "commands/help/help.go", 0},
		{"port calls are not session responses", clean, checkInteractionResponses, "commands/roll/rollnew.go", 0},
		{"a command without Visibility is flagged", registering, checkCommandsDeclareVisibility, "x.go", 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fset, file := parse(tt.src)
			if got := tt.check(fset, file, tt.file); len(got) != tt.want {
				t.Errorf("violations = %d %v, want %d", len(got), got, tt.want)
			}
		})
	}
}
