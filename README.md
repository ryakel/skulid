# calm-axolotl

Self-hosted, single-user alternative to Reclaim.ai for Google Calendar.
You bring your own Google OAuth credentials and your own public HTTPS endpoint;
calm-axolotl runs as one Go binary plus a Postgres database in Docker.

Two features:

1. **Sync rules** — mirror events between your calendars with optional
   filtering and transformation. Works one-way or bidirectionally.
2. **Smart blocks** — automatically maintain "focus"/"busy" blocks on a
   target calendar based on busy time elsewhere, respecting per-block
   working hours in any IANA timezone.

## Quick start

1. Create an OAuth 2.0 **Web application** client in
   [Google Cloud Console](https://console.cloud.google.com/apis/credentials).
   Add `https://YOUR.HOST/auth/google/callback` as an authorized redirect URI.
   Enable the **Google Calendar API** for the project.
2. `cp .env.example .env` and fill it in. Generate the secrets:
   ```
   openssl rand -base64 48   # SESSION_SECRET
   openssl rand -base64 32   # ENCRYPTION_KEY
   ```
3. Make `EXTERNAL_URL` reachable over HTTPS. A Cloudflare tunnel or
   Tailscale Funnel is the easiest. (Plain `http://localhost` works for
   browsing but Google push notifications require HTTPS.)
4. `docker compose up -d --build`.
5. Open `EXTERNAL_URL` in a browser. The first Google account to sign in
   becomes the permanent owner of this instance (TOFU). Connect any
   additional accounts from **Accounts → Connect Google account**.

## Architecture

| Concern               | Implementation                                                  |
| --------------------- | --------------------------------------------------------------- |
| HTTP                  | `chi` + `html/template` (HTMX/Alpine sprinkled in)              |
| Persistence           | Postgres 16 via `pgx/v5`; migrations embedded with `goose`      |
| Token storage         | AES-256-GCM sealed (per-row nonce); key from `ENCRYPTION_KEY`   |
| Auth                  | Google OAuth + TOFU owner claim + HMAC-SHA256 session cookies   |
| Calendar API          | Google Calendar v3 client with sync tokens + freebusy           |
| Change notification   | Google push channels with HMAC token; 5-minute polling fallback |
| Loop protection       | `extendedProperties.private.calmAxolotlManaged` + `event_link`  |
| Concurrency           | One goroutine worker per account; debounced smart-block recompute |

## Development

```
go build ./...
go vet ./...
```

Run a local Postgres and export `.env` then:
```
go run ./cmd/calmaxolotl
```

## Backup

Everything important lives in Postgres. Snapshot the `db_data` volume (or
`pg_dump`) and keep your `ENCRYPTION_KEY` somewhere safe — the database
alone is useless without it.

## License

MIT.
