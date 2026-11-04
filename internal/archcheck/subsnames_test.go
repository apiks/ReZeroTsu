package archcheck

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// checkSubscriptionNames reports identifiers that spell "subs" as a word. The plural names no track, so it always means
// subscriptions (or subcommands), and CODING_STANDARDS.md spells those out. "anime subs" is the stored collection
// and package name and stays. A line carrying archcheck:allow is exempt. The singular "sub" is left to review
// because it names the Sub track as often as a subscription.
func checkSubscriptionNames(fset *token.FileSet, file *ast.File, name string) []string {
	allowed := allowedLines(fset, file)
	var out []string
	seen := map[string]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if !ok {
			return true
		}
		words := strings.Fields(wordsOf(id.Name))
		for i, w := range words {
			if w != "subs" || (i > 0 && words[i-1] == "anime") {
				continue
			}
			line := fset.Position(id.Pos()).Line
			key := fmt.Sprintf("%d:%s", line, id.Name)
			if allowed[line] || seen[key] {
				break
			}
			seen[key] = true
			out = append(out, fmt.Sprintf("%s:%d: the name %q spells \"subs\"; write \"subscriptions\" (or \"subcommands\"), see CODING_STANDARDS.md",
				name, line, id.Name))
			break
		}
		return true
	})
	return out
}

func TestSubscriptionsAreSpelledOutInNames(t *testing.T) {
	for _, v := range scanTreeWithTests(t, "..", checkSubscriptionNames) {
		t.Error(v)
	}
}

func TestSubscriptionNameDetectorFlagsViolations(t *testing.T) {
	const src = `package bot

type showSubs []string

func IterateUserSubs(subs []string) {}

func TestAddNamesTheSubsNextEpisode() {}

var anime_subs, AnimeSubs, subsAllowed int // archcheck:allow

func fine(subscriptions, subTracks []string, animeSubs int, subs2 int) {}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "sample.go", src, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	if got := checkSubscriptionNames(fset, file, "sample.go"); len(got) != 4 {
		t.Errorf("violations = %d %v, want 4 (showSubs, IterateUserSubs, subs, the test name)", len(got), got)
	}
}
