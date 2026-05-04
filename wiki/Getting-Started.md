# Getting Started

This walks you from zero to a running skulid instance with one
Google account connected and one sync rule firing.

## Prerequisites

- A Linux box that can run Docker (`docker compose` v2).
- A Google account whose calendars you want to manage.
- A **public HTTPS URL** that points at the box — Google requires HTTPS
  for OAuth redirects and push notifications. Easy options:
  - [Cloudflare Tunnel](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/)
  - [Tailscale Funnel](https://tailscale.com/kb/1223/funnel)
  - A reverse proxy (Caddy/nginx) with a Let's Encrypt cert.
- About 15 minutes.

## 1. Create a Google OAuth client

1. Go to [Google Cloud Console → APIs & Services → Credentials](https://console.cloud.google.com/apis/credentials).
2. Create or pick a project.
3. **OAuth consent screen**: choose **External**, add yourself as a test
   user. (You don't need to publish the app — skulid is for one
   person.)
4. **Create credentials → OAuth client ID → Web application**.
5. Authorized redirect URIs: add `https://YOUR.PUBLIC.HOST/auth/google/callback`.
6. Copy the **client ID** and **client secret**.
7. Under **APIs & Services → Library**, enable the **Google Calendar API**
   for the project.

## 2. Clone and configure

```bash
git clone https://github.com/ryakel/skulid.git
cd skulid
cp .env.example .env
```

Generate the two secrets:

```bash
openssl rand -base64 48   # paste as SESSION_SECRET
openssl rand -base64 32   # paste as ENCRYPTION_KEY
```

Edit `.env`:

```ini
EXTERNAL_URL=https://skulid.example.com
GOOGLE_CLIENT_ID=...
GOOGLE_CLIENT_SECRET=...
SESSION_SECRET=...
ENCRYPTION_KEY=...
```

If you also want the AI assistant, add:

```ini
ANTHROPIC_API_KEY=sk-ant-...
ANTHROPIC_MODEL=claude-opus-4-7
```

See [Configuration](Configuration) for every supported variable.

## 3. Boot it

```bash
docker compose up -d
docker compose logs -f app
```

This pulls the latest published image from
[`ghcr.io/ryakel/skulid`](https://github.com/ryakel/skulid/pkgs/container/skulid).
Pass `--build` to compile from source instead, or pin a release by
adding `SKULID_TAG=v1.2.3` to your `.env`.

Wait for `migrations applied` and `http server listening`. Visit
`EXTERNAL_URL` in your browser.

## 4. Claim the instance

The first Google account to sign in becomes the **permanent owner** of
this instance — this is [Trust On First Use](Security-Model#tofu).
Anyone else who tries to log in afterward gets a 403.

Click **Sign in with Google**. After consent you land on the dashboard.

## 5. Connect any additional accounts

If you have multiple Google accounts (work + personal, etc.), go to
**Accounts → + Connect Google account** and run the OAuth flow again
for each one. They all funnel into the same instance.

When an account is connected, skulid auto-discovers every calendar
visible to it and registers a Google push channel (a webhook
subscription) so changes flow back in near-real time. You can re-trigger
discovery anytime with **Refresh calendars**.

## 6. Create your first sync rule

**Rules → + New rule**. Pick a source calendar, a target calendar, give
it a name, and save. By default the rule mirrors every event one-way
and forwards new changes only — see [Sync Rules](Sync-Rules) for filters,
transforms, bidirectional mode, and backfill.

Hit **Sync now** to immediately pull from the source. Or just create or
edit an event on the source calendar in Google Calendar; within a few
seconds the mirror should appear on the target.

## 7. (Optional) Create a smart block

**Smart blocks → + New smart block**. Pick a target calendar (where the
focus/busy blocks live) and one or more source calendars (busy time read
from these). Set working hours per weekday in your IANA timezone.
Save → skulid writes blocks for the next 30 days and keeps them
fresh as the source calendars change.

See [Smart Blocks](Smart-Blocks) for the full options.

## What's next?

- [Operations](Operations) — backups, watch renewal, troubleshooting.
- [AI Assistant](AI-Assistant) — set up the Claude-powered chat.
- [Security Model](Security-Model) — what skulid protects against.
