# Habits

A **habit** is a recurring soft block — Lunch, Decompress, Standup
buffer — that the scheduler maintains on a target calendar near a
preferred time, drifting only as far as you allow.

## Anatomy

| Field             | Meaning                                                     |
| ----------------- | ----------------------------------------------------------- |
| Title             | Becomes the event summary.                                  |
| Target calendar   | Where occurrences are written.                              |
| Duration          | Minutes per occurrence.                                     |
| Ideal time        | `HH:MM` in the target account's hours timezone.             |
| Flex              | How many minutes the scheduler may drift from ideal.        |
| Days of week      | Subset of mon/tue/wed/thu/fri/sat/sun.                      |
| Hours window      | `working` / `personal` / `meeting` — which account hours apply. |
| Category          | Optional pin; otherwise the auto-categorizer decides.       |
| Horizon (days)    | How far into the future occurrences are maintained.         |
| Enabled           | Disabled habits aren't placed.                              |

## How placement works

The scheduler walks each day in `[today, today + horizon)`. For days
matching a `days_of_week` entry it pulls a per-day freebusy on the
target calendar, applies buffer padding, and calls
`hours.NearestFitSlot(duration, flex, ideal)`.

- If a slot fits within `±flex` of `ideal`, an occurrence is created
  (or moved if it already existed for that date).
- If no slot fits, any stale occurrence on that day is removed. The
  next recompute will retry.

Each occurrence is stored in `habit_occurrence` with a unique
`(habit_id, occurs_on)` constraint, so the scheduler is idempotent.

## Recompute triggers

- Saving a new or edited habit (future occurrences are wiped first
  so the scheduler rebuilds under the new rules).
- The **Recompute** button on the habits list.
- Daily worker tick (planned).

## Common patterns

### Lunch

- Title `Lunch`, duration 60m, ideal `12:00`, flex 90m, weekdays,
  hours window `personal`, horizon 14 days.

### Decompress after meetings *(future)*

The current Buffers v1 implements decompression as scheduler-internal
padding. A visible "Decompress" event after every non-managed meeting
is on the roadmap.

### Standup buffer

- Title `Standup`, duration 15m, ideal `09:30`, flex 30m, weekdays,
  hours window `working`. Even on busy days you'll keep the slot.

## Audit log

Every placement / reschedule / drop lands in the audit log with
`kind="habit"`.

## See also

- [Tasks](Tasks) — one-shot scheduled blocks
- [Hours](Hours) — Working/Personal/Meeting windows
- [Buffers](Buffers) — padding between scheduled blocks
- [AI Assistant](AI-Assistant) — `create_habit`, `update_habit`, `delete_habit`
