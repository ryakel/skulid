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
3. Configure the **OAuth consent screen** — see
   [Publishing status: the one setting that matters](#publishing-status-the-one-setting-that-matters)
   directly below. Getting this wrong breaks skulid one week later,
   so read it before you click.
4. **Create credentials → OAuth client ID → Web application**.
5. Authorized redirect URIs: add `https://YOUR.PUBLIC.HOST/auth/google/callback`.
6. Copy the **client ID** and **client secret**.
7. Under **APIs & Services → Library**, enable the **Google Calendar API**
   for the project.

### Publishing status: the one setting that matters

skulid holds one long-lived refresh token per connected account and runs
unattended. Google's rules for how long that token lives depend entirely
on your app's **publishing status** — not on whether the app is
"verified".

> **Never leave the app in Testing.** An **External** app in **Testing**
> status has *every refresh token revoked after 7 days*. skulid will run
> beautifully for a week and then stop syncing. This is the single most
> common way to break a self-hosted install.

The **"Google hasn't verified this app"** screen is a separate thing, and
it is not a symptom of any of this. It appears whenever an External app is
unverified — in Testing and In production alike — so seeing it tells you
nothing about your publishing status. Don't diagnose from it; open the
consent screen and read the status directly.

Pick whichever of these two matches your accounts:

| | **Internal** | **External + Production** |
| --- | --- | --- |
| Available to | Google Workspace orgs only | anyone |
| Accounts you can connect | only accounts in your Workspace domain | any Google account, consumer `@gmail.com` included |
| Refresh token lifetime | indefinite | indefinite |
| Consent warning screen | none | one-time "Google hasn't verified this app" per account |
| Verification needed | no | no |

**If every calendar you want to sync lives in one Google Workspace
domain**, choose **Internal**. Nothing else to do — no warning screen, no
user cap, no verification.

**If you need to connect even one consumer `@gmail.com` account**,
Internal will refuse it. Choose **External**, then click **Publish app**
on the consent screen so the status reads *In production*. Leave it
unverified.

You do **not** need to submit the app for Google verification. skulid
asks only for the Calendar scope, which Google classifies as *sensitive*,
not *restricted* — so no security audit or CASA assessment applies. The
consequences of staying unverified are exactly two, and neither matters
for a single-user install:

- Each account sees a **"Google hasn't verified this app"** interstitial
  the first time it connects. Click **Advanced → Go to skulid (unsafe)**.
  It appears once per account, not once per sync. The wording is aimed at
  people being phished by a stranger's app; this is your OAuth client,
  your code, on your own box.
- The project is capped at **100 authorized users for its lifetime**. You
  will use one or two.

Submitting for verification would remove the interstitial, but it means
publishing a privacy policy and homepage on a domain you own and waiting
on a review — all to remove one click on a private tool only you use.

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
Pin a release by adding `SKULID_TAG=v1.2.3` to your `.env`. To deploy
from an internal registry, set `SKULID_IMAGE=registry.home.lan/ryakel/skulid`.

For local development with uncommitted changes, build the image
directly and run with `SKULID_TAG=dev`:

```bash
docker build -t ghcr.io/ryakel/skulid:dev .
SKULID_TAG=dev docker compose up -d
```

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

When an account is connected, skulid discovers every calendar visible to
it and lists them on **Accounts** — but they arrive **disabled**, and a
disabled calendar registers no push channel and syncs nothing. Enable the
ones you actually want with the **Enable** button on each row. Enabling
registers the Google push channel (a webhook subscription) at that point,
so changes flow back in near-real time.

That default is deliberate: connecting an account should never start
watching calendars you didn't choose. It matters most for an account you
don't own outright — see
[Connecting an account you don't own](#connecting-an-account-you-dont-own)
below. Re-run discovery anytime with **Refresh calendars**; it never
disturbs what you have already enabled.

### Connecting an account you don't own

Connecting an employer's Google Workspace account is a different
proposition from connecting your own, in two ways that have nothing to do
with this code:

- **Their admin may simply block it.** Workspace has *API controls → App
  access control*, and restricting unverified third-party apps that
  request sensitive scopes is a common configuration. skulid is exactly
  that, so the connect may fail with an admin-blocked error. That is
  their policy working as intended, not a bug here.
- **Their data policy governs, not yours.** Routing corporate calendar
  data through a host you operate personally is the sort of thing an
  acceptable-use policy tends to have an opinion about. Find out before
  you connect, not after.

If you do connect one, two settings are worth setting deliberately:

1. Leave its calendars disabled except the ones you genuinely need.
2. If you run the assistant, use **Hide from assistant** on that account
   — see [AI Assistant § Excluding an account](AI-Assistant#excluding-an-account).

skulid never stores event content: `event_link` holds Google event IDs
and etags, never titles, descriptions or attendees. Events are fetched,
transformed in memory, and written to the target. The assistant is the
one exception, and the one you can switch off per account.

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
