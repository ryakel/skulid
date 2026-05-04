# Smart Blocks

A **smart block** automatically maintains placeholder events on a target
calendar that represent your *available* (or *busy-from-another-source*)
time, refreshed whenever the source calendars change.

The classic use case: you want your work calendar to show "Focus" blocks
during every empty slot in your working hours, so coworkers can see when
you're protecting deep-work time.

## Anatomy of a smart block

| Field             | Meaning                                                            |
| ----------------- | ------------------------------------------------------------------ |
| Name              | Free-form label                                                    |
| Target calendar   | Where the focus blocks are written                                 |
| Source calendars  | Busy time read from these (multi-select)                           |
| Time zone         | IANA timezone name (`America/Chicago`, `Europe/Berlin`, etc.)     |
| Per-weekday hours | List of `HH:MM-HH:MM` ranges per weekday                          |
| Horizon (days)    | How far into the future to maintain blocks                        |
| Min block (min)   | Drop windows shorter than this                                    |
| Merge gap (min)   | Merge windows separated by ≤ this many minutes                    |
| Block title       | The summary on the generated event (e.g. `Focus`)                 |
| Enabled           | Disabled blocks aren't recomputed                                 |

## How recompute works

Every time a source calendar changes (or every 5 min from the polling
fallback), the smart-block engine debounces by 15s and then runs:

1. Build the list of working windows from your per-weekday hours,
   bounded to `[now, now + horizon]`.
2. Pull busy windows from each source calendar via Google's
   `Freebusy.query` (DST-correct, in your specified timezone).
3. Subtract busy windows from working windows → free windows.
4. Merge any free windows separated by ≤ merge-gap minutes.
5. Drop free windows shorter than min-block minutes.
6. Diff the result against the existing managed blocks for this
   smart_block:
   - Overlap with an existing block → update its window in-place
     (preserves the Google event ID).
   - No match → insert a new block.
   - Existing block with no match → delete it.
7. Every write carries `extendedProperties.private.skulidManaged=1`
   so it doesn't trigger sync rules and is recognizable as ours.

## DST and timezones

The engine uses Go's `time.LoadLocation` for the IANA name you provide.
That means working hours like `09:00-17:00` always mean **9am to 5pm
local clock time**, even across daylight-saving transitions. The
duration of a window will differ by an hour on the spring-forward and
fall-back days.

## Common patterns

### "Mark my work calendar as Focus during empty slots"

- Target: your work calendar
- Sources: same work calendar (so personal events on it count as busy),
  plus your personal calendar (so personal events block focus too)
- Time zone: yours
- Hours: `09:00-12:00,13:00-17:00` Mon-Fri
- Horizon: 30 days
- Min block: 30 minutes
- Merge gap: 15 minutes
- Title: `Focus`

### "Show my partner my available evenings"

- Target: shared calendar
- Sources: your work + personal calendars
- Hours: `18:00-22:00` every day
- Min block: 60 minutes
- Title: `Free`

### "Block out lunch every weekday"

This isn't really a smart-block use case — for a recurring static event,
just create it normally in Google Calendar.

## Manual recompute

Hit **Recompute** on the row to force a full pass right now. Useful if
you just changed the working hours or want to verify a change.
**Settings → Re-register all webhooks** is for a different problem (see
[Operations](Operations#re-registering-webhooks)).

## Audit trail

Every block create/update/delete lands in the audit log with
`kind="smart_block"`, the smart_block ID, and the action.

## Limitations

- The engine writes one event per free window; there is no "batched"
  block representation. Very fragmented calendars produce many tiny
  events (which is why **min block** exists).
- Working hours are weekday-keyed. Holidays aren't auto-detected — if
  you want a holiday to be treated as a non-working day, just decline
  the all-day holiday event from your sources.
- The engine doesn't yet support negative-availability (e.g. "only show
  blocks during certain hours"). It's working hours minus busy.
