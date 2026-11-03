package anime_subs

import (
	"context"
	"errors"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

func TestShowSubRouteIsOptionalInStoredDocuments(t *testing.T) {
	legacy, err := bson.Marshal(bson.M{"show": "Frieren", "notified": true, "guild": false})
	if err != nil {
		t.Fatal(err)
	}
	var sub ShowSub
	if err := bson.Unmarshal(legacy, &sub); err != nil {
		t.Fatal(err)
	}
	if sub.Route != "" || sub.Show != "Frieren" {
		t.Errorf("legacy sub = %+v, want empty route", sub)
	}
	out, err := bson.Marshal(&ShowSub{Show: "Frieren"})
	if err != nil {
		t.Fatal(err)
	}
	var raw bson.M
	if err := bson.Unmarshal(out, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["route"]; ok {
		t.Errorf("an empty route is stored: %v", raw)
	}
}

// ADR-0002: a subscription with a route matches by route only; one without matches by title under MatchKey.
func TestShowSubMatches(t *testing.T) {
	tests := []struct {
		name         string
		sub          ShowSub
		route, title string
		want         bool
	}{
		{"same route", ShowSub{Show: "Foo", Route: "x"}, "x", "Foo Season 2", true},
		{"different route, same title", ShowSub{Show: "Foo", Route: "a"}, "b", "Foo", false},
		{"route subscription never title-matches a route-less entry", ShowSub{Show: "Twin", Route: "twin-a"}, "", "Twin", false},
		{"route-less subscription matches by title key", ShowSub{Show: " Foo "}, "x", "foo", true},
		{"route-less subscription, different title", ShowSub{Show: "Foo"}, "x", "Bar", false},
	}
	for _, tc := range tests {
		if got := tc.sub.Matches(tc.route, tc.title); got != tc.want {
			t.Errorf("%s: %+v.Matches(%q, %q) = %v, want %v", tc.name, tc.sub, tc.route, tc.title, got, tc.want)
		}
	}
}

// The atomic add skips only a subscription of the same route, or a route-less one of the same title: it must not
// skip a different-route show that shares a title.
func TestAddShowIfAbsentFilterFollowsTheRouteThenTitleRule(t *testing.T) {
	var filters []any
	capture := func(_ context.Context, filter, _ any, _ bool) (*mongo.UpdateResult, error) {
		filters = append(filters, filter)
		return &mongo.UpdateResult{ModifiedCount: 1}, nil
	}
	for _, sub := range []*ShowSub{{Show: "Foo", Route: "x"}, {Show: "Foo"}} {
		if _, err := addShowIfAbsent(context.Background(), capture, "u1", sub); err != nil {
			t.Fatal(err)
		}
	}
	branches := func(f any) bson.A {
		out, _ := f.(bson.M)["shows"].(bson.M)["$not"].(bson.M)["$elemMatch"].(bson.M)["$or"].(bson.A)
		if out == nil {
			t.Fatalf("filter %v has no $or of match branches inside $elemMatch", f)
		}
		return out
	}
	withRoute, routeless := branches(filters[0]), branches(filters[1])
	if len(withRoute) != 2 || withRoute[0].(bson.M)["route"] != "x" {
		t.Errorf("route subscription filter = %v, want a same-route branch then a route-less same-title branch", withRoute)
	}
	if len(routeless) != 1 {
		t.Errorf("route-less subscription filter = %v, want only the route-less same-title branch", routeless)
	}
	for _, b := range []bson.A{withRoute, routeless} {
		title := b[len(b)-1].(bson.M)
		if _, ok := title["show"]; !ok || title["route"] == nil {
			t.Errorf("title branch %v must require an existing subscription without a route", title)
		}
	}
}

func dupKeyErr() error {
	return mongo.WriteException{WriteErrors: mongo.WriteErrors{{Code: 11000, Message: "E11000 duplicate key"}}}
}

type updateCall struct{ upsert bool }

type scriptedUpdate struct {
	results []*mongo.UpdateResult
	errs    []error
	calls   []updateCall
}

func (s *scriptedUpdate) update(_ context.Context, _, _ any, upsert bool) (*mongo.UpdateResult, error) {
	i := len(s.calls)
	s.calls = append(s.calls, updateCall{upsert: upsert})
	var res *mongo.UpdateResult
	if i < len(s.results) {
		res = s.results[i]
	}
	var err error
	if i < len(s.errs) {
		err = s.errs[i]
	}
	return res, err
}

// The Mongo behaviour of the null-tolerant append is covered by manual acceptance; this only pins that show
// names are wrapped in $literal, so a name starting with "$" is never evaluated as a field path.
func TestAppendedShowsExprWrapsShowsInLiteral(t *testing.T) {
	shows := []*ShowSub{{Show: "$shows"}, {Show: "B"}}
	expr := appendedShowsExpr(shows)
	parts := expr["$concatArrays"].(bson.A)
	lit, ok := parts[1].(bson.M)["$literal"]
	if !ok {
		t.Fatalf("appended shows are not wrapped in $literal: %v", parts[1])
	}
	got, ok := lit.([]*ShowSub)
	if !ok || len(got) != 2 || got[0].Show != "$shows" || got[1].Show != "B" {
		t.Errorf("$literal holds %v, want the two shows unchanged", lit)
	}
}

// The collapse expression must at least encode as BSON; its Mongo behaviour is covered by manual acceptance.
func TestCollapsedShowsExprMarshals(t *testing.T) {
	if _, err := bson.Marshal(bson.M{"shows": collapsedShowsExpr()}); err != nil {
		t.Fatal(err)
	}
}

// Names the collapse treats as duplicates must also be one show to the notifier, or a collapse could keep an
// entry the notifier does not read.
func TestMatchKeyAgreesWithCollapseKey(t *testing.T) {
	pairs := [][2]string{
		{"Frieren", "FRIEREN"},
		{" Frieren ", "frieren"},
		{" Show A", "show a\t"},
		{"ÄBC", "Äbc"},
	}
	for _, p := range pairs {
		if CollapseKey(p[0]) != CollapseKey(p[1]) {
			t.Fatalf("test pair %q has different collapse keys", p)
		}
		if MatchKey(p[0]) != MatchKey(p[1]) {
			t.Errorf("MatchKey(%q) = %q, MatchKey(%q) = %q, want equal", p[0], MatchKey(p[0]), p[1], MatchKey(p[1]))
		}
	}
	if MatchKey("ÄBC") != MatchKey("äbc") {
		t.Errorf("MatchKey is not case-insensitive for non-ASCII letters")
	}
}

// CollapseKey must equal what the collapse pipeline computes in Mongo: $trim's default whitespace set, then the
// ASCII-only $toLower. Expected values follow the MongoDB documentation of both operators.
func TestCollapseKeyMatchesMongo(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"ASCII case", "FrIeReN", "frieren"},
		{"surrounding spaces and tabs", " \tFrieren\r\n", "frieren"},
		{"vertical tab, form feed and null", "\v\f\x00Frieren\x00", "frieren"},
		{"no-break, ogham and en/em spaces", "   Frieren  ", "frieren"},
		{"non-ASCII letters keep their case", "ÄÖÜ Show", "ÄÖÜ show"},
		{"Turkish dotted I is not lowered", "İstanbul", "İstanbul"},
		{"ideographic space is not trimmed", "　Frieren", "　frieren"},
		{"next line is not trimmed", "Frieren\u0085", "frieren\u0085"},
		{"zero-width space is not trimmed", "​Frieren", "​frieren"},
		{"inner whitespace is kept", "Show  A", "show  a"},
		{"only whitespace", "  \t", ""},
		{"empty", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := CollapseKey(tc.in); got != tc.want {
				t.Errorf("CollapseKey(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestAddShowIfAbsent(t *testing.T) {
	boom := errors.New("boom")
	tests := []struct {
		name       string
		results    []*mongo.UpdateResult
		errs       []error
		wantAdded  bool
		wantErr    bool
		wantUpsert []bool // upsert flag of each update call
	}{
		{
			name:       "modified existing document",
			results:    []*mongo.UpdateResult{{MatchedCount: 1, ModifiedCount: 1}},
			wantAdded:  true,
			wantUpsert: []bool{true},
		},
		{
			name:       "created a new document",
			results:    []*mongo.UpdateResult{{UpsertedCount: 1}},
			wantAdded:  true,
			wantUpsert: []bool{true},
		},
		{
			name:       "duplicate key then the conditional retry matches: the show was added",
			errs:       []error{dupKeyErr()},
			results:    []*mongo.UpdateResult{nil, {MatchedCount: 1, ModifiedCount: 1}},
			wantAdded:  true,
			wantUpsert: []bool{true, false},
		},
		{
			name:       "duplicate key then the conditional retry matches nothing: the show is really present",
			errs:       []error{dupKeyErr()},
			results:    []*mongo.UpdateResult{nil, {}},
			wantAdded:  false,
			wantUpsert: []bool{true, false},
		},
		{
			name:       "duplicate key then the retry fails",
			errs:       []error{dupKeyErr(), boom},
			wantErr:    true,
			wantUpsert: []bool{true, false},
		},
		{
			name:       "other error is returned without retry",
			errs:       []error{boom},
			wantErr:    true,
			wantUpsert: []bool{true},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := &scriptedUpdate{results: tc.results, errs: tc.errs}
			added, err := addShowIfAbsent(context.Background(), s.update, "u1", &ShowSub{Show: "Frieren"})
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if added != tc.wantAdded {
				t.Errorf("added = %v, want %v", added, tc.wantAdded)
			}
			if len(s.calls) != len(tc.wantUpsert) {
				t.Fatalf("update calls = %d, want %d", len(s.calls), len(tc.wantUpsert))
			}
			for i, c := range s.calls {
				if c.upsert != tc.wantUpsert[i] {
					t.Errorf("call %d upsert = %v, want %v", i, c.upsert, tc.wantUpsert[i])
				}
			}
		})
	}
}
