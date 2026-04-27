# Hours

skulid stores three windows per account, mirroring Reclaim's model:

| Kind     | Used by                                                                |
| -------- | ---------------------------------------------------------------------- |
| Working  | Tasks, the sync rule "working hours only" toggle, smart-block defaults |
| Personal | Habits configured with `hours_kind=personal` (e.g. Lunch)              |
| Meeting  | Habits with `hours_kind=meeting`; future scheduling-link feature       |

Personal and Meeting **fall back to Working** when their own column
is blank. Most users only need to fill in Working.

## Editing

**Settings → Hours**. Each connected account gets its own form. Each
day-of-week takes a comma-separated list of `HH:MM-HH:MM` ranges:

```
09:00-12:00,13:00-17:00
```

Time zones are IANA names (`America/Chicago`, `Europe/Berlin`). The
scheduler interprets your hours in this timezone, so DST transitions
"just work" — `09:00-17:00` always means 9am-5pm wall clock.

## Where the helpers live

The pure window arithmetic — `Parse`, `Expand`, `Merge`,
`SubtractBusy`, `MergeWithGap`, `FirstFitSlot`, `NearestFitSlot` —
lives in `internal/hours/`. Tests cover the spring-forward DST case
in America/Chicago.

## Effective hours

`db.Account.EffectiveHours(kind)` returns the JSON blob the engine
should use after applying fallbacks. Always call this rather than
reading the column directly, so the fallback logic is preserved.
