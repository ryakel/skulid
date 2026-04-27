# Hours

skulid stores three windows per account *and* per calendar, mirroring
Reclaim's model with one extra dimension. The per-calendar override
chain is:

```
calendar override  ->  account default  ->  built-in default
```

Edit account-level hours at **Settings → Hours**, calendar-level
overrides at **Accounts → calendar Settings**. Three kinds:

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

`db.EffectiveCalendarHours(cal, account, kind)` returns the JSON blob
the engine should use after applying both fallback chains: per-calendar
override first, then per-account fallback (with personal/meeting
fallback to working inside the account). Always call this rather than
reading either column directly, so the fallback logic is preserved.

`db.Account.EffectiveHours(kind)` is the lower-level account-only
helper; use it only when you don't have a calendar in context (e.g.
for legacy migrations).
