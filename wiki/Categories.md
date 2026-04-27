# Categories

Categories drive color-coding on the Planner and grouping in hour
totals. skulid ships with eight built-in categories matching Reclaim's
default set; you can rename them and pick new colors, but slugs are
fixed (the engines reference them by slug).

## Built-in set

| Slug         | Default name        | Default color |
| ------------ | ------------------- | ------------- |
| `focus`      | Focus               | indigo        |
| `one_on_one` | 1:1 meetings        | green         |
| `team`       | Team meetings       | green         |
| `external`   | External meetings   | red           |
| `personal`   | Personal            | orange        |
| `travel`     | Travel & breaks     | amber         |
| `other`      | Other work          | gray          |
| `free`       | Free                | lime          |

Edit at **Settings → Categories**. Only the display name and color
are editable; the slug never changes.

## How an event gets a category

The categorizer (`internal/category/`) is pure heuristic logic. It
takes an event plus a small context (your owner email domains, the
calendar's optional default) and returns a slug:

1. **Cancelled** → `other`.
2. **Transparent** (free) → `free`, or the calendar default.
3. **Title keyword** — "lunch"/"break"/"travel"/"commute"/etc. →
   `travel`. "focus"/"deep work"/"heads down" → `focus`.
4. **All-day** (no DateTime) → `personal`, or the calendar default.
5. **Solo / 1 attendee** → `focus`, or the calendar default.
6. **2 attendees** → `one_on_one`.
7. **3+ attendees, all internal** → `team`.
8. **3+ attendees, any external** → `external`.

Resource attendees (rooms, equipment) are ignored when counting.

## Pinning

Three places can pin a category and bypass the heuristic:

- **Per calendar**: a "Family" calendar can default everything on it
  to `personal` (`calendar.default_category_id`).
- **Per sync rule**: the mirror events get the chosen category
  (`sync_rule.category_id`).
- **Per smart block / task / habit**: the written event gets the
  chosen category.

## Internal vs external

The "team vs external" boundary is based on email-domain comparison
to your **owner domains** — derived from your connected accounts'
emails. With no accounts connected, every multi-attendee meeting
collapses to `team` (conservative — we don't know which is internal,
so we don't escalate to external).
