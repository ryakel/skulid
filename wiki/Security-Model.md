# Security Model

What skulid protects against, and what it doesn't.

## Threat model

skulid is built for a single user running it on their own
infrastructure (homelab, VPS, behind a tunnel). It is not multi-tenant
and the auth model assumes the operator is the only legitimate user.

| Threat                                              | Mitigation                                              |
| --------------------------------------------------- | ------------------------------------------------------- |
| Someone else discovers your URL and tries to log in | TOFU owner claim; non-owner Google logins get 403      |
| DB dump leaks via backup theft                      | OAuth tokens are AES-256-GCM sealed; key is in env     |
| Google push spoofing                                | Per-channel HMAC token verified on every webhook        |
| Session cookie tampering                            | HMAC-SHA256-signed cookies                              |
| Replay of an old session                            | 30-day expiry built into the cookie payload             |
| Cross-site request forgery                          | Mutating routes require POST; SameSite=Lax cookies      |
| Cross-site scripting                                | All template output goes through `html/template`        |
| Loop attack (mirror writes triggering more mirrors) | `extendedProperties.private` loop guard                 |
| Malicious AI tool calls                             | Confirmation required for every destructive write       |

## TOFU

**Trust On First Use.** The first Google account to complete the
OAuth login claims this instance permanently. Their
`google_sub` (the stable user ID Google returns) is recorded in the
`setting` table.

- Subsequent logins must match that `google_sub` or get rejected.
- The `Connect Google account` flow is for adding *additional* Google
  accounts under the same owner — it doesn't change ownership.
- To reset ownership, you have to manually delete the setting row.

## Token storage

Refresh and access tokens go through `crypto.Sealer`:

- AES-256-GCM with a fresh 12-byte nonce per ciphertext.
- The nonce is prepended to the ciphertext, the whole thing is base64
  encoded.
- The 32-byte key comes from `ENCRYPTION_KEY` (base64-decoded).

If the DB is dumped without the key, the tokens are unrecoverable.
If the key is exposed, every stored token can be decrypted — treat it
like the master secret it is.

## Sessions

Cookies are `payload.signature` where:

- `payload = base64(googleSub|email|issuedAtUnix)`
- `signature = base64(HMAC-SHA256(SESSION_SECRET, payload))`

The server verifies the signature on every request and refuses any
session older than 30 days. There is no server-side session store —
sessions are purely cookie-based, which means rotating
`SESSION_SECRET` invalidates everything.

Cookies are `HttpOnly`, `SameSite=Lax`, and `Secure` whenever
`EXTERNAL_URL` is HTTPS.

## Webhook authentication

Google sends push notifications to `/api/webhooks/google`. The handler
checks:

1. `X-Goog-Channel-Id` matches a row in `sync_token`.
2. `X-Goog-Channel-Token` matches the per-channel HMAC token we
   generated when registering — verified with `subtle.ConstantTimeCompare`.
3. `X-Goog-Resource-Id` matches what we stored (warned-but-accepted on
   mismatch — Google sometimes rotates these).

Unknown channel IDs return `200 OK` so Google stops retrying. (Returning
4xx would only flood our logs.)

## OAuth

- We request **offline access** with `prompt=consent` so we always get
  a refresh token. Without one we can't run unattended.
- Scopes: `openid email profile` plus the **full Google Calendar** scope.
  We can't do read-only because the app needs to write mirrors and
  smart blocks.
- The OAuth state cookie + intent cookie are short-lived (10 min) and
  validated on the callback.

## AI assistant

When `ANTHROPIC_API_KEY` is set, the AI assistant has access to the
same calendars as you. It can call read tools without confirmation
(list calendars, list events, find free time) and propose write tools
(create, update, delete, move event). **Every write requires you to
click "Apply" before it actually hits Google.**

Conversations are stored in Postgres for 30 days, then deleted. You
can also delete a conversation manually at any time.

If you don't trust the AI assistant feature, just don't set
`ANTHROPIC_API_KEY` — the routes are unregistered and the nav link
hidden when it's absent.

## What's *not* protected

- **Local network attacker who can read process memory.** Tokens are
  decrypted into memory whenever a sync runs.
- **Compromised host.** If someone has root on the box, the
  `ENCRYPTION_KEY` is in env; the DB is too.
- **Malicious Google itself.** We trust Google's TLS certs and the
  Google Calendar API responses.
- **Side-channel timing.** We don't constant-time-compare every
  user-controllable string — only the webhook channel token.
- **DoS.** No rate limiting on `/api/webhooks/google` (relies on
  Google's own throttling) or on the OAuth callback.
