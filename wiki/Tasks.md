# Tasks

A **task** is a chunk of work skulid auto-places onto a target calendar
in the next available Working-hours slot.

## Anatomy

| Field            | Meaning                                                         |
| ---------------- | --------------------------------------------------------------- |
| Title            | Free-form. Becomes the event summary.                           |
| Notes            | Goes into the event description.                                |
| Target calendar  | Where the scheduler writes the block.                           |
| Priority         | `critical` / `high` / `medium` / `low`. Drives Priorities Kanban.|
| Duration         | Minutes the scheduler reserves.                                 |
| Due              | Optional deadline. The scheduler won't place after due.         |
| Category         | Optional pin; when blank the auto-categorizer decides.         |
| Status           | `pending` (not placed) / `scheduled` / `completed` / `cancelled` |

## How placement works

When you save a task, the scheduler asynchronously:

1. Loads the target account's **effective Working hours**.
2. Expands those hours over `[now, due_at or now+14d)`.
3. Pulls freebusy for **every enabled calendar on every connected
   account** — not just the target. It is one person's time regardless of
   which account owns the calendar, so a personal task will not be placed
   on top of a work meeting. skulid's own writes — smart blocks,
   decompression buffers, other scheduled tasks and habits — are then
   subtracted, so a Focus block never stops real work being scheduled.
   That costs no extra Google calls: skulid recorded each of those
   windows when it wrote them.
4. Applies any configured [buffer padding](Buffers).
5. Calls `hours.FirstFitSlot` to find the earliest free window of the
   right duration.
6. Inserts (or updates, on reschedule) a Google event with
   `extendedProperties.private.skulidManaged="1"` plus
   `skulidTaskId=<id>` so the rule engine doesn't loop on it.

If no fit exists, the task stays `pending` — bump the due date or
clear an existing block out of the way.

## Manual scheduling

The **Schedule** button on a task row triggers an immediate placement.
**Done** marks the task `completed` and leaves the existing event in
place (it really happened — the calendar should still show it).
**Delete** removes the task and its scheduled event.

## Audit log

Every placement / reschedule / drop lands in the audit log with
`kind="task"` and the task's scheduled window in the message field.

## Limitations

- **Single-block placement.** Tasks aren't split into chunks — if
  duration > the largest free window, the task stays pending.
- **Placement fails closed.** If any connected account's freebusy can't
  be fetched — a revoked token, an API error — the task isn't placed at
  all rather than placed against a busy set known to be incomplete.
  Scheduling over a real meeting because a token expired is worse than
  not scheduling. A locked-out account shows a banner on every page, so
  the cause is visible; reconnect it and placement resumes on the next
  tick.
- **A real meeting created on top of a skulid block isn't seen.** Google
  merges overlapping busy periods before returning them, and skulid
  removes its own blocks from that set by matching the windows it
  recorded. So if a meeting was created over a Focus block *after* the
  block was placed, that overlap stops reading as busy. Narrow, and the
  smart-block recompute usually moves the block out of the way first.
- **No drag-and-drop yet.** Move a task by editing its target
  calendar/due, or use the AI assistant.

## See also

- [Priorities](Priorities) — Kanban view of active tasks
- [Hours](Hours) — Working/Personal/Meeting windows
- [Buffers](Buffers) — padding around scheduled blocks
- [AI Assistant](AI-Assistant) — `create_task`, `update_task`,
  `complete_task`, `delete_task`
