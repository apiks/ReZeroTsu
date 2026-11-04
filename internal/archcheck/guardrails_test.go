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
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
	"unicode"
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

// portAdapterFile is the one file allowed to call them.
const portAdapterFile = "interaction/discordgo.go"

// responseCalls lists every interaction-response call in a file.
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

// checkInteractionResponses reports session interaction-response calls outside the port adapter. name is the
// path relative to internal/.
func checkInteractionResponses(fset *token.FileSet, file *ast.File, name string) []string {
	if name == portAdapterFile {
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

// removedModeratorSettings matches the settings fields that ADR-0001 removed (moderator roles, mod-only and
// reacts-module), their bson keys, and the bot-side moderator refusal. Stored documents keep the old values, but no
// code reads or writes them: servers set access in Discord's command permissions.
var removedModeratorSettings = regexp.MustCompile(`\b(CommandRoles|ModOnly|ReactsModule|ModeratorRefusal|command_roles|mod_only|reacts_module)\b`)

// checkRemovedModeratorSettings reports identifiers and string literals (struct tags included) that name a removed
// moderator setting. Comments may still mention them.
func checkRemovedModeratorSettings(fset *token.FileSet, file *ast.File, name string) []string {
	var out []string
	ast.Inspect(file, func(n ast.Node) bool {
		var text string
		switch n := n.(type) {
		case *ast.Ident:
			text = n.Name
		case *ast.BasicLit:
			text = n.Value
		default:
			return true
		}
		if m := removedModeratorSettings.FindString(text); m != "" {
			out = append(out, fmt.Sprintf("%s:%d: %s is a removed moderator setting", name, fset.Position(n.Pos()).Line, m))
		}
		return true
	})
	return out
}

type check func(*token.FileSet, *ast.File, string) []string

// scanTree runs check over the non-test Go files under root (recursively). The name handed to check is the path
// relative to root, with forward slashes.
func scanTree(t *testing.T, root string, c check) []string {
	t.Helper()
	return scanFiles(t, root, c, false)
}

// scanTreeWithTests is scanTree over the test files too.
func scanTreeWithTests(t *testing.T, root string, c check) []string {
	t.Helper()
	return scanFiles(t, root, c, true)
}

func scanFiles(t *testing.T, root string, c check, withTests bool) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || (!withTests && strings.HasSuffix(path, "_test.go")) {
			return nil
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
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
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
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

func TestEveryRegisteredCommandDeclaresAVisibility(t *testing.T) {
	for _, v := range scanTree(t, "../commands", checkCommandsDeclareVisibility) {
		t.Error(v)
	}
}

func TestRemovedModeratorSettingsAreNotReferenced(t *testing.T) {
	for _, v := range scanTree(t, "..", checkRemovedModeratorSettings) {
		t.Error(v)
	}
}

func TestGlossaryAvoidTermsAreNotUsedInNames(t *testing.T) {
	for _, v := range scanTree(t, "..", checkAvoidTerms) {
		t.Error(v)
	}
}

func TestGlossaryAvoidPhrasesAreNotUsedInCommentsOrStrings(t *testing.T) {
	for _, v := range scanTree(t, "..", checkAvoidPhrases(avoidPhrases(t))) {
		t.Error(v)
	}
}

func TestGlossaryAvoidPhrasesAreNotUsedInNamesOrFileNames(t *testing.T) {
	for _, v := range scanTreeWithTests(t, "..", checkAvoidNames(avoidNamePhrases(t))) {
		t.Error(v)
	}
}

func TestGlossaryAvoidPhrasesAreNotUsedInUserDocs(t *testing.T) {
	re := avoidPhrases(t)
	for _, doc := range []string{"README.md", "docs/release-notes.md"} {
		raw, err := os.ReadFile(filepath.Join("../..", doc))
		if err != nil {
			t.Fatalf("read %s: %v", doc, err)
		}
		for _, v := range checkAvoidPhrasesInText(re, doc, string(raw)) {
			t.Error(v)
		}
		for _, v := range checkRawMentions(doc, string(raw)) {
			t.Error(v)
		}
	}
}

func TestRawMentionsAreFlaggedInDocs(t *testing.T) {
	text := "Run </subscription add> in a DM.\nOr </subscription add:123>.\nOr </schedule:99>.\nA quote `/schedule`.\n</p>\nKept </list:1> <!-- archcheck:allow -->\n"
	if got := checkRawMentions("x.md", text); len(got) != 3 {
		t.Errorf("violations = %d %v, want 3", len(got), got)
	}
}

func TestTextIsNotSlicedAtDiscordLengthLimits(t *testing.T) {
	for _, v := range scanTree(t, "..", checkLimitSlices) {
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
	commands.Add(&commands.Command{Name: "c", Run: h})
	wg.Add(1)
}
`
	const moderating = "package settings\n\n" +
		"// CommandRoles and mod_only may be named in a comment.\n" +
		"type GuildSettings struct {\n" +
		"\tCommandRoles []string `bson:\"command_roles\"`\n" +
		"}\n\n" +
		"func f(gs *GuildSettings) bool { return gs.ModOnly }\n"
	const avoiding = "package settings\n\n" +
		"// ModeratorRole may be named in a comment.\n" +
		"const prefix = \"command-role:\"\n\n" +
		"func f(giveawayID string) { _ = \"fine\" }\n"
	const slicing = `package bot

func f(title string, fields []int) {
	_ = title[:discord.MaxEmbedAuthorNameLength-1]
	_ = title[:discord.MaxEmbedFooterLength]
	_ = fields[:discord.MaxEmbedFields]
	_ = title[:n]
}
`
	const phrasing = "package feeds\n\n" +
		"// The post type of a feed.\n" +
		"// The upstream ONA (Chinese) label. archcheck:allow\n" +
		"const note = \"Invalid post type\"\n" +
		"const quoted = \"ONA (Chinese)\" // archcheck:allow\n" +
		"const stored = \"post_type\"\n" +
		"const fine = \"sort\"\n"
	const naming = "package bot\n\n" +
		"type Entry struct {\n" +
		"\tAirType string\n" +
		"\tpostTypeFeed int // archcheck:allow\n" +
		"}\n\n" +
		"func trackFilter(episodeFeeds []string) { _ = ONAChinese }\n\n" +
		"func fine(sortOrder int) {}\n"
	const storing = "package bot\n\nfunc get(postType string) { _ = postTypeNewEpisodes }\n"
	parse := func(src string) (*token.FileSet, *ast.File) {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, "sample.go", src, parser.ParseComments)
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
		{"command helpers may not respond", responding, checkInteractionResponses, "commands/helpers.go", 3},
		{"port calls are not session responses", clean, checkInteractionResponses, "commands/roll/rollnew.go", 0},
		{"a command without Visibility is flagged", registering, checkCommandsDeclareVisibility, "x.go", 2},
		{"removed moderator settings are flagged in code and tags, not comments", moderating, checkRemovedModeratorSettings, "x.go", 3},
		{"unrelated code is not a removed moderator setting", clean, checkRemovedModeratorSettings, "x.go", 0},
		{"glossary Avoid terms are flagged in names and strings, not comments", avoiding, checkAvoidTerms, "x.go", 2},
		{"unrelated code has no Avoid term", clean, checkAvoidTerms, "x.go", 0},
		{"multi-word Avoid terms are flagged in comments and strings, not where allowed or stored names", phrasing, checkAvoidPhrases(avoidPhrases(t)), "x.go", 2},
		{"unrelated code has no Avoid phrase", clean, checkAvoidPhrases(avoidPhrases(t)), "x.go", 0},
		{"multi-word Avoid terms are flagged in names, not where allowed", naming, checkAvoidNames(avoidPhrases(t)), "x.go", 3},
		{"the stored autopost post type is not an Avoid term in names", storing, checkAvoidNames(avoidNamePhrases(t)), "x.go", 0},
		{"a file name with an Avoid term is flagged", clean, checkAvoidNames(avoidPhrases(t)), "post_type_test.go", 1},
		{"unrelated names have no Avoid phrase", clean, checkAvoidNames(avoidPhrases(t)), "x.go", 0},
		{"text sliced at a Discord length limit is flagged, a count limit is not", slicing, checkLimitSlices, "x.go", 2},
		{"unrelated slices are fine", clean, checkLimitSlices, "x.go", 0},
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

// allowMarker on a line (or inside a comment) exempts that line from the Avoid-phrase check, for the places that
// must quote an upstream or stored name, such as AnimeSchedule's "ONA (Chinese)" label.
const allowMarker = "archcheck:allow"

// avoidPhrases returns a matcher for the multi-word `_Avoid_` terms in GLOSSARY.md ("post type", "ONA (Chinese)",
// "episode feed"). The glossary stays the single source: add a term there and this check follows. A space in a term
// also matches a hyphen, and a trailing "s" is allowed. Single words stay in avoidTerms and CODING_STANDARDS.md.
func avoidPhrases(t *testing.T) *regexp.Regexp {
	t.Helper()
	return avoidPhrasesExcept(t)
}

// storedNameTerms are Avoid terms that identifiers may still spell because the stored field they name is a
// different concept from the one the glossary retires: an autopost's `post_type` (new-episodes or daily-schedule)
// is not a Reddit feed's sort. The comment and string checks keep flagging them.
var storedNameTerms = []string{"post type"}

// avoidNamePhrases is avoidPhrases without the terms in storedNameTerms, for the check on identifiers.
func avoidNamePhrases(t *testing.T) *regexp.Regexp {
	t.Helper()
	return avoidPhrasesExcept(t, storedNameTerms...)
}

// avoidPhrasesExcept is avoidPhrases without the listed terms.
func avoidPhrasesExcept(t *testing.T, skip ...string) *regexp.Regexp {
	t.Helper()
	raw, err := os.ReadFile("../../GLOSSARY.md")
	if err != nil {
		t.Fatalf("read GLOSSARY.md: %v", err)
	}
	var alts []string
	for _, line := range strings.Split(string(raw), "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "_Avoid_:")
		if !ok {
			continue
		}
		for _, term := range strings.Split(rest, ",") {
			term = strings.TrimSpace(term)
			if strings.Contains(term, " ") && !slices.Contains(skip, term) {
				alts = append(alts, strings.ReplaceAll(regexp.QuoteMeta(term), " ", "[ -]"))
			}
		}
	}
	if len(alts) == 0 {
		t.Fatal("GLOSSARY.md has no multi-word Avoid terms; the parser is out of date")
	}
	return regexp.MustCompile(`(?i)(?:^|[^\pL\pN])(?:` + strings.Join(alts, "|") + `)s?(?:$|[^\pL\pN])`)
}

// checkAvoidPhrases reports comments and string literals that contain a multi-word glossary Avoid term.
func checkAvoidPhrases(re *regexp.Regexp) check {
	return func(fset *token.FileSet, file *ast.File, name string) []string {
		allowed := map[int]bool{}
		for _, g := range file.Comments {
			for _, c := range g.List {
				if strings.Contains(c.Text, allowMarker) {
					allowed[fset.Position(c.End()).Line] = true
				}
			}
		}
		var out []string
		report := func(pos token.Pos, kind, text string) {
			if allowed[fset.Position(pos).Line] {
				return
			}
			if m := re.FindString(text); m != "" {
				out = append(out, fmt.Sprintf("%s:%d: %s contains the glossary Avoid term %q; use the term GLOSSARY.md defines",
					name, fset.Position(pos).Line, kind, strings.TrimFunc(m, func(r rune) bool { return !unicode.IsLetter(r) && r != ')' })))
			}
		}
		for _, g := range file.Comments {
			for _, c := range g.List {
				if !strings.Contains(c.Text, allowMarker) {
					report(c.Pos(), "a comment", c.Text)
				}
			}
		}
		ast.Inspect(file, func(n ast.Node) bool {
			if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				report(lit.Pos(), "a string", lit.Value)
			}
			return true
		})
		return out
	}
}

// wordsOf splits an identifier or file name into lower-case words, so "AirType", "airType" and "air_type" all give
// "air type", the shape the multi-word Avoid terms are written in.
func wordsOf(name string) string {
	runes := []rune(name)
	var b strings.Builder
	for i, r := range runes {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			b.WriteByte(' ')
			continue
		}
		if unicode.IsUpper(r) && i > 0 {
			prev := runes[i-1]
			nextLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
			if unicode.IsLower(prev) || unicode.IsDigit(prev) || (unicode.IsUpper(prev) && nextLower) {
				b.WriteByte(' ')
			}
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

// checkAvoidNames reports identifiers, and the file's own name, that spell a multi-word glossary Avoid term as
// words ("AirType", "trackFilter", "track_setting_test.go"). checkAvoidPhrases reads only comments and strings,
// so a name can otherwise carry a retired term for ever. A line carrying archcheck:allow is exempt, for a stored
// or upstream name that must keep its spelling.
func checkAvoidNames(re *regexp.Regexp) check {
	return func(fset *token.FileSet, file *ast.File, name string) []string {
		allowed := map[int]bool{}
		for _, g := range file.Comments {
			for _, c := range g.List {
				if strings.Contains(c.Text, allowMarker) {
					allowed[fset.Position(c.End()).Line] = true
				}
			}
		}
		var out []string
		stem := strings.TrimSuffix(strings.TrimSuffix(filepath.Base(name), ".go"), "_test")
		if m := re.FindString(" " + wordsOf(stem) + " "); m != "" && !allowed[fset.Position(file.Package).Line] {
			out = append(out, fmt.Sprintf("%s: the file name contains the glossary Avoid term %q; rename the file with the term GLOSSARY.md defines", name, strings.TrimSpace(m)))
		}
		seen := map[string]bool{}
		ast.Inspect(file, func(n ast.Node) bool {
			id, ok := n.(*ast.Ident)
			if !ok {
				return true
			}
			line := fset.Position(id.Pos()).Line
			if allowed[line] {
				return true
			}
			if m := re.FindString(" " + wordsOf(id.Name) + " "); m != "" {
				key := fmt.Sprintf("%d:%s", line, id.Name)
				if !seen[key] {
					seen[key] = true
					out = append(out, fmt.Sprintf("%s:%d: the name %q contains the glossary Avoid term %q; use the term GLOSSARY.md defines",
						name, line, id.Name, strings.TrimSpace(m)))
				}
			}
			return true
		})
		return out
	}
}

// checkAvoidPhrasesInText reports the lines of a Markdown document that contain a multi-word glossary Avoid term.
// A line carrying archcheck:allow is exempt, for the places that quote a retired or upstream name.
func checkAvoidPhrasesInText(re *regexp.Regexp, name, text string) []string {
	var out []string
	for i, line := range strings.Split(text, "\n") {
		if strings.Contains(line, allowMarker) {
			continue
		}
		if m := re.FindString(line); m != "" {
			out = append(out, fmt.Sprintf("%s:%d: contains the glossary Avoid term %q; use the term GLOSSARY.md defines",
				name, i+1, strings.TrimFunc(m, func(r rune) bool { return !unicode.IsLetter(r) && r != ')' })))
		}
	}
	return out
}

// rawMention matches Discord's own command-mention syntax, "</subscription add:123>" or "</schedule:123>", and the
// same without its ID. Markdown shows it as junk; a document quotes the command as `/subscription add`. A plain
// HTML closing tag such as </p> has no space and no ID, so it does not match.
var rawMention = regexp.MustCompile(`</[\w-]+(?: [\w-]+)+(?::\d+)?>|</[\w-]+:\d+>`)

// checkRawMentions reports the lines of a Markdown document that contain Discord's raw command-mention syntax. A
// line carrying archcheck:allow is exempt.
func checkRawMentions(name, text string) []string {
	var out []string
	for i, line := range strings.Split(text, "\n") {
		if strings.Contains(line, allowMarker) {
			continue
		}
		if m := rawMention.FindString(line); m != "" {
			out = append(out, fmt.Sprintf("%s:%d: contains the raw Discord mention %q; write the command as `/name` instead", name, i+1, m))
		}
	}
	return out
}

// checkLimitSlices reports a string sliced at a Discord length limit (s[:discord.MaxEmbedDescriptionLength-1]).
// Discord counts UTF-16 units and the slice counts bytes, so it cuts early and can split a character. Use
// discord.Fit or discord.Head.
func checkLimitSlices(fset *token.FileSet, file *ast.File, name string) []string {
	var out []string
	ast.Inspect(file, func(n ast.Node) bool {
		s, ok := n.(*ast.SliceExpr)
		if !ok {
			return true
		}
		for _, bound := range []ast.Expr{s.Low, s.High, s.Max} {
			if bound != nil && mentionsLengthLimit(bound) {
				out = append(out, fmt.Sprintf("%s:%d: sliced at a Discord length limit; cut with discord.Fit or discord.Head",
					name, fset.Position(s.Pos()).Line))
				break
			}
		}
		return true
	})
	return out
}

// mentionsLengthLimit reports whether e refers to a discord.Max...Length constant.
func mentionsLengthLimit(e ast.Expr) bool {
	found := false
	ast.Inspect(e, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok {
			if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "discord" &&
				strings.HasPrefix(sel.Sel.Name, "Max") && strings.HasSuffix(sel.Sel.Name, "Length") {
				found = true
			}
		}
		return !found
	})
	return found
}

// avoidTerms are the GLOSSARY.md `_Avoid_` terms that mean nothing else in this codebase, written lowercase with
// separators removed. Words that are also everyday code (pick, roll, check, cache, sub) stay a judgement call in
// CODING_STANDARDS.md.
var avoidTerms = []string{"moderatorrole", "modrole", "commandrole", "giveaway", "lottery"}

var avoidSeparators = strings.NewReplacer("_", "", "-", "", " ", "", ":", "")

// checkAvoidTerms reports identifiers and string literals that contain a glossary Avoid term. Comments are not
// scanned, so a comment may still quote the old name.
func checkAvoidTerms(fset *token.FileSet, file *ast.File, name string) []string {
	var out []string
	ast.Inspect(file, func(n ast.Node) bool {
		var text string
		switch n := n.(type) {
		case *ast.Ident:
			text = n.Name
		case *ast.BasicLit:
			if n.Kind != token.STRING {
				return true
			}
			text = n.Value
		default:
			return true
		}
		folded := strings.ToLower(avoidSeparators.Replace(text))
		for _, term := range avoidTerms {
			if strings.Contains(folded, term) {
				out = append(out, fmt.Sprintf("%s:%d: %q contains the glossary Avoid term %q; use the term GLOSSARY.md defines", name, fset.Position(n.Pos()).Line, text, term))
				break
			}
		}
		return true
	})
	return out
}
