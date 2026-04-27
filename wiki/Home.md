# skulid

Self-hosted, single-user Google Calendar sync — a Reclaim.ai
alternative you can run on a homelab box. One Go binary plus
Postgres in Docker.

## What it does

- **Sync rules** mirror events between calendars (one-way or
  bidirectional) with optional filtering, transformation, and
  Reclaim-style visibility presets.
- **Smart blocks** auto-generate availability/focus blocks on a
  target calendar based on busy time elsewhere.
- **Tasks** are auto-placed into the next free Working-hours slot.
- **Habits** are recurring soft blocks that drift near an ideal time.
- **Categories** drive color-coding, planner totals, and per-rule pins.
- **Planner** + **Priorities** views to see the week at a glance.
- **AI assistant** (optional) lets you talk to Claude to manage your
  calendars; every write requires confirmation.

## Where to start

| If you want to…                          | Read                                |
| ---------------------------------------- | ----------------------------------- |
| Stand up a fresh instance                | [Getting Started](Getting-Started)  |
| Understand how the moving parts fit      | [Architecture](Architecture)        |
| See your week                             | [Planner](Planner)                  |
| Add a one-shot block (e.g. "draft Q3")   | [Tasks](Tasks)                      |
| Add a recurring soft block (e.g. Lunch)  | [Habits](Habits)                    |
| Triage by priority                        | [Priorities](Priorities)            |
| Mirror events between calendars          | [Sync Rules](Sync-Rules)            |
| Maintain availability / focus blocks     | [Smart Blocks](Smart-Blocks)        |
| Tune working hours                        | [Hours](Hours)                      |
| Add padding around scheduled blocks       | [Buffers](Buffers)                  |
| Customize event color-coding              | [Categories](Categories)            |
| Use the Claude-powered chat               | [AI Assistant](AI-Assistant)        |
| Configure environment variables          | [Configuration](Configuration)      |
| Back up, renew watches, or troubleshoot  | [Operations](Operations)            |
| Understand the threat model              | [Security Model](Security-Model)    |
| Hack on the codebase                     | [Development](Development)          |

## Status

Beta. Schema is at v7; breaking changes will get numbered migrations.
Token sealing keys are not rotated automatically — keep your
`ENCRYPTION_KEY` somewhere safe.

## Project links

- Source: [github.com/ryakel/skulid](https://github.com/ryakel/skulid)
- Issues: [github.com/ryakel/skulid/issues](https://github.com/ryakel/skulid/issues)
