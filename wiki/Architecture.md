# Architecture

A 10,000-foot tour of how skulid is built and how data flows
through it.

## Component map

```
┌─────────────────────────────────────────────────────────────────┐
│                      skulid (one Go binary)                │
│                                                                 │
│  HTTP layer (chi)         Engines              Workers          │
│  ┌──────────────┐         ┌────────────┐     ┌───────────────┐ │
│  │ Web UI       │         │ Sync rule  │◄────┤ Per-account   │ │
│  │ (htmx/      │────────►│ engine     │     │ goroutine     │ │
│  │ Alpine.js)  │         │            │     │ pool          │ │
│  └──────────────┘         └────────────┘     │               │ │
│  ┌──────────────┐         ┌────────────┐     │ Polling       │ │
│  │ Webhook      │────────►│ Smart      │◄────┤ fallback      │ │
│  │ /api/...     │         │ block      │     │ (5 min)       │ │
│  └──────────────┘         │ engine     │     │               │ │
│  ┌──────────────┐         └────────────┘     │ Watch         │ │
│  │ AI assistant │              ▲             │ renewal       │ │
│  │ /assistant   │──────┐       │             │ (24h before   │ │
│  └──────────────┘      │       │             │  expiry)      │ │
│                        ▼       ▼             └───────────────┘ │
│  ┌──────────────────────────────────────────────────────────┐ │
│  │ Repositories (pgx-backed)                                 │ │
│  └──────────────────────────────────────────────────────────┘ │
└──────────────────────────┬──────────────────────────────────────┘
                           │
                ┌──────────┴──────────┐
                ▼                     ▼
         ┌────────────┐         ┌────────────┐
         │ Postgres   │         │ Google     │
         │            │         │ Calendar   │
         │ Token      │         │ API        │
         │ ciphertexts│         │            │
         │ stored     │         │            │
         │ here.      │         │            │
         └────────────┘         └────────────┘
```

## Stack

| Layer            | Choice                                                |
| ---------------- | ----------------------------------------------------- |
| Language         | Go 1.25+                                              |
| HTTP             | `chi` router + stdlib `html/template`                 |
| Frontend         | Server-rendered HTML, sprinkles of HTMX and Alpine.js |
| Database         | Postgres 16 via `pgx/v5` (no ORM)                     |
| Migrations       | `goose`, embedded with `embed.FS`                     |
| Calendar API     | Google Calendar API v3 (official Go client)           |
| OAuth            | `golang.org/x/oauth2`                                 |
| Token sealing    | AES-256-GCM with per-row nonces                       |
| Sessions         | HMAC-SHA256-signed cookies                            |
| Container        | Multi-stage build → distroless `static-debian12`      |

## Data model

| Table                | Purpose                                                                |
| -------------------- | ---------------------------------------------------------------------- |
| `setting`            | Owner identity (TOFU), external URL, buffers, schema version           |
| `account`            | One Google account; sealed tokens; per-account Working/Personal/Meeting hours |
| `calendar`           | Each visible calendar belonging to an account; optional default category |
| `sync_token`         | Per-calendar Google sync token + push channel state                    |
| `sync_rule`          | A rule mirroring source → target. Visibility preset + all-day mode + working-hours-only + optional category pin |
| `event_link`         | Links a source event to its mirror; loop-guard primary key             |
| `smart_block`        | A focus-block recipe (target, sources, working hours, horizon)         |
| `managed_block`      | Each focus block we've actually written to Google                      |
| `category`           | Eight built-in categories (slug, name, color); user-editable name+color |
| `task`               | One-shot scheduled work; priority + duration + due + scheduled placement |
| `habit`              | Recurring soft block (e.g. Lunch); ideal_time + flex + days_of_week    |
| `habit_occurrence`   | Per-day instance of a habit (event id + window)                        |
| `audit_log`          | What skulid did and why                                                |
| `ai_conversation`    | One AI assistant chat (30-day TTL)                                     |
| `ai_message`         | One turn within a conversation                                         |
| `ai_pending_action`  | Tool call awaiting human confirmation                                  |

## Change flow

### Inbound: a Google calendar event changed

1. Google → POST `/api/webhooks/google` with `X-Goog-Channel-Id`.
2. Webhook handler verifies the per-channel HMAC token in
   `X-Goog-Channel-Token`, looks up the matching `sync_token` row,
   enqueues a job onto the per-account worker.
3. Per-account worker picks up the job, calls Calendar API
   `Events.list` with the stored sync token (incremental sync).
4. For each changed event, the rule engine processes it: looks up
   matching rules, applies filter+transform, inserts/updates/deletes the
   mirror via `Events.insert/update/delete`, records an `event_link`.
5. Smart-block engine recompute is debounced (15s) per affected block,
   then runs: pulls busy windows via `Freebusy.query`, subtracts from
   working hours, diffs against existing managed blocks.

If the webhook is dropped (network glitch, channel expired), the
**polling fallback** picks up the slack: every 5 minutes the scheduler
walks `sync_token` rows whose `last_polled_at` is stale and triggers an
incremental sync regardless.

### Outbound: a skulid write

Every write to Google sets `extendedProperties.private`:

```json
{
  "skulidManaged": "1",
  "skulidRuleId": "42",
  "skulidSourceEventId": "abc123"
}
```

Smart-block writes use `skulidSmartBlockId` instead of the rule
fields. When the corresponding webhook bounces back, the rule engine
sees `IsManaged() == true` and refuses to forward it — that's the
primary loop guard. The `event_link` table is the secondary guard for
bidirectional rules: if the source etag hasn't changed since the last
sync, the engine skips the update.

## Concurrency model

- One **goroutine per Google account**. Jobs queue per-account so a slow
  account never blocks the others.
- One global **scheduler** runs the polling fallback and watch-channel
  renewal loops.
- Smart-block recompute is **debounced** (15s) per block — useful when
  many events change in a burst (e.g. the user pastes 30 invites).
- All Google API calls are wrapped in `context.Context` with sensible
  per-request deadlines.

## Where things live

```
cmd/skulid/main.go             # entrypoint, wires every dependency
internal/
  config/                      # env-var loading
  crypto/                      # AES-256-GCM token sealing
  db/                          # pgx repos + scanned struct models
  auth/                        # OAuth, sessions, TOFU, middleware
  calendar/                    # Google Calendar client + extendedProperties helpers
  category/                    # pure event-classification heuristics
  hours/                       # pure WorkingHours + window arithmetic + slot finders
  sync/                        # rule engine + smart-block engine + scheduler (tasks/habits)
  worker/                      # per-account workers + scheduler tick + AI cleanup
  webhook/                     # Google push notification handler
  httpx/                       # chi router, html/template, handlers
  ai/                          # Anthropic-powered assistant (optional)
migrations/                    # *.sql, embedded into the binary
wiki/                          # this documentation, synced to GitHub Wiki
```

See [Development](Development) for hacking on the codebase.
