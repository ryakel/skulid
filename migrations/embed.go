// Package migrations exposes the goose-managed schema as an embed.FS so the
// app can run migrations on startup without shipping the .sql files separately.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
