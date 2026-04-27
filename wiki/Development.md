# Development

For people hacking on calm-axolotl itself.

## Local setup

```bash
git clone https://github.com/ryakel/skulid.git
cd skulid

# Run a Postgres for development.
docker run -d --name calmaxolotl-pg \
  -e POSTGRES_USER=calmaxolotl \
  -e POSTGRES_PASSWORD=changeme \
  -e POSTGRES_DB=calmaxolotl \
  -p 5432:5432 \
  postgres:16-alpine

cp .env.example .env
# fill in EXTERNAL_URL (use a tunnel for OAuth round trips),
# the Google credentials, SESSION_SECRET, ENCRYPTION_KEY
export $(grep -v '^#' .env | xargs)

go run ./cmd/calmaxolotl
```

For development without OAuth, you can:

1. Skip the login flow entirely by inserting an owner row directly:
   ```sql
   INSERT INTO setting (key, value) VALUES ('owner_google_sub', 'dev'),
                                            ('owner_email', 'dev@local');
   ```
2. Forge a session cookie — but the simpler path is just running with
   real OAuth via a [Cloudflare tunnel](Getting-Started#prerequisites).

## Build & test

```bash
go build ./...
go vet ./...
go test ./...
go test -race -count=1 ./...
```

The test suite covers pure logic (filter, transform, smart-block
helpers, crypto, sessions, calendar managed-event helpers, httpx
helpers, renderer smoke test). Integration tests against Postgres and
Google are deferred — see [#integration-tests-are-deferred](#integration-tests-are-deferred).

## Project layout

```
cmd/calmaxolotl/      # main.go entrypoint
internal/
  ai/                 # Anthropic-powered assistant (optional feature)
  auth/               # OAuth, sessions, TOFU, middleware
  calendar/           # Google Calendar v3 wrapper
  config/             # env-var loading
  crypto/             # AES-256-GCM token sealing
  db/                 # pgx repos + scanned models
  httpx/              # chi router, templates, handlers
  sync/               # rule engine + smart-block engine (pure logic)
  webhook/            # Google push handler
  worker/             # per-account workers + scheduler
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

## Integration tests are deferred

We deliberately don't run integration tests against Postgres or the
real Google API in CI. To get there:

- Postgres: `dockertest` or Testcontainers, gated behind a build tag.
- Google: refactor `*calendar.Client` into an interface so a fake can
  be injected, then assert the rule engine's behavior end-to-end.

Both are good first PRs.

## Wiki

The `wiki/` folder is synced to the GitHub Wiki by
`.github/workflows/wiki-sync.yml` on every push to `main`. To preview
locally, just open the `.md` files in any markdown reader.

## Releasing

There's no formal release cadence. Tag a commit on `main`:

```bash
git tag -a v0.1.0 -m "first usable release"
git push --tags
```

The Docker image isn't published anywhere — the `Dockerfile` and
`docker-compose.yml` build it locally on each machine.
