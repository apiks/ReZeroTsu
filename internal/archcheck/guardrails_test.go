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

type check func(*token.FileSet, *ast.File, string) []string

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
