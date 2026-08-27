# CLAUDE.md

Guide for Claude Code (or any LLM-assisted contributor) working in this
repo.

## What this is

skulid: self-hosted, single-user Google Calendar sync. One Go
binary + Postgres in Docker. Two core features (sync rules, smart
blocks) plus an optional Anthropic-powered chat assistant.

The user-facing docs live in `wiki/` and are mirrored to the GitHub
Wiki by `.github/workflows/wiki-sync.yml`. Read those when you need
product context — they cover threat model, architecture, every
configuration knob, and operations. Code layout follows the standard
`cmd/` + `internal/` Go shape; `wiki/Architecture.md` maps it.

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
`calendar.IsManaged(ev) == true`. Without this guarantee, two
bidirectional rules can ping-pong indefinitely.

### Tests

The suite covers pure logic plus Postgres-backed repo tests. The
Postgres ones skip unless `SKULID_TEST_DATABASE_URL` points at a server
you don't mind them creating and dropping databases in; CI sets it
against a service container. Google Calendar is still faked out only by
absence — driving the rule engine end-to-end is open (see
`wiki/Development.md`).

When you add new pure helpers, add tests. When you add a column or a
migration, extend the matching repo round-trip test — that is what
catches a select list you forgot to update. Run with
`go test -race -count=1 ./...`.

## Areas that require care

### Adding a migration

1. New file: `migrations/000N_my_change.sql`.
2. Use `-- +goose Up` / `-- +goose Down`. Wrap multi-statement bodies
   in `-- +goose StatementBegin` / `-- +goose StatementEnd`.
3. The file is auto-embedded via `migrations/embed.go`.
4. **Don't edit existing migrations.** Add a new one.
5. Run it: `SKULID_TEST_DATABASE_URL=... go test ./internal/db/`.
   `TestMigrationsApplyFromScratch` applies the whole chain and
   `TestMigrationsAreReversible` takes it all the way down and back up,
   so a `Down` that doesn't undo its `Up` fails there. Add any new table
   to the list in the first of those.

### Touching the worker

`internal/worker/worker.go` runs goroutines. Keep:

- One job queue per account; no cross-account locks.
- All goroutines listen for `m.stop` so shutdown is clean.
- Smart-block recompute is debounced (15s) per block — preserve that
  when adding new triggers.
- Buffer recompute (decompress + travel) is debounced (15s) per
  calendar; fires after every successful incremental sync.
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

## Branches, commits, and PRs

- **`main` is the default branch.** Never push to it directly.
- **Feature branches: `claude/<topic>`** — short, descriptive,
  topic-scoped (e.g. `claude/travel-buffers`,
  `claude/scheduling-links`). Avoid stamped/random suffixes.
- **Commit style: small + focused.** One logical change per commit,
  with a descriptive message. Don't batch unrelated work into a
  single commit even if it's all going into the same PR.
- **PR style: big + thematic.** A PR can carry several commits that
  together make one coherent feature; the *PR* is the unit of
  review, not the commit. Write the PR description so each commit
  in the list is legible at a glance.
- **Owner reviews and merges.** Open the PR ready-to-merge, hand
  off, then move on to the next branch.

## Links

- User docs: [`wiki/`](./wiki) (also at GitHub Wiki).
- Architecture: [`wiki/Architecture.md`](./wiki/Architecture.md).
- Threat model: [`wiki/Security-Model.md`](./wiki/Security-Model.md).
- Repository: github.com/ryakel/skulid.
