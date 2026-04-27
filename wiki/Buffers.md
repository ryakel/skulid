# Buffers

Padding the scheduler keeps around busy time when placing tasks and
habits, so you never end up with one block butting straight up against
another.

## Configure at **Settings → Buffers**

Three fields:

| Field                         | Effect                                                  |
| ----------------------------- | ------------------------------------------------------- |
| Task & Habit breaks (minutes) | Scheduler-internal padding (v1 details below)           |
| Decompression after meetings  | Scheduler-internal padding (v1 details below)           |
| Travel time before & after    | Stored, **not yet enforced** (location-aware future)    |

## v1 behavior

When the scheduler pulls busy windows from a calendar, it extends
each window's end by `max(task_habit_break, decompression)` before
running first-fit / nearest-fit. The two values aren't yet
distinguished by source because freebusy responses don't carry enough
metadata to tell our managed events apart from real meetings cheaply.

So in v1 the **larger** of the two values is the universal padding
the scheduler applies. The travel-time field is reserved.

## What's coming later

- Per-source padding (managed-vs-real distinction) — likely via
  `Events.list` rather than freebusy.
- Visible "Decompress" / travel events written to the calendar.
- Travel time gated on event location strings.

The `setting` row stores all three values today, so when these land
the data is already in place.
