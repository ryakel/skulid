# CLAUDE.md

Guide for Claude Code (or any LLM-assisted contributor) working in this
repo.

## What this is

skulid: self-hosted, single-user Google Calendar sync. One Go
binary + Postgres in Docker. Two core features (sync rules, smart
blocks) plus an optional Anthropic-powered chat assistant.

The user-facing docs live in `wiki/` and are mirrored to the GitHub
Wiki by `.github/workflows/wiki-sync.yml`. Read those first if you
need product context — they cover threat model, architecture, every
configuration knob, and operations.

## Repo map

```
cmd/skulid/main.go             # entrypoint; wires every dependency
internal/
  ai/                          # Anthropic assistant (only enabled when ANTHROPIC_API_KEY is set)
  auth/                        # OAuth, sessions (HMAC-SHA256), TOFU, middleware
  calendar/                    # Google Calendar v3 client wrapper + extendedProperties helpers
  category/                    # pure event categorizer (no I/O, exhaustively tested)
  config/                      # env-var loading
  crypto/                      # AES-256-GCM token sealing
  db/                          # pgx repos + scanned struct models
  hours/                       # pure WorkingHours + window arithmetic + slot finders
  httpx/                       # chi router, html/template + HTMX, handlers
  sync/                        # rule engine + smart-block engine + scheduler (tasks/habits)
  webhook/                     # Google push notification handler
  worker/                      # per-account goroutines + scheduler tick + AI cleanup
migrations/                    # *.sql, embedded into the binary via embed.FS
wiki/                          # user-facing docs, synced to GitHub Wiki
```

## Common commands

| Task                       | Command                                  |
| -------------------------- | ---------------------------------------- |
| Build                      | `go build ./...`                         |
| Vet                        | `go vet ./...`                           |
| Tests (fast)               | `go test ./...`                          |
| Tests (race + fresh cache) | `go test -race -count=1 ./...`           |
| Boot the stack             | `docker compose up -d --build`           |
| Tail app logs              | `docker compose logs -f app`             |
| Reset everything           | `docker compose down -v`                 |

## Conventions worth respecting

### Code

- **No ORM, no codegen.** Repos are thin structs over `*pgxpool.Pool`
  with explicit SQL. Returning `(nil, nil)` on `pgx.ErrNoRows` is the
  convention for "not found is not an error".
- **Pure logic stays pure.** Code under `internal/sync/` (filter,
  transform, smart-block helpers) takes no `context.Context` and does
  no I/O. That's why it's exhaustively tested.
- **Wire in `main.go`.** No global state. Every dependency is
  constructor-injected.
- **Comments explain *why*, never *what*.** If a name is bad, fix the
  name; if a comment restates the code, delete the comment.
- **Errors return up.** The logger lives on the engine/worker struct
  and is only used for fire-and-forget background failures (debounced
  recompute, daily cleanup) where there's no caller.
- **html/template only.** Never assemble HTML by string concatenation.
  All user-controlled data goes through escape-aware templates.

### Loop guards

Every event skulid writes to Google sets
`extendedProperties.private` keys that are checked before forwarding:

| Key                       | Set by                                    |
| ------------------------- | ----------------------------------------- |
| `skulidManaged=1`         | every write (rules, blocks, tasks, habits, buffers, AI) |
| `skulidRuleId`            | sync rule mirror writes                   |
| `skulidSourceEventId`     | sync rule mirror writes                   |
| `skulidSmartBlockId`      | smart block writes                        |
| `skulidTaskId`            | task scheduler writes                     |
| `skulidHabitId`           | habit scheduler writes                    |
| `skulidBufferType`        | "decompression" / "travel" — buffer engine writes |
| `skulidBufferForEventId`  | Google ID of the meeting a buffer trails  |
| `skulidAiSession`         | AI assistant writes                       |

`IsManaged()` recognizes both the `skulid*` keys and the legacy
`calmAxolotl*` keys (pre-rename) so any old managed event still
trips the loop guard. Don't remove the legacy check until we're
confident no pre-rename events exist in production.

The rule engine refuses to forward any event where
`calendar.IsManaged(ev) == true`. **Don't break this guarantee** —
without it, two bidirectional rules can ping-pong indefinitely.

### Tests

The test suite covers pure logic only. Postgres and Google Calendar
integration tests are deferred (see `wiki/Development.md`). When you
add new pure helpers, add tests. When you change something covered by
the suite, update the suite.

## Areas that require care

### Adding a migration

1. New file: `migrations/000N_my_change.sql`.
2. Use `-- +goose Up` / `-- +goose Down`. Wrap multi-statement bodies
   in `-- +goose StatementBegin` / `-- +goose StatementEnd`.
3. The file is auto-embedded via `migrations/embed.go`.
4. **Don't edit existing migrations.** Add a new one.

### Touching the worker

`internal/worker/worker.go` runs goroutines. Keep:

- One job queue per account; no cross-account locks.
- All goroutines listen for `m.stop` so shutdown is clean.
- Smart-block recompute is debounced (15s) per block — preserve that
  when adding new triggers.
- Decompression recompute is debounced (15s) per calendar; fires
  after every successful incremental sync.
- The 6-hour maintenance tick (`runMaintenance`) re-runs `PlaceHabit`
  and `PlaceTask` so rolling horizons stay current. Adding a new
  scheduler-driven entity? Hook it in there.

### Touching the sync engine

`internal/sync/rule_engine.go` is the trickiest file in the codebase.
Things to remember:

- Reverse passes for bidirectional rules use a synthetic `event_link`
  key: `"rev:" + ev.Id`. Forward and reverse must not collide on the
  unique index.
- Cancelled events delete the mirror — even if the filter no longer
  matches, deletion still flows through.
- Etag dedup (skip update when `ev.Etag == existing.SourceEtag` for
  bidirectional rules) prevents the inbound webhook → outbound update
  → inbound webhook loop.

### AI assistant

`internal/ai/` is gated by `ANTHROPIC_API_KEY`. When it's unset, the
package is essentially dormant — routes aren't registered, the nav
link is hidden. Keep it that way: don't make any other subsystem
require `ANTHROPIC_API_KEY` or the assistant.

Tool execution policy:

- Read tools auto-execute and return results to Claude immediately.
- Write tools (`create_event`, `update_event`, `delete_event`,
  `move_event`) are *staged*. They never hit Google until the user
  clicks **Apply** in the UI.
- Every assistant write writes to `audit_log` with `kind="ai"`.

## When to open a PR vs commit directly

The branch convention here is feature/init branches like
`claude/skulid-init-WUUlm`. Push commits to that branch; only
open a PR when the branch is ready for review. **Never** push to
`main` directly.

## Links

- User docs: [`wiki/`](./wiki) (also at GitHub Wiki).
- Architecture: [`wiki/Architecture.md`](./wiki/Architecture.md).
- Threat model: [`wiki/Security-Model.md`](./wiki/Security-Model.md).
- Repository: github.com/ryakel/skulid.
