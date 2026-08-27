package db

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// EnvTestDatabaseURL points at a Postgres the integration tests may create and
// drop databases in. Unset, every test in this file skips, so `go test ./...`
// stays fast and dependency-free for anyone without a server to hand.
//
// The gate is an environment variable rather than a build tag deliberately: a
// build-tagged file stops compiling the moment someone renames a repo method,
// and nobody notices until they run with the tag. This one compiles on every
// build and only its execution is conditional.
const EnvTestDatabaseURL = "SKULID_TEST_DATABASE_URL"

// testDB brings up a database of its own, runs every migration from scratch,
// and returns a pool onto it. Each test gets a fresh database, so nothing
// leaks between them and the migration chain is exercised on every run --
// which is the point: 0011 and 0012 reached production having never been run
// against a real Postgres.
func testDB(t *testing.T) *pgxpool.Pool {
	t.Helper()

	base := strings.TrimSpace(os.Getenv(EnvTestDatabaseURL))
	if base == "" {
		t.Skipf("set %s to run the Postgres integration tests", EnvTestDatabaseURL)
	}

	ctx := context.Background()
	admin, err := Connect(ctx, base)
	if err != nil {
		t.Fatalf("connecting to %s: %v", EnvTestDatabaseURL, err)
	}
	defer admin.Close()

	name := testDBName(t)
	// A leftover from a killed run would otherwise fail the create.
	if _, err := admin.Exec(ctx, `DROP DATABASE IF EXISTS "`+name+`"`); err != nil {
		t.Fatalf("dropping stale %s: %v", name, err)
	}
	if _, err := admin.Exec(ctx, `CREATE DATABASE "`+name+`"`); err != nil {
		t.Fatalf("creating %s: %v", name, err)
	}

	dsn := replaceDBName(base, name)
	if err := Migrate(ctx, dsn); err != nil {
		t.Fatalf("migrating %s: %v", name, err)
	}
	pool, err := Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connecting to %s: %v", name, err)
	}

	t.Cleanup(func() {
		pool.Close()
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		admin, err := Connect(cleanupCtx, base)
		if err != nil {
			return
		}
		defer admin.Close()
		_, _ = admin.Exec(cleanupCtx, `DROP DATABASE IF EXISTS "`+name+`" WITH (FORCE)`)
	})
	return pool
}

// testDBName derives a database name from the test's own name, so a failure
// leaves behind something identifiable rather than a random string.
func testDBName(t *testing.T) string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			return r
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		default:
			return '_'
		}
	}, t.Name())
	// Postgres truncates identifiers at 63 bytes; leave room for the prefix.
	if len(safe) > 50 {
		safe = safe[:50]
	}
	return "skulid_t_" + safe
}

// replaceDBName swaps the database in a postgres:// DSN, leaving credentials,
// host and query parameters alone.
func replaceDBName(dsn, name string) string {
	// Split off any query string first so a "/" inside it can't be mistaken
	// for the path separator.
	base, query, hasQuery := strings.Cut(dsn, "?")
	slash := strings.LastIndex(base, "/")
	if slash < 0 {
		return dsn
	}
	out := base[:slash+1] + name
	if hasQuery {
		out += "?" + query
	}
	return out
}

func TestReplaceDBName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"postgres://u:p@localhost:5432/skulid", "postgres://u:p@localhost:5432/target"},
		{"postgres://u:p@localhost:5432/skulid?sslmode=disable", "postgres://u:p@localhost:5432/target?sslmode=disable"},
		// A "/" inside the query must not be taken for the path separator.
		{"postgres://u@h/db?opt=a/b", "postgres://u@h/target?opt=a/b"},
	}
	for _, c := range cases {
		if got := replaceDBName(c.in, "target"); got != c.want {
			t.Errorf("replaceDBName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestMigrationsApplyFromScratch is the test that would have caught 0011 and
// 0012: it runs the whole chain against a real server. testDB does the work;
// reaching the body at all means every migration parsed and applied.
func TestMigrationsApplyFromScratch(t *testing.T) {
	pool := testDB(t)
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
	pool := testDB(t)
	pool.Close()

	base := strings.TrimSpace(os.Getenv(EnvTestDatabaseURL))
	dsn := replaceDBName(base, testDBName(t))
	ctx := context.Background()

	if err := MigrateDownAll(ctx, dsn); err != nil {
		t.Fatalf("goose down: %v", err)
	}
	if err := Migrate(ctx, dsn); err != nil {
		t.Fatalf("re-applying migrations after a full down: %v", err)
	}
}

// seedCalendar creates the account and calendar rows most repos need a foreign
// key onto, through the repos themselves so the write paths are covered too.
func seedCalendar(t *testing.T, pool *pgxpool.Pool) (accountID, calendarID int64) {
	t.Helper()
	ctx := context.Background()

	expires := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	accountID, err := NewAccountRepo(pool).Upsert(ctx,
		"google-sub-1", "owner@example.com", "sealed-refresh", "sealed-access", &expires)
	if err != nil {
		t.Fatalf("seeding account: %v", err)
	}
	calendarID, err = NewCalendarRepo(pool).Upsert(ctx, accountID, "primary", "Primary", "UTC", "#ff0000")
	if err != nil {
		t.Fatalf("seeding calendar: %v", err)
	}
	return accountID, calendarID
}

func ptr[T any](v T) *T { return &v }
