package schedule

import (
	"context"
	"os"
	"testing"

	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/database/anime_subs"
)

// TestMain keeps every test off the database: a member who is not under test has no document, so pages without a
// server setting call shows by their titles. member_preferred_name_test.go counts the reads.
func TestMain(m *testing.M) {
	memberDocument = func(context.Context, *database.Client, string) (*anime_subs.AnimeSubs, error) { return nil, nil }
	os.Exit(m.Run())
}
