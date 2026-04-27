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
  bidirectionally, with optional filtering and transformation.
- **Smart blocks** — auto-maintain focus/busy blocks on a target
  calendar based on busy time on others, respecting per-block working
  hours in any IANA timezone.
- **Webhook + polling** — Google push channels for near-real-time sync
  with a 5-minute polling fallback.
- **AI assistant** *(optional)* — chat with Claude to manage your
  calendars; every write requires a one-click confirmation.
- **Token sealing** — refresh tokens are AES-256-GCM encrypted at rest.
- **Single-user TOFU** — first Google login claims the instance; no
  one else can log in afterward.

## Quick start

```bash
git clone https://github.com/ryakel/skulid.git
cd skulid
cp .env.example .env
# fill in EXTERNAL_URL, Google OAuth, SESSION_SECRET, ENCRYPTION_KEY
docker compose up -d --build
```

Open `EXTERNAL_URL` in a browser, sign in with Google, you own the
instance. Full walkthrough in
[Getting Started](https://github.com/ryakel/skulid/wiki/Getting-Started).

## Documentation

The detailed docs live in the [GitHub Wiki](https://github.com/ryakel/skulid/wiki),
and are version-controlled in [`wiki/`](./wiki) for review alongside
code changes (synced to the Wiki by `.github/workflows/wiki-sync.yml`
on push to `main`).

| Page                                                                         | What's in it                                                       |
| ---------------------------------------------------------------------------- | ------------------------------------------------------------------ |
| [Home](https://github.com/ryakel/skulid/wiki/Home)                           | Index and orientation                                              |
| [Getting Started](https://github.com/ryakel/skulid/wiki/Getting-Started)     | Zero-to-running walkthrough                                        |
| [Architecture](https://github.com/ryakel/skulid/wiki/Architecture)           | Stack, data model, change flow                                     |
| [Sync Rules](https://github.com/ryakel/skulid/wiki/Sync-Rules)               | Filters, transforms, bidirectional, backfill, examples             |
| [Smart Blocks](https://github.com/ryakel/skulid/wiki/Smart-Blocks)           | Working hours, DST, recompute, examples                            |
| [AI Assistant](https://github.com/ryakel/skulid/wiki/AI-Assistant)           | Tools, confirmation flow, persistence                              |
| [Configuration](https://github.com/ryakel/skulid/wiki/Configuration)         | Every supported environment variable                               |
| [Operations](https://github.com/ryakel/skulid/wiki/Operations)               | Backups, watch renewal, audit log, troubleshooting                 |
| [Security Model](https://github.com/ryakel/skulid/wiki/Security-Model)       | Threat model and what we do/don't protect against                  |
| [Development](https://github.com/ryakel/skulid/wiki/Development)             | Local setup, conventions, adding features                          |

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
