# Operations

Day-2 stuff: backups, watch renewal, log inspection, troubleshooting.

## Backups

Everything important is in Postgres. The token ciphertexts in the
`account` table are useless without your `ENCRYPTION_KEY`, so keep both:

1. **Database**: snapshot the `db_data` Docker volume, or `pg_dump`:
   ```bash
   docker compose exec db pg_dump -U skulid skulid \
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
| Kind    | `rule`, `smart_block`, `task`, `habit`, `buffer`, or `ai` |
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

### "Sync is stopped" banner / an account needs reconnecting

skulid shows a red banner on every page, and a **needs reconnect** pill on
**Accounts**, when Google has permanently refused to renew an account's
access. The Accounts row spells out the reason. Click **Reconnect** on that
row and complete the consent screen; rules, smart blocks and per-calendar
settings all survive, because the account is matched on its Google subject
ID and only the stored tokens are replaced.

Google revokes a refresh token when:

- **The OAuth app is still in Testing publishing status.** Tokens die
  after 7 days, every time. This is the usual cause, and reconnecting only
  buys another week — fix the publishing status instead, per
  [Getting Started § Publishing status](Getting-Started#publishing-status-the-one-setting-that-matters).
- Consent was withdrawn at [myaccount.google.com/permissions](https://myaccount.google.com/permissions).
- The account's password changed.
- The token went unused for six months.
- `GOOGLE_CLIENT_ID` / `GOOGLE_CLIENT_SECRET` changed, or the OAuth client
  was deleted in Google Cloud. Here every account breaks at once, and no
  amount of reconnecting helps until `.env` matches the real client.

Transient failures — a network blip, a Google 5xx, a rate limit — do *not*
raise the banner. They are retried on the next tick.

To check the state directly:

```sql
SELECT email, needs_reauth, reauth_reason, reauth_detected_at FROM account;
```

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
Check that mirrored events have `extendedProperties.private.skulidManaged="1"`
in the Google UI — if not, your sealed token might be from a different
deployment that wrote without the loop key.

## Upgrades

```bash
docker compose up -d
```

That's the whole thing. The `app` service is `pull_policy: always`, so
`up` re-pulls `ghcr.io/ryakel/skulid:latest` every time — you do not need
`docker compose pull` first.

Two things that are **not** part of upgrading:

- **`--build` does nothing.** `docker-compose.yml` pulls a published image
  and has no `build:` stanza, so there is no build context for the flag to
  act on. Nothing here compiles on your machine.
- **`git pull` doesn't fetch the new binary.** That comes from the
  registry. Pull only when you want an updated `docker-compose.yml` or
  `.env.example`.

Pin a specific release with `SKULID_TAG=v0.1.0` in `.env` if you'd rather
not track `:latest`.

Migrations run on startup. Breaking changes get a numbered migration file
under `migrations/`; to see which schema version an instance is actually
on, ask the database rather than the docs:

```bash
docker compose exec db psql -U skulid -d skulid \
  -c "select max(version_id) from goose_db_version;"
```

## Resetting the instance

To wipe everything and start over:

```bash
docker compose down -v   # -v removes the db_data volume
```

Reconnect Google accounts after restart.
