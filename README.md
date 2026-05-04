# skulid

> Self-hosted, single-user Google Calendar sync — a Reclaim.ai alternative
> you can run on a homelab box.

skulid is one Go binary plus a Postgres database, packaged as a
Docker Compose stack. You bring your own Google OAuth credentials and
your own public HTTPS endpoint; skulid mirrors events between
your calendars on rules you define and maintains automatic
focus/availability blocks based on busy time elsewhere. An optional
Claude-powered chat lets you talk to your calendars.

## Features

- **Sync rules** — mirror events between calendars one-way or
  bidirectionally, with Reclaim-style 4-level visibility presets,
  all-day handling modes, and a working-hours-only toggle.
- **Smart blocks** — auto-maintain focus/busy blocks on a target
  calendar based on busy time elsewhere.
- **Tasks** — one-shot work the scheduler auto-places in your next
  available Working-hours slot.
- **Habits** — recurring soft blocks (Lunch, Decompress) that drift
  near an ideal time within ±flex.
- **Categories** drive event color-coding and weekly hour totals.
- **Planner** — week timeline of every connected calendar.
- **Priorities** — Kanban of active tasks by priority bucket.
- **Buffers** — padding the scheduler keeps around busy time.
- **AI assistant** *(optional)* — chat with Claude; 17 tools across
  events, tasks, and habits; every write requires confirmation.
- **Webhook + polling** — Google push channels with 5-minute fallback.
- **Token sealing** — refresh tokens AES-256-GCM encrypted at rest.
- **Single-user TOFU** — first Google login claims the instance.

## Quick start

```bash
git clone https://github.com/ryakel/skulid.git
cd skulid
cp .env.example .env
# fill in EXTERNAL_URL, Google OAuth, SESSION_SECRET, ENCRYPTION_KEY
docker compose up -d --build
```

Or pull the prebuilt multi-arch image straight from
[GitHub Container Registry](https://github.com/ryakel/skulid/pkgs/container/skulid):

```bash
docker pull ghcr.io/ryakel/skulid:latest
```

Tags `latest` and `vX.Y.Z` are published on every push to `main` by
`.github/workflows/build-and-publish.yml`.

Open `EXTERNAL_URL` in a browser, sign in with Google, you own the
instance. Full walkthrough in
[Getting Started](https://github.com/ryakel/skulid/wiki/Getting-Started).

## Documentation

The detailed docs live in the [GitHub Wiki](https://github.com/ryakel/skulid/wiki),
and are version-controlled in [`wiki/`](./wiki) for review alongside
code changes (synced to the Wiki by `.github/workflows/wiki-sync.yml`
on push to `main`).

| Page                                                                       | What's in it                                                       |
| -------------------------------------------------------------------------- | ------------------------------------------------------------------ |
| [Home](https://github.com/ryakel/skulid/wiki/Home)                         | Index and orientation                                              |
| [Getting Started](https://github.com/ryakel/skulid/wiki/Getting-Started)   | Zero-to-running walkthrough                                        |
| [Architecture](https://github.com/ryakel/skulid/wiki/Architecture)         | Stack, data model, change flow                                     |
| [Planner](https://github.com/ryakel/skulid/wiki/Planner)                   | Week timeline view                                                 |
| [Tasks](https://github.com/ryakel/skulid/wiki/Tasks)                       | Auto-scheduled one-shot blocks                                     |
| [Habits](https://github.com/ryakel/skulid/wiki/Habits)                     | Recurring soft blocks                                              |
| [Priorities](https://github.com/ryakel/skulid/wiki/Priorities)             | Kanban view of active tasks                                        |
| [Sync Rules](https://github.com/ryakel/skulid/wiki/Sync-Rules)             | Visibility modes, all-day, working-hours-only, filters             |
| [Smart Blocks](https://github.com/ryakel/skulid/wiki/Smart-Blocks)         | Working hours, DST, recompute, examples                            |
| [Categories](https://github.com/ryakel/skulid/wiki/Categories)             | Built-in palette + auto-categorization heuristics                  |
| [Hours](https://github.com/ryakel/skulid/wiki/Hours)                       | Working/Personal/Meeting windows per account                       |
| [Buffers](https://github.com/ryakel/skulid/wiki/Buffers)                   | Padding around scheduled blocks                                    |
| [AI Assistant](https://github.com/ryakel/skulid/wiki/AI-Assistant)         | Tools, confirmation flow, persistence                              |
| [Configuration](https://github.com/ryakel/skulid/wiki/Configuration)       | Every supported environment variable                               |
| [Operations](https://github.com/ryakel/skulid/wiki/Operations)             | Backups, watch renewal, audit log, troubleshooting                 |
| [Security Model](https://github.com/ryakel/skulid/wiki/Security-Model)     | Threat model and what we do/don't protect against                  |
| [Development](https://github.com/ryakel/skulid/wiki/Development)           | Local setup, conventions, adding features                          |

## Status

Beta. Schema is at v1 with a single migration; breaking changes will
get a numbered migration. The token sealing key is not auto-rotated —
back up your `ENCRYPTION_KEY` somewhere offline.

## Stack

Go 1.25 · chi · pgx + Postgres 16 · goose · Google Calendar API v3 ·
HTMX + Alpine.js · distroless. See
[Architecture](https://github.com/ryakel/skulid/wiki/Architecture)
for the full map.

## Contributing

Open an issue first for anything beyond a small fix. The codebase
prefers explicit code over abstraction — see
[Development](https://github.com/ryakel/skulid/wiki/Development) for
conventions.

## License

MIT.
