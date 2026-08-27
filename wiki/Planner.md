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
- **Events**: positioned absolutely within their day column. Concurrent
  events are laid out in side-by-side lanes rather than stacked, so an
  overlapping block is legible rather than hidden behind its neighbour.
  Background is a tint of the category color; left bar is the full
  saturation. Title and start time + duration shown on hover.

## Navigation

Four view modes: **day**, **3-day**, **week** and **month**. Day, 3-day and
week share the timeline; month renders as a 6×7 grid with leading and
trailing spillover days dimmed. Pick one with the view selector; the choice
persists as your default.

Prev / Today / Next step by whatever the current view spans. The URL carries
the state as:

| Parameter | Meaning |
| --- | --- |
| `?view=` | `day`, `3day`, `week` or `month` |
| `?at=YYYY-MM-DD` | the anchor date the view is built around |
| `?w=YYYY-MM-DD` | legacy week anchor; honoured only when the view is `week` |

## Time zone

The planner renders in the timezone declared on the **first connected
account's Working hours** (falls back to UTC). To change it, edit the
hours of that account at **Settings → Hours**.

## What it doesn't do (yet)

- **Drag-and-drop rescheduling.** Move events in Google Calendar; the
  next sync brings them back here. Tracked as SKUL-13.
- **Inline event creation.** Use the AI assistant or `/tasks/new`.

## Performance

The handler issues one `Events.list` per connected calendar per page
load — typically <20 calls, parallel-friendly but currently
sequential. Consider a 2-second wait normal on first paint; subsequent
loads are faster as Google's HTTP cache warms.
