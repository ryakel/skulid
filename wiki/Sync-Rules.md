# Sync Rules

A **sync rule** mirrors events from a source calendar onto a target
calendar, optionally with filtering and transformation applied.

## Anatomy of a rule

| Field                | Meaning                                                       |
| -------------------- | ------------------------------------------------------------- |
| Name                 | Free-form label                                               |
| Source calendar      | Where events are read from                                    |
| Target calendar      | Where mirrored events are written                             |
| Direction            | `one_way` or `bidirectional`                                  |
| Primary side         | Tie-breaker for bidirectional conflicts (`source` or `target`) |
| Filter               | JSON predicate — only matching events are mirrored            |
| Transform            | JSON spec for what the mirror looks like                      |
| Backfill days        | Walk N days of history at save time (0 = forward-only)        |
| Enabled              | Disabled rules are inert until you re-enable them             |

## Filters

Filters are AND'd together — every condition must match. An empty
filter matches every non-cancelled event.

| Filter           | Effect                                                      |
| ---------------- | ----------------------------------------------------------- |
| Title regex      | Go `regexp` matched against `event.Summary`                 |
| Color IDs        | Comma-separated; matches `event.ColorId`                    |
| Attendee any     | Mirror only if any attendee email matches one of these      |
| Free/busy        | `busy` = opaque only; `free` = transparent only             |
| All-day          | `only` = all-day only; `exclude` = timed only               |
| Start hour ≥     | Local-time hour bound (rejects all-day events)              |
| Start hour <     | Upper local-time bound                                      |

Cancelled events are always rejected by the filter — but they still
trigger **deletion** of any existing mirror. So if you cancel an event
on the source, the mirror disappears too.

## Transforms

Transforms shape the mirror event before it's written.

| Field                 | Effect                                                                      |
| --------------------- | --------------------------------------------------------------------------- |
| Title template        | `{title}` and `{location}` are substituted; defaults to source summary      |
| Force "busy"          | Sets the mirror's transparency to `opaque` regardless of source             |
| Strip attendees       | Mirror has no attendees                                                     |
| Strip description     | Mirror has empty description                                                |
| Visibility            | `default`/`public`/`private` to override the source                         |

Time fields, recurrence, and the original location are always preserved.

## Direction

### `one_way`

Source → target. Updates and deletes propagate; updates on the target
are *not* mirrored back. This is the safe default.

### `bidirectional`

Source ↔ target. The rule engine processes events from both calendars.
Two safeguards prevent infinite loops:

1. **Loop guard via `extendedProperties`** — every mirror skulid
   writes carries `skulidManaged=1`, and the rule engine refuses
   to re-process those events.
2. **Etag dedup** — if a webhook fires for an event whose etag matches
   what we last saw, the rule skips the no-op update.

Bidirectional rules use a synthetic key (`rev:<event_id>`) for the
reverse direction's `event_link` row, so forward and reverse passes
don't collide on the unique constraint.

## Backfill

By default, rules only mirror new changes — events that already existed
on the source before the rule was created are ignored. To pull in
history:

1. Set **Backfill last N days** when saving the rule.
2. After save, click **Backfill Nd** on the rule row.

Backfill walks the source calendar from `now - N days` and runs every
event through the rule engine once. The rule's `backfill_done` flag is
set when complete, hiding the button.

If you change the rule and want to re-backfill, edit the rule (which
implicitly resets the flag isn't done automatically — manually clear it
via SQL if you must, or just change the backfill_days value and re-save).

## Manual sync

The **Sync now** button on a rule enqueues an incremental sync for the
source calendar's account+calendar combo. This is the same mechanism
the polling fallback uses, but on demand.

## Audit trail

Every action the rule engine takes lands in the audit log with
`kind="rule"`, the rule ID, the source event ID, and the action:

- `create` — a new mirror was inserted
- `update` — an existing mirror was updated
- `delete` — the source was cancelled or the mirror no longer matches the filter
- `filter_drop` — the source no longer matches the filter
- `error` — the engine hit an error (message attached)
- `backfill_complete` — a backfill pass finished

See [Operations](Operations#audit-log) for how to use this.

## Common patterns

### "Mirror my work calendar to my personal one as 'Busy' blocks"

- Source: work calendar
- Target: personal calendar
- Direction: one_way
- Transform: title template = `Busy`, force busy = on, strip attendees = on,
  strip description = on, visibility = private

### "Sync events between two calendars I both edit"

- Direction: bidirectional
- Primary side: pick whichever you trust more (used to break tie on
  conflicting concurrent edits — though this is rare)

### "Only mirror confirmed customer meetings"

- Filter: title regex `^\\[CUST\\]`, attendee any = `cust@example.com`
- Transform: title template = `{title}` (default), strip description = on

### "Pull only my morning events"

- Filter: start hour ≥ `6`, start hour < `12`
- All-day: exclude

## Limitations

- No conflict UI. If a bidirectional rule sees concurrent edits on
  both sides, the most recent wins.
- No event chunking. Very large calendars (tens of thousands of events)
  during a backfill could slow the worker.
- Cross-account rules work but use the source account's token to read
  and the target account's token to write — both must be connected.
