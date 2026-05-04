# Planner

A week-at-a-glance timeline rendering every event on every connected
calendar, color-coded by [category](Categories), with hour totals
strip across the top.

## Reading it

- **Top strip**: total hours per category for the visible week.
- **Day headers**: weekday + date. Today is highlighted.
- **All-day strip**: the thin row above the timeline; multi-day
  events span the days they cover.
- **Timeline**: 6am-10pm visible (anything outside that window isn't
  rendered yet — bump the constants in `handlers_planner.go` if you
  routinely have 5am or 11pm events).
- **Events**: positioned absolutely within their day column.
  Background is a tint of the category color; left bar is the full
  saturation. Title and start time + duration shown on hover.

## Navigation

Prev / Today / Next walk one ISO week at a time. The URL carries the
selected week as `?w=YYYY-MM-DD` (any day; the handler snaps to the
preceding Monday).

## Time zone

The planner renders in the timezone declared on the **first connected
account's Working hours** (falls back to UTC). To change it, edit the
hours of that account at **Settings → Hours**.

## What it doesn't do (yet)

- **Drag-and-drop rescheduling.** Move events in Google Calendar; the
  next sync brings them back here.
- **Inline event creation.** Use the AI assistant or `/tasks/new`.
- **Overlapping event lanes.** Concurrent events stack on top of each
  other — readable for 2-3 overlaps, busy if you really pack a slot.
- **Multi-week views.** Just the current week.

## Performance

The handler issues one `Events.list` per connected calendar per page
load — typically <20 calls, parallel-friendly but currently
sequential. Consider a 2-second wait normal on first paint; subsequent
loads are faster as Google's HTTP cache warms.
