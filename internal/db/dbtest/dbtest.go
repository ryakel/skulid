// Package dbtest brings up throwaway Postgres databases for tests.
//
// It lives in its own package rather than beside the tests that first needed
// it so the sync engines can use the same harness: a rule-engine test that
// runs against real rows and real SQL, with only Google faked out, is worth
// more than one that fakes the database too.
package dbtest

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ryakel/skulid/internal/db"
)

// EnvDatabaseURL points at a Postgres these tests may create and drop
// databases in. Unset, every test using this harness skips, so
// `go test ./...` stays fast and dependency-free for anyone without a server
// to hand.
//
// The gate is an environment variable rather than a build tag deliberately: a
// build-tagged file stops compiling the moment someone renames a repo method,
// and nobody notices until they run with the tag. This one compiles on every
// build and only its execution is conditional.
const EnvDatabaseURL = "SKULID_TEST_DATABASE_URL"

// New brings up a database of its own, runs every migration from scratch, and
// returns a pool onto it. Each test gets a fresh database, so nothing leaks
// between them and the migration chain is exercised on every run -- which is
// the point: 0011 and 0012 reached production having never been run against a
// real Postgres.
//
// Skips the test when EnvDatabaseURL is unset.
func New(t *testing.T) *pgxpool.Pool {
	t.Helper()

	base := BaseDSN(t)
	ctx := context.Background()

	admin, err := db.Connect(ctx, base)
	if err != nil {
		t.Fatalf("connecting to %s: %v", EnvDatabaseURL, err)
	}
	defer admin.Close()

	name := DBName(t)
	// A leftover from a killed run would otherwise fail the create.
	if _, err := admin.Exec(ctx, `DROP DATABASE IF EXISTS "`+name+`" WITH (FORCE)`); err != nil {
		t.Fatalf("dropping stale %s: %v", name, err)
	}
	if _, err := admin.Exec(ctx, `CREATE DATABASE "`+name+`"`); err != nil {
		t.Fatalf("creating %s: %v", name, err)
	}

	dsn := ReplaceDBName(base, name)
	if err := db.Migrate(ctx, dsn); err != nil {
		t.Fatalf("migrating %s: %v", name, err)
	}
	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connecting to %s: %v", name, err)
	}

	t.Cleanup(func() {
		pool.Close()
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		admin, err := db.Connect(cleanupCtx, base)
		if err != nil {
			return
		}
		defer admin.Close()
		_, _ = admin.Exec(cleanupCtx, `DROP DATABASE IF EXISTS "`+name+`" WITH (FORCE)`)
	})
	return pool
}

// BaseDSN returns the configured server DSN, skipping the test if there is
// none. Use it when you need the DSN itself rather than a pool.
func BaseDSN(t *testing.T) string {
	t.Helper()
	base := strings.TrimSpace(os.Getenv(EnvDatabaseURL))
	if base == "" {
		t.Skipf("set %s to run the Postgres integration tests", EnvDatabaseURL)
	}
	return base
}

// DBName derives a database name from the test's own name, so a failure leaves
// behind something identifiable rather than a random string.
func DBName(t *testing.T) string {
	t.Helper()
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

// ReplaceDBName swaps the database in a postgres:// DSN, leaving credentials,
// host and query parameters alone.
func ReplaceDBName(dsn, name string) string {
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

// SeedCalendar creates the account and calendar rows most repos need a foreign
// key onto, through the repos themselves so the write paths are covered too.
// The calendar arrives enabled, since every caller wants one that is.
func SeedCalendar(t *testing.T, pool *pgxpool.Pool, email, googleID string) (accountID, calendarID int64) {
	t.Helper()
	ctx := context.Background()

	expires := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	accountID, err := db.NewAccountRepo(pool).Upsert(ctx,
		"sub-"+email, email, "sealed-refresh", "sealed-access", &expires)
	if err != nil {
		t.Fatalf("seeding account %s: %v", email, err)
	}
	cals := db.NewCalendarRepo(pool)
	calendarID, err = cals.Upsert(ctx, accountID, googleID, googleID, "UTC", "#ff0000")
	if err != nil {
		t.Fatalf("seeding calendar %s: %v", googleID, err)
	}
	if err := cals.SetEnabled(ctx, calendarID, true); err != nil {
		t.Fatalf("enabling calendar %s: %v", googleID, err)
	}
	return accountID, calendarID
}
