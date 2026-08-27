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
5. Calls `hours.ChunkedSlots` to plan where the duration goes: one
   contiguous block if there is room for one, otherwise several
   (see below).
6. Inserts (or updates, on reschedule) a Google event per block, with
   `extendedProperties.private.skulidManaged="1"` plus
   `skulidTaskId=<id>` so the rule engine doesn't loop on them.

If nothing can be placed, the task stays `pending` **and says why** —
the reason appears under its status on the tasks list, e.g. "Only 6h of
the 8h needed would fit" or "No free time before Fri May 1, 5:00 PM".

## Splitting a long task

A task longer than any single free gap is split across several blocks
rather than left unplaced. "8 hours of writing, due Friday" lands in
whatever gaps exist between now and Friday.

The rules:

- **One block wins whenever it can.** If the whole duration fits in a
  single free window, that is what you get — an ordinary task looks and
  behaves exactly as it always did, with no churn.
- **Pieces are at least the minimum task block**, set on
  **Settings → Buffers** and defaulting to 30 minutes. Gaps shorter than
  that are skipped rather than filled, so an eight-hour task doesn't
  shatter into a dozen fragments wedged between meetings. Raise it if
  you want fewer, longer sittings.
- **The last piece may be short.** A 15-minute tail beats leaving the
  whole task unplaced over it.
- **At most 8 blocks**, as a backstop against a pathological calendar.
- **Placement is all-or-nothing.** If the whole duration can't be
  covered, nothing is booked — booking 6 of 8 hours and calling the task
  scheduled would be a lie you only discover on Friday. The note says
  how close it got.
- Split blocks are titled `Write the report (1/3)`, `(2/3)`, `(3/3)`, so
  they read as one task rather than three duplicates. A single-block
  task keeps its plain title.

Each block is a row in `task_chunk`. The task's own
`scheduled_starts_at` / `scheduled_ends_at` summarise the **first**
block, which is what the tasks list, the planner and the AI assistant
show; the tasks list adds "(first of N blocks)" when there is more than
one.

Re-placement reconciles rather than rebuilds: blocks are moved in place
where the counts line up, extras are deleted and shortfalls inserted, so
the churn on your calendar is limited to the blocks that actually moved.
Deleting a task deletes **all** of its blocks.

## Manual scheduling

The **Schedule** button on a task row triggers an immediate placement.
**Done** marks the task `completed` and leaves the existing event in
place (it really happened — the calendar should still show it).
**Delete** removes the task and every block it holds.

## Audit log

Every placement / reschedule / drop lands in the audit log with
`kind="task"` and the task's scheduled window in the message field.

## Limitations

- **Blocks are re-planned wholesale, not pinned.** Every placement pass
  re-derives the whole set from current availability, so a block can
  move when the calendar around it changes. Reconciliation keeps that
  down to the blocks that genuinely have to move, but skulid does not
  promise a block stays put once you have seen it.
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
