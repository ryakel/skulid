# Development

For people hacking on skulid itself.

## Local setup

```bash
git clone https://github.com/ryakel/skulid.git
cd skulid

# Run a Postgres for development.
docker run -d --name skulid-pg \
  -e POSTGRES_USER=skulid \
  -e POSTGRES_PASSWORD=changeme \
  -e POSTGRES_DB=skulid \
  -p 5432:5432 \
  postgres:16-alpine

cp .env.example .env
# fill in EXTERNAL_URL (use a tunnel for OAuth round trips),
# the Google credentials, SESSION_SECRET, ENCRYPTION_KEY
export $(grep -v '^#' .env | xargs)

go run ./cmd/skulid
```

For development without OAuth, set:

```ini
SKULID_DEV_AUTH_BYPASS=1
SKULID_DEV_USER_EMAIL=dev@local   # optional, defaults to dev@local
```

If you are also using placeholder secrets or a plain-http `EXTERNAL_URL` —
typical for pure UI work — you need the second opt-in as well, because the
daemon otherwise refuses to start on them:

```ini
SKULID_ALLOW_INSECURE_CONFIG=1
```

They are deliberately separate flags: one bypasses authentication, the other
accepts unsafe secrets. Wanting one does not imply wanting the other. See
[Configuration → Refusing to start on unsafe values](Configuration#refusing-to-start-on-unsafe-values).

This registers `GET /dev/login`. Hitting that route claims TOFU as the
synthetic `dev@local` (or whichever email you set) and issues a real
session cookie. From then on every owner-protected page works exactly
like prod — no Google round-trip needed.

Visible safeguards so the flag never sneaks into production:

- The daemon logs a `WARN` at startup naming the synthetic user.
- The login page shows a "Skip OAuth →" link to `/dev/login`.
- Every rendered page carries a yellow `DEV AUTH BYPASS` banner.
- The `/dev/login` route is **only** registered when the env var is
  set — there is no code path in the prod build that grants a session
  without OAuth.

Connecting real Google calendars still requires the actual OAuth
flow (the bypass doesn't fake calendar API responses). For mockup
review without any real connections, `/dev/login` is enough — just
the calendar/event-listing pages will be empty.

## Build & test

```bash
go build ./...
go vet ./...
go test ./...
go test -race -count=1 ./...
```

The default run covers pure logic (filter, transform, smart-block
helpers, slot finders, crypto, sessions, calendar managed-event
helpers, httpx helpers, renderer smoke test). Postgres-backed tests
also live in the suite but skip unless you point them at a server —
see [#integration-tests](#integration-tests). Those drive the rule
engine and the task scheduler end-to-end, against real rows and a fake
Google.

## Security scanning

CI runs two scans, deliberately in different places:

| Scan | Where | Posture |
| --- | --- | --- |
| `govulncheck ./...` | its own `security` job | **fails the job** |
| Trivy on the container image | the `docker` job, after build | report-only, SARIF to the Security tab |

`govulncheck` reports only vulnerabilities *reachable* from this code, so it
is quiet enough to gate on. It lives in a separate job on purpose: a
vulnerable dependency should redden the PR loudly without also stranding the
image build and the Portainer redeploy.

The image scan is report-only because it mostly surfaces the distroless base,
where a CRITICAL/HIGH often has no fix available yet — and refusing to deploy
over an unfixable base CVE is its own kind of outage. It runs with
`ignore-unfixed`, so what it does report is actionable.

To run the Go scan locally:

```bash
go install golang.org/x/vuln/cmd/govulncheck@v1.7.0
govulncheck ./...
```

It needs network access to `vuln.go.dev` for the vulnerability database.

Dependency bumps arrive as PRs from Dependabot (`.github/dependabot.yml`),
weekly and grouped, covering Go modules, both Dockerfile stages, and the
pinned GitHub Actions.

## Project layout

```
cmd/skulid/           # main.go entrypoint
internal/
  ai/                 # Anthropic-powered assistant (optional feature)
  auth/               # OAuth, sessions, TOFU, middleware
  calendar/           # Google Calendar v3 wrapper + ext-properties helpers
    calfake/          # in-memory calendar.API for tests
  category/           # pure event categorizer (no I/O)
  config/             # env-var loading
  crypto/             # AES-256-GCM token sealing
  db/                 # pgx repos + scanned models
    dbtest/           # throwaway Postgres harness for tests
  hours/              # pure window/working-hours helpers + slot finders
  httpx/              # chi router, templates, handlers
  sync/               # rule engine, smart-block engine, task/habit scheduler
  webhook/            # Google push handler
  worker/             # per-account workers + scheduler tick + AI cleanup
migrations/           # *.sql, embedded into the binary
wiki/                 # this documentation, synced to GitHub Wiki
```

## Conventions

- **Repos** live under `internal/db/`. Each one is a thin struct over
  `*pgxpool.Pool` with explicit query strings (no ORM, no codegen yet).
  Returning `(nil, nil)` on `pgx.ErrNoRows` is the convention for
  "not found is not an error".
- **Pure logic stays pure.** `internal/sync/filter.go`,
  `internal/sync/transform.go`, and the helpers in
  `internal/sync/smart_block.go` (parseRange, mergeWindows,
  subtractBusy, etc.) take no `context.Context` and do no I/O. That's
  why they're easy to test.
- **No global state.** Everything is wired in `main.go`.
- **Errors are returned, not logged then swallowed.** The logger lives
  on the worker/engine struct and is used for fire-and-forget failures
  (e.g. background recompute) where there's no caller to return to.
- **Comments answer "why", never "what".**

## Adding a new sync filter dimension

1. Add the field to `internal/sync/filter.go`.
2. Implement the matcher inside `Filter.Match`.
3. Add a test in `filter_test.go`.
4. Add a form input in `internal/httpx/templates/rule_edit.html`.
5. Map the form value into the `Filter` struct in `handleRuleSave` in
   `internal/httpx/handlers.go`.

## Adding a migration

1. Create `migrations/000N_my_change.sql` with `-- +goose Up` and
   `-- +goose Down` sections.
2. Use `-- +goose StatementBegin` / `-- +goose StatementEnd` if your
   statement contains semicolons.
3. The file is auto-embedded via `migrations/embed.go` — no other
   bookkeeping needed.

## Adding an AI tool

1. Define the tool schema in `internal/ai/tools.go`.
2. Implement the executor in the same file.
3. If it's destructive, list it in the `destructive` set so it requires
   confirmation.
4. Update [AI Assistant](AI-Assistant) docs with the new tool's
   behavior.

## Integration tests

Postgres-backed tests live in `internal/db/*_integration_test.go` and
run in CI against a `postgres:16` service container. They exist because
every repo is hand-written SQL with no compile-time checking — a column
added to a table but missed in a select list is a runtime failure on
every read — and because migrations `0011` and `0012` once reached
production having never been run against a real server.

Each test creates a database of its own, runs every migration from
scratch, and drops it again on cleanup. So the whole migration chain is
exercised on every run, and `TestMigrationsAreReversible` additionally
runs every `Down` and then every `Up` again, which is where a
hand-written rename goes wrong.

They're gated on an environment variable rather than a build tag:

```bash
# any Postgres you don't mind the suite creating and dropping databases in
export SKULID_TEST_DATABASE_URL='postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable'
go test -race -count=1 ./...
```

Unset it and they skip, so `go test ./...` stays fast and needs nothing
installed. A build tag would have worked too, but a build-tagged file
stops compiling the moment someone renames a repo method and nobody
notices until they next run with the tag; this way the file compiles on
every build and only its execution is conditional.

The DSN should point at a database you're happy for the suite to create
and drop databases *next to* — it connects there as an admin and works
in throwaway databases named after each test.

### The calendar fake

`internal/calendar/calfake` is an in-memory `calendar.API`. It is a fake
rather than a mock: it keeps real events in a map and answers reads from
them, so a test asserts on the state Google would have ended up in
rather than on a sequence of expected calls. That reads better for
engines whose whole contract is "insert, update and delete these
events".

`Seed` puts events there without recording a write; `Calls` and
`CallsOf` return the writes the engine made, in order; `Err` makes a
named operation fail so error handling can be exercised without a real
API. `FreeBusy` is derived from the seeded events, so one setup serves
both the sync and the scheduling paths.

Combined with `dbtest`, that gives a genuine end-to-end: real rows, real
SQL, fake Google. `internal/sync/rule_engine_integration_test.go` covers
the loop guard (including the legacy `calmAxolotl*` keys), etag dedup on
bidirectional rules, cancellation deleting a mirror the filter would now
reject, `filter_drop`, and the `rev:` key that keeps forward and reverse
passes off each other's `event_link` row.
`scheduler_integration_test.go` covers all three branches of the chunk
reconcile.

Each of those was checked by breaking the invariant and confirming the
test fails — a test that passes either way is worse than no test.

### Writing a test that needs a calendar

```go
pool := dbtest.New(t)
_, calID := dbtest.SeedCalendar(t, pool, "owner@example.com", "primary")

fake := calfake.New()
fake.Seed("primary", someEvent)

clientFor := func(context.Context, int64) (calendar.API, error) { return fake, nil }
```

Nothing outside `internal/calendar` touches `*calendar.Service`, so a
fake only has to satisfy the ten methods on `calendar.API`.

## Wiki

The `wiki/` folder is synced to the GitHub Wiki by
`.github/workflows/wiki-sync.yml` on every push to `main`. To preview
locally, just open the `.md` files in any markdown reader.

## Releasing

Releasing is automatic. Merging to `main` runs
`.github/workflows/build-and-publish.yml`, which:

1. Reads the conventional-commit messages in the merge and **bumps a semver
   tag** accordingly (`feat:` → minor, `fix:`/`chore:` → patch).
2. Builds a multi-arch image (`linux/amd64,linux/arm64`) and pushes
   `:latest` and `:vX.Y.Z` to **GHCR**, plus an internal registry when
   `INTERNAL_REGISTRY` is configured.
3. Fires the Portainer redeploy webhook when `PORTAINER_WEBHOOK_URL` is set.

So **don't tag by hand** on `main` — the workflow does it, and a manual tag
just fights the bump. Pushing a `vX.Y.Z` tag directly is a separate,
supported path: it publishes that single immutable image and skips the
version bump and the release entry.

The image is published, not built on each machine. `docker-compose.yml`
pulls `ghcr.io/ryakel/skulid` and contains no `build:` stanza at all — see
[Operations → Upgrades](Operations#upgrades).

Note the workflow's `paths-ignore`: changes confined to `**/*.md`,
`wiki/**`, `LICENSE*`, `.gitignore` or `CLAUDE.md` run no CI and publish no
image, on both `push` and `pull_request`. A docs-only PR therefore shows no
checks at all — that's expected, not a stuck build.
