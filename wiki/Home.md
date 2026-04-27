# calm-axolotl

Self-hosted, single-user Google Calendar sync — a Reclaim.ai alternative
you can run on a homelab box. One Go binary plus Postgres in Docker.

## What it does

- **Sync rules** mirror events between calendars (one-way or
  bidirectional) with optional filtering and transformation.
- **Smart blocks** automatically maintain "focus"/"busy" blocks on a
  target calendar based on busy time elsewhere, respecting per-block
  working hours in any IANA timezone.
- **AI assistant** (optional) lets you make calendar changes by chatting
  with Claude, with confirmation required before any write hits Google.

## Where to start

| If you want to…                          | Read                                |
| ---------------------------------------- | ----------------------------------- |
| Stand up a fresh instance                | [Getting Started](Getting-Started)  |
| Understand how the moving parts fit      | [Architecture](Architecture)        |
| Set up a sync rule                       | [Sync Rules](Sync-Rules)            |
| Set up an availability/focus block      | [Smart Blocks](Smart-Blocks)        |
| Configure the daemon                     | [Configuration](Configuration)      |
| Back up, renew watches, or troubleshoot  | [Operations](Operations)            |
| Understand the threat model              | [Security Model](Security-Model)    |
| Hack on the codebase                     | [Development](Development)          |
| Use the Claude-powered chat              | [AI Assistant](AI-Assistant)        |

## Status

Beta. The schema is at v1; breaking schema changes will get a migration.
Token sealing keys are not rotated automatically — keep your
`ENCRYPTION_KEY` somewhere safe.

## Project links

- Source: [github.com/ryakel/skulid](https://github.com/ryakel/skulid)
- Issues: [github.com/ryakel/skulid/issues](https://github.com/ryakel/skulid/issues)
