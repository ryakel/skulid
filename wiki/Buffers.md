# Buffers

Two ways skulid keeps your calendar from feeling overstuffed:

1. **Padding** the scheduler keeps around busy time when placing tasks
   and habits, so back-to-back blocks have breathing room.
2. **Visible "Decompress" and "Travel" events** the buffer engine
   writes around your non-managed meetings, so the gap is real on
   Google's side too.

## Configure

**Settings → Buffers** sets the global defaults. Each connected
calendar can override on its own **Calendar Settings** page (linked
from the account list).

Three fields:

| Field                         | Effect                                                                |
| ----------------------------- | --------------------------------------------------------------------- |
| Task & Habit breaks (minutes) | Scheduler-internal padding (rolled up with decompression — see below) |
| Decompression after meetings  | Visible event of this length after each meeting + scheduler padding   |
| Travel time before & after    | Visible event of this length either side of each **located** meeting  |

The **per-calendar override chain** is `calendar → global → 0`. Empty
value or unchecked override = use the global setting.

## Scheduler padding

When the scheduler places a task or habit, it pulls freebusy for every
enabled calendar on every account and extends each busy window's end by
`max(task_habit_break, decompression)` before searching for a slot.
The two values can't yet be distinguished by source (freebusy doesn't
say which event each busy window came from), so the larger wins.

Travel is deliberately *not* in that rollup. Freebusy carries no
location, so the scheduler has no way to tell which busy windows
deserve travel padding — and padding every window by the travel
minutes would be wrong for the video calls that make up most of them.
Instead, the visible travel event does the work: it is a real busy
event on your calendar, so it shows up in freebusy on its own.

That is also why travel blocks, unlike skulid's other writes, are
*not* subtracted from the busy set when placing work. A decompression
block is subtracted because its protection is already applied twice
over by the padding above; counting the visible block too would double
it. A travel block has no such padding behind it, so the block itself
has to keep reading as busy.

## Visible buffer events

The buffer engine runs after every calendar sync (debounced 15s) and
keeps `buffer_event` rows in step with your upcoming meetings:

1. List Google events for the next 7 days.
2. Work out which buffers each event earns (below), and create, move
   or reap events on the same calendar so reality matches.

A row is keyed by `(calendar, source event, buffer type, placement)`,
so one meeting can own travel-before, decompress, and travel-back at
once without them colliding.

### Which meetings earn what

Every buffer requires the same floor: a live, timed, busy event skulid
did not write itself. All-day events, cancelled events, events marked
"free", and anything carrying `skulidManaged=1` are skipped — padding
our own buffer would compound a little more on every recompute.

On top of that:

- **Decompress** needs **≥2 non-resource attendees**, so solo blocks
  and personal reminders don't trigger one. It sits immediately after
  the meeting ends.
- **Travel** needs a non-empty `location` that isn't a video-call
  link. Meet, Zoom, Teams, Webex, GoToMeeting, Whereby, Chime,
  BlueJeans, Discord and Slack links are all recognised and skipped.
  Attendees are irrelevant — you have to get there whether or not
  anyone else is on the invite.

When a meeting earns all three, the order is **travel → meeting →
decompress → travel**: you decompress at the meeting, then leave. So
the trailing travel block starts where decompression ends, not where
the meeting ends.

Travel time is a flat pad, not a real estimate. Google gives a
free-text location and nothing else — no origin, no distance, no mode
of transport — so anything smarter would mean a maps API and a home
address. If your commute to the office and your walk to the coffee
place differ enough to matter, set the global value for the common
case and override it per calendar.

Every operation lands in the audit log under `kind="buffer"`, with the
buffer type as the message.

The buffer event itself carries:

- `extendedProperties.private.skulidManaged = "1"`
- `skulidBufferType = "decompression"` or `"travel"`
- `skulidBufferForEventId = <source event id>`

so it bounces off the rule-engine loop guard and other skulid
subsystems.

### Trigger paths

- **Per-calendar sync**: every successful incremental sync queues a
  debounced buffer recompute for that calendar.
- **Manual button**: **Settings → Buffers → "Recompute buffer events
  now"** runs the engine across every calendar synchronously.
- **Saving the global buffers**: same thing — fires after the value
  is persisted so the new minutes immediately take effect.

## What's not in v1

- **Real travel estimates.** See above: the pad is flat, and the same
  either side.
- **Per-source padding distinction.** Task-break vs decompression
  can't be teased apart from freebusy yet — the scheduler uses the
  larger value.
- **Buffer events on managed mirrors.** A sync rule that mirrors a
  meeting onto another calendar does *not* drag its buffers along
  (mirror is `skulidManaged=1`, so it's filtered out). Worth
  reconsidering if you actually want buffers on the mirror's side too.
