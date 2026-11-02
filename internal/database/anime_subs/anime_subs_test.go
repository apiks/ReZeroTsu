package anime_subs

import (
	"context"
	"errors"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

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
	expr := appendedShowsExpr([]*ShowSub{{Show: "$shows"}, {Show: "B"}})
	parts := expr["$concatArrays"].(bson.A)
	literals := parts[1].(bson.A)
	if len(literals) != 2 {
		t.Fatalf("literals = %d, want 2", len(literals))
	}
	for i, l := range literals {
		if _, ok := l.(bson.M)["$literal"]; !ok {
			t.Errorf("entry %d is not wrapped in $literal: %v", i, l)
		}
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
