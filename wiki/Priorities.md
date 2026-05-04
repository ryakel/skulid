# Priorities

A four-column Kanban that buckets every active [task](Tasks) by
priority. Reclaim's "Priorities" view, scoped to the entities skulid
actually owns (tasks).

## Columns

| Column   | Color | Meaning                                          |
| -------- | ----- | ------------------------------------------------ |
| Critical | red   | Must-do today / can't slip                       |
| High     | amber | Should-do this week                              |
| Medium   | indigo | Default for new tasks                           |
| Low      | gray  | Nice-to-do; the scheduler will defer these       |

The colors are baked into the Kanban (not editable yet). Tasks
themselves carry the priority slug, so renaming a column is just a
template tweak.

## Cards

Each card shows:

- Title (clickable — opens the task editor)
- Duration
- Due (humanized: "today" / "tomorrow" / "Mon Apr 27")
- Scheduled time (when set: "Mon 9:00 AM")
- Status (only when something other than `scheduled`)

Completed and cancelled tasks are intentionally hidden. See the
**Tasks** page for the full list.

## Future

Drag-and-drop between columns to change priority, inline edit of due
date, mass-recompute trigger. None of which exist yet — open an
issue if you want any of them.
