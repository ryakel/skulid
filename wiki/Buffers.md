# Buffers

Two ways skulid keeps your calendar from feeling overstuffed:

1. **Padding** the scheduler keeps around busy time when placing tasks
   and habits, so back-to-back blocks have breathing room.
2. **Visible "Decompress" events** the buffer engine writes after every
   non-managed meeting, so the gap is real on Google's side too.

## Configure

**Settings → Buffers** sets the global defaults. Each connected
calendar can override on its own **Calendar Settings** page (linked
from the account list).

Three fields:

| Field                         | Effect                                                                |
| ----------------------------- | --------------------------------------------------------------------- |
| Task & Habit breaks (minutes) | Scheduler-internal padding (rolled up with decompression — see below) |
| Decompression after meetings  | Visible event of this length after each meeting + scheduler padding   |
| Travel time before & after    | **Stored, not yet enforced** (location-aware future)                  |

The **per-calendar override chain** is `calendar → global → 0`. Empty
value or unchecked override = use the global setting.

## Scheduler padding

When the scheduler places a task or habit, it pulls the target
calendar's freebusy and extends each busy window's end by
`max(task_habit_break, decompression)` before searching for a slot.
The two values can't yet be distinguished by source (freebusy doesn't
say which event each busy window came from), so the larger wins.

## Visible decompress events

The buffer engine runs after every calendar sync (debounced 15s) and
keeps `decompression_event` rows in step with the user's upcoming
meetings:

1. List Google events for the next 7 days.
2. For each non-managed event with **≥2 non-resource attendees**,
   ensure a "Decompress" event of length `decompression_minutes`
   exists right after, on the same calendar.
3. If the meeting moves or the decompression-minutes value changes,
   the existing buffer event is updated in place.
4. If a meeting is cancelled or no longer qualifies, its trailing
   buffer is reaped.

Every operation lands in the audit log under `kind="buffer"`.

The buffer event itself carries:

- `extendedProperties.private.skulidManaged = "1"`
- `skulidBufferType = "decompression"`
- `skulidBufferForEventId = <source event id>`

so it bounces off the rule-engine loop guard and other skulid
subsystems.

### Trigger paths

- **Per-calendar sync**: every successful incremental sync queues a
  debounced decompression recompute for that calendar.
- **Manual button**: **Settings → Buffers → "Recompute decompress
  events now"** runs the engine across every calendar synchronously.
- **Saving the global buffers**: same thing — fires after the value
  is persisted so the new minutes immediately take effect.

## What's not in v1

- **Travel time enforcement.** The field saves; the engine ignores it.
  Comes when we have location-aware policy.
- **Per-source padding distinction.** Task-break vs decompression
  can't be teased apart from freebusy yet — the scheduler uses the
  larger value.
- **Buffer events on managed mirrors.** A sync rule that mirrors a
  meeting onto another calendar does *not* drag the decompress event
  along (mirror is `skulidManaged=1`, so it's filtered out). Worth
  reconsidering if you actually want decompression on the mirror's
  side too.
