# Configuration

skulid is configured entirely via environment variables. The
canonical example is [`.env.example`](https://github.com/ryakel/skulid/blob/main/.env.example)
in the repo root.

## Required

| Variable               | Meaning                                                      |
| ---------------------- | ------------------------------------------------------------ |
| `EXTERNAL_URL`         | Public HTTPS URL the daemon answers on (no trailing slash)   |
| `GOOGLE_CLIENT_ID`     | OAuth client ID from Google Cloud Console                    |
| `GOOGLE_CLIENT_SECRET` | OAuth client secret                                          |
| `SESSION_SECRET`       | Random string used to sign session cookies (≥32 bytes)       |
| `ENCRYPTION_KEY`       | Base64 of 32 random bytes; AES-256-GCM key for token storage |
| `DATABASE_URL`         | Postgres DSN (`postgres://user:pass@host:5432/db?sslmode=disable`) |

Generate the secrets:

```bash
openssl rand -base64 48   # SESSION_SECRET
openssl rand -base64 32   # ENCRYPTION_KEY
```

## Optional

| Variable             | Default          | Meaning                                      |
| -------------------- | ---------------- | -------------------------------------------- |
| `LISTEN_ADDR`        | `:8567`          | TCP address the HTTP server binds to         |
| `ANTHROPIC_API_KEY`  | unset (off)      | Enable the AI assistant; see [AI Assistant](AI-Assistant) |
| `ANTHROPIC_MODEL`    | `claude-opus-4-7` | Model the assistant uses                    |

## Development overrides

These exist for UI mockup work without doing a real Google OAuth
round-trip. **Never set in production** — the daemon logs a `WARN`
at startup and every page renders a yellow `DEV AUTH BYPASS` banner
when on, but it's still your responsibility to keep them out of
prod env files.

| Variable                  | Default     | Meaning                                                                 |
| ------------------------- | ----------- | ----------------------------------------------------------------------- |
| `SKULID_DEV_AUTH_BYPASS`  | unset (off) | Truthy (`1`/`true`/`yes`/`on`) registers `GET /dev/login`; hitting that route claims TOFU as `SKULID_DEV_USER_EMAIL` and issues a real session. |
| `SKULID_DEV_USER_EMAIL`   | `dev@local` | Email recorded as the synthetic owner.                                  |

In `docker-compose.yml`, set them in the `app` service `environment:` block (or a `.env` file at the repo root that compose reads automatically). They're just regular env vars — no special wiring beyond the daemon checking for them at startup.

```yaml
services:
  app:
    environment:
      SKULID_DEV_AUTH_BYPASS: "1"
      SKULID_DEV_USER_EMAIL: "dev@local"
```

After enabling, hit `https://your.host/dev/login` (or the "Skip OAuth →" link on the login page) to land on the dashboard.

See [Development → Local setup](Development#local-setup) for the longer story on what this does and doesn't bypass.

## Postgres (compose)

The bundled `docker-compose.yml` reads:

| Variable            | Default        | Used by                            |
| ------------------- | -------------- | ---------------------------------- |
| `POSTGRES_USER`     | `skulid`              | Postgres init + `DATABASE_URL`     |
| `POSTGRES_PASSWORD` | `changeme`            | Postgres init + `DATABASE_URL`     |
| `POSTGRES_DB`       | `skulid`              | Postgres init + `DATABASE_URL`     |
| `HOST_PORT`         | `8567`                | Host port mapped to container 8567 |
| `SKULID_IMAGE`      | `ghcr.io/ryakel/skulid` | Image repo to pull (override for an internal registry) |
| `SKULID_TAG`        | `latest`              | Image tag (pin to `vX.Y.Z` for a specific release) |

## Notes

- `EXTERNAL_URL` is what gets sent to Google as the OAuth redirect URI
  base and the watch-channel webhook address. If you change it later,
  re-register webhooks via **Settings → Re-register all webhooks**
  ([why](Operations#re-registering-webhooks)).
- `ENCRYPTION_KEY` is the **only** thing that decrypts your stored
  refresh tokens. If you lose it, you must reconnect every account.
  Back it up offline.
- `SESSION_SECRET` rotation invalidates every active session — users
  get bounced to the login page, which is fine.
