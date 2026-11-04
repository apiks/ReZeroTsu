package archcheck

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// fieldOperators are the update operators whose operand names stored fields.
var fieldOperators = map[string]bool{
	"$set": true, "$setOnInsert": true, "$unset": true, "$inc": true, "$min": true, "$max": true, "$mul": true,
	"$push": true, "$pull": true, "$pullAll": true, "$addToSet": true, "$pop": true, "$rename": true,
}

// bsonTags is the stored name of every struct field with a bson tag in the non-test files of dir, the vocabulary
// a filter or update of that package may name.
func bsonTags(t *testing.T, dir string) map[string]bool {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	tags := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, e.Name()), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", e.Name(), err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			if f, ok := n.(*ast.Field); ok && f.Tag != nil {
				raw, err := strconv.Unquote(f.Tag.Value)
				if err != nil {
					return true
				}
				if name, _, _ := strings.Cut(reflect.StructTag(raw).Get("bson"), ","); name != "" && name != "-" {
					tags[name] = true
				}
			}
			return true
		})
	}
	return tags
}

func isBsonM(lit *ast.CompositeLit) bool {
	sel, ok := lit.Type.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "bson" && sel.Sel.Name == "M"
}

func stringKey(kv *ast.KeyValueExpr) (string, bool) {
	lit, ok := kv.Key.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	return s, err == nil
}

// checkBsonFieldNames reports a stored field name in a filter or update that is no bson tag of the package, so a
// typo in a name fails a test instead of writing a field nothing reads. It reads each top-level bson.M literal:
// the keys of a filter, and the keys of the operand of an update operator such as $set. A dotted path is checked
// segment by segment; "$", "$[x]" and index segments are skipped. It leaves alone what it cannot know: documents
// nested in another document, aggregation pipelines, keys built at run time, and bson.D. A literal that is an
// aggregation expression or an array filter (keys led by its identifier, "r.date") and not a plain filter or update
// takes an archcheck:allow comment on its first line.
func checkBsonFieldNames(tags map[string]bool) check {
	return func(fset *token.FileSet, file *ast.File, name string) []string {
		allowed := allowedLines(fset, file)
		var out []string
		field := func(literal, pos token.Pos, path string) {
			if allowed[fset.Position(pos).Line] || allowed[fset.Position(literal).Line] {
				return
			}
			for _, seg := range strings.Split(path, ".") {
				if _, err := strconv.Atoi(seg); seg == "" || strings.HasPrefix(seg, "$") || err == nil {
					continue
				}
				if !tags[seg] {
					out = append(out, fmt.Sprintf("%s:%d: the stored name %q (in %q) is no bson tag in this package; fix the name, or mark an expression literal archcheck:allow",
						name, fset.Position(pos).Line, seg, path))
				}
			}
		}
		keys := func(lit *ast.CompositeLit) {
			for _, e := range lit.Elts {
				if kv, ok := e.(*ast.KeyValueExpr); ok {
					if k, ok := stringKey(kv); ok && !strings.HasPrefix(k, "$") {
						field(lit.Pos(), kv.Pos(), k)
					}
				}
			}
		}
		var stack []ast.Node
		ast.Inspect(file, func(n ast.Node) bool {
			if n == nil {
				stack = stack[:len(stack)-1]
				return true
			}
			defer func() { stack = append(stack, n) }()
			lit, ok := n.(*ast.CompositeLit)
			if !ok || !isBsonM(lit) {
				return true
			}
			if len(stack) > 0 {
				switch stack[len(stack)-1].(type) {
				case *ast.KeyValueExpr, *ast.CompositeLit:
					return true // nested in another document, array or pipeline
				}
			}
			keys(lit)
			for _, e := range lit.Elts {
				kv, ok := e.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				if k, ok := stringKey(kv); ok && fieldOperators[k] {
					if operand, ok := kv.Value.(*ast.CompositeLit); ok && isBsonM(operand) {
						keys(operand)
					}
				}
			}
			return true
		})
		return out
	}
}

func TestStoredFieldNamesAreBsonTags(t *testing.T) {
	entries, err := os.ReadDir("../database")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join("../database", e.Name())
		for _, v := range scanDir(t, dir, checkBsonFieldNames(bsonTags(t, dir))) {
			t.Error(v)
		}
	}
}

func TestBsonNameDetectorFlagsViolations(t *testing.T) {
	const src = `package guilds

type Raffle struct {
	Name   string ` + "`bson:\"name\"`" + `
	Winner string ` + "`bson:\"winner_id,omitempty\"`" + `
}

type Guild struct {
	ID      string   ` + "`bson:\"_id\"`" + `
	Raffles []Raffle ` + "`bson:\"raffles\"`" + `
}

func f(guildID, field string) {
	_ = bson.M{"_id": guildID, "raffles.name": "a"}
	_ = bson.M{"$set": bson.M{"raffles.$.winner_id": "w", "raffles.$[x].winner_id": "w"}}
	_ = bson.M{"$setOnInsert": bson.M{"raffels": 1}}
	_ = bson.M{"_id": guildID, "raffles.nmae": "a"}
	_ = bson.M{"$set": bson.M{"guild_settings." + field: 1}}
	_ = bson.M{"$pull": bson.M{"raffles": bson.M{"channel_id": "m"}}}
	_ = bson.M{"$and": bson.A{bson.M{"anything": 1}}}
	_ = bson.D{{Key: "anything", Value: 1}}
	expression := bson.M{"keys": 1} // archcheck:allow aggregation expression
	_ = expression
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "sample.go", src, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	tags := map[string]bool{"name": true, "winner_id": true, "_id": true, "raffles": true}
	if got := checkBsonFieldNames(tags)(fset, file, "sample.go"); len(got) != 2 {
		t.Errorf("violations = %d %v, want 2 (raffels, nmae)", len(got), got)
	}
}
