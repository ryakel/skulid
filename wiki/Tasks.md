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
3. Pulls the target calendar's freebusy.
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
- **Target calendar's freebusy only.** Tasks don't cross-check
  conflicts on other calendars connected to the same account.
- **No drag-and-drop yet.** Move a task by editing its target
  calendar/due, or use the AI assistant.

## See also

- [Priorities](Priorities) — Kanban view of active tasks
- [Hours](Hours) — Working/Personal/Meeting windows
- [Buffers](Buffers) — padding around scheduled blocks
- [AI Assistant](AI-Assistant) — `create_task`, `update_task`,
  `complete_task`, `delete_task`
