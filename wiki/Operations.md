# Operations

Day-2 stuff: backups, watch renewal, log inspection, troubleshooting.

## Backups

Everything important is in Postgres. The token ciphertexts in the
`account` table are useless without your `ENCRYPTION_KEY`, so keep both:

1. **Database**: snapshot the `db_data` Docker volume, or `pg_dump`:
   ```bash
   docker compose exec db pg_dump -U calmaxolotl calmaxolotl \
     > backup-$(date +%Y%m%d).sql
   ```
2. **Encryption key**: keep `ENCRYPTION_KEY` somewhere offline. If you
   lose it, the database is recoverable but every Google account must
   be reconnected.
3. **Session secret**: optional to back up — losing it just bounces
   active logins to the login page.

## Re-registering webhooks

Google push channels expire after 7 days. The scheduler renews them
automatically when there's <24h left, but two situations require manual
re-registration:

- **Your `EXTERNAL_URL` changed.** The old webhook URL is dead and
  Google will give up retrying.
- **You restored from a backup older than 7 days.** Channel state in
  the DB is stale.

Fix: **Settings → Re-register all webhooks**. This stops every existing
channel and creates a new one for every calendar.

## Audit log

**Audit** in the nav shows the last 200 actions. Each entry has:

| Column  | Meaning                                                   |
| ------- | --------------------------------------------------------- |
| When    | UTC timestamp                                             |
| Kind    | `rule`, `smart_block`, or `ai`                            |
| Rule    | Sync rule ID (if `kind=rule`)                             |
| Block   | Smart block ID (if `kind=smart_block`)                    |
| Action  | `create`, `update`, `delete`, `error`, etc.               |
| Source  | Source Google event ID                                    |
| Target  | Target Google event ID                                    |
| Message | Free text (error details, backfill summary, etc.)         |

For deeper inspection, the table is `audit_log` in Postgres.

## Logs

The daemon writes structured JSON logs to stdout:

```bash
docker compose logs -f app
```

Filter by interesting things with `jq`:

```bash
docker compose logs -f app \
  | jq 'select(.level == "ERROR")'

docker compose logs -f app \
  | jq 'select(.msg == "http") | "\(.method) \(.path) \(.status) \(.dur)"' -r
```

## Troubleshooting

### Events aren't syncing

1. **Audit log** — is the rule actually firing? Look for matching
   `kind=rule` entries.
2. **Push channel** — is one registered? `SELECT calendar_id, watch_channel_id, watch_expires_at FROM sync_token;`
   Empty channel ID means watches aren't set up. Hit
   **Settings → Re-register all webhooks**.
3. **Sync token** — invalidate it manually to force a full resync:
   ```sql
   UPDATE sync_token SET sync_token = '' WHERE calendar_id = ?;
   ```
   Then click **Sync now** on a rule that uses that calendar.

### Smart block isn't writing

1. Is it **enabled**?
2. Are the working hours sensible? An empty per-weekday list = no
   working windows that day.
3. Time zone valid? Check `docker compose exec app /bin/sh` doesn't
   exist on distroless — instead, look for
   `smart block recompute failed` in logs.
4. Manual **Recompute** to force a pass.

### "owner mismatch" on login

You're trying to log in as a Google account that doesn't match the
owner recorded by [TOFU](Security-Model#tofu). Either:

- Sign in with the original owner account, or
- Reset ownership: `DELETE FROM setting WHERE key IN ('owner_email', 'owner_google_sub');`
  Then log in fresh.

### "no_refresh" error after login

Google didn't return a refresh token. This usually means the OAuth
client wasn't created with `prompt=consent` and `access_type=offline`,
or the user previously authorized the app and Google decided not to
re-issue. Revoke access at
[myaccount.google.com/permissions](https://myaccount.google.com/permissions)
and try again.

### Watch channel keeps re-firing the same change

Either two channels are registered for the same calendar (rare; happens
if a `Stop` call previously failed), or the loop guard is misfiring.
Check that mirrored events have `extendedProperties.private.calmAxolotlManaged="1"`
in the Google UI — if not, your sealed token might be from a different
deployment that wrote without the loop key.

## Upgrades

`git pull && docker compose up -d --build`. Migrations run on startup.
The schema is at v1 — breaking changes will get a numbered migration
file under `migrations/`.

## Resetting the instance

To wipe everything and start over:

```bash
docker compose down -v   # -v removes the db_data volume
```

Reconnect Google accounts after restart.
