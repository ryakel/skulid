package db

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/ryakel/skulid/migrations"
)

// Migrations only ever run at startup against a live Postgres, so a malformed
// goose annotation block surfaces as a crash-looping container rather than a
// test failure. Check the framing here. This validates goose's Up/Down and
// StatementBegin/End structure, not the SQL itself.
func TestMigrationAnnotationsAreWellFormed(t *testing.T) {
	files, err := fs.Glob(migrations.FS, "*.sql")
	if err != nil {
		t.Fatalf("globbing migrations: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no migration files embedded")
	}

	for _, name := range files {
		t.Run(name, func(t *testing.T) {
			raw, err := migrations.FS.ReadFile(name)
			if err != nil {
				t.Fatalf("reading: %v", err)
			}
			sql := string(raw)

			up := strings.Index(sql, "-- +goose Up")
			down := strings.Index(sql, "-- +goose Down")
			if up < 0 {
				t.Error("missing `-- +goose Up`")
			}
			if down < 0 {
				t.Error("missing `-- +goose Down`")
			}
			if up >= 0 && down >= 0 && down < up {
				t.Error("Down block appears before Up")
			}

			begins := strings.Count(sql, "-- +goose StatementBegin")
			ends := strings.Count(sql, "-- +goose StatementEnd")
			if begins != ends {
				t.Errorf("unbalanced statement markers: %d Begin vs %d End", begins, ends)
			}
		})
	}
}

// The reauth columns are what the account repo now selects on every read, so a
// missing or renamed column means every account query fails at runtime.
func TestReauthMigrationDefinesAccountColumns(t *testing.T) {
	raw, err := migrations.FS.ReadFile("0011_account_reauth.sql")
	if err != nil {
		t.Fatalf("reading migration: %v", err)
	}
	sql := string(raw)

	for _, col := range []string{"needs_reauth", "reauth_reason", "reauth_detected_at"} {
		if !strings.Contains(sql, col) {
			t.Errorf("migration does not mention %q", col)
		}
		if !strings.Contains(accountSelectCols, col) {
			t.Errorf("accountSelectCols does not read %q", col)
		}
	}
}
