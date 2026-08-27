package db_test

import (
	"context"
	"testing"

	"github.com/ryakel/skulid/internal/db"
	"github.com/ryakel/skulid/internal/db/dbtest"
)

// These tests are external (package db_test) so they can use the shared
// dbtest harness, which imports internal/db and would otherwise cycle.

func TestReplaceDBName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"postgres://u:p@localhost:5432/skulid", "postgres://u:p@localhost:5432/target"},
		{"postgres://u:p@localhost:5432/skulid?sslmode=disable", "postgres://u:p@localhost:5432/target?sslmode=disable"},
		// A "/" inside the query must not be taken for the path separator.
		{"postgres://u@h/db?opt=a/b", "postgres://u@h/target?opt=a/b"},
	}
	for _, c := range cases {
		if got := dbtest.ReplaceDBName(c.in, "target"); got != c.want {
			t.Errorf("ReplaceDBName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestMigrationsApplyFromScratch is the test that would have caught 0011 and
// 0012: it runs the whole chain against a real server. dbtest.New does the
// work; reaching the body at all means every migration parsed and applied.
func TestMigrationsApplyFromScratch(t *testing.T) {
	pool := dbtest.New(t)
	ctx := context.Background()

	var version int64
	if err := pool.QueryRow(ctx,
		`SELECT max(version_id) FROM goose_db_version`).Scan(&version); err != nil {
		t.Fatalf("reading goose version: %v", err)
	}
	if version <= 0 {
		t.Fatalf("no migrations recorded, got version %d", version)
	}

	// Every table the repos read from must exist. A migration that renames one
	// without updating its readers fails here rather than at runtime.
	for _, table := range []string{
		"setting", "account", "calendar", "sync_token", "sync_rule", "event_link",
		"smart_block", "managed_block", "category", "task", "task_chunk",
		"habit", "habit_occurrence", "buffer_event", "audit_log",
		"ai_conversation", "ai_message", "ai_pending_action",
	} {
		var exists bool
		if err := pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables
			                WHERE table_schema = 'public' AND table_name = $1)`,
			table).Scan(&exists); err != nil {
			t.Fatalf("checking %s: %v", table, err)
		}
		if !exists {
			t.Errorf("table %q missing after migrations", table)
		}
	}
}

// TestMigrationsAreReversible runs every Down migration and then every Up
// again. A Down that doesn't undo its Up leaves the second Up failing, which
// is exactly the breakage a hand-written rename introduces.
func TestMigrationsAreReversible(t *testing.T) {
	pool := dbtest.New(t)
	pool.Close()

	dsn := dbtest.ReplaceDBName(dbtest.BaseDSN(t), dbtest.DBName(t))
	ctx := context.Background()

	if err := db.MigrateDownAll(ctx, dsn); err != nil {
		t.Fatalf("goose down: %v", err)
	}
	if err := db.Migrate(ctx, dsn); err != nil {
		t.Fatalf("re-applying migrations after a full down: %v", err)
	}
}
