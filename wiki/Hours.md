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

**Settings → Hours** for the account, **Accounts → calendar Settings**
for one calendar. Both pages use the same grid: a row per weekday, a
column per kind.

Each box takes a comma-separated list of `HH:MM-HH:MM` ranges:

```
09:00-12:00,13:00-17:00
```

Ranges are normalised on save, so `9:00-17:00`, `09:00 - 17:00` and a
range pasted with an en-dash all store as `09:00-17:00`. Anything that
isn't a time range is rejected with an error naming the day and the
offending text — it is never stored and silently ignored.

**An empty box means "inherit".** The grey placeholder in an empty box
is what it currently inherits, so a box reading `09:00-17:00` in grey
behaves that way even though nothing is typed in it. A box whose
placeholder reads `unavailable` inherits nothing: no time is available
that day.

**Quick fill.** Type a range into the box at the top of a column and
press **Mon–Fri** (or Enter) to drop it into all five weekdays, **All 7**
for the whole week, or **Clear** to empty the column and go back to
inheriting.

### Time zones

The scheduler interprets your hours in a single IANA time zone, so DST
transitions "just work" — `09:00-17:00` always means 9am-5pm wall clock.

The account form requires a zone; it is the root of the chain and has
nothing above it to inherit from.

A calendar **inherits its zone from Google** by default, and that is
almost always what you want: move the calendar to another zone in
Google and its availability moves with it, with nothing to re-pick here.
Pin an explicit zone on the calendar only when you want its hours read
somewhere other than where Google says the calendar lives.

## Where the helpers live

The pure window arithmetic — `Parse`, `Expand`, `Merge`,
`SubtractBusy`, `MergeWithGap`, `FirstFitSlot`, `NearestFitSlot`,
`ChunkedSlots` —
lives in `internal/hours/`. Tests cover the spring-forward DST case
in America/Chicago.

`NormalizeRange` / `NormalizeRanges` are what the forms validate
against, and `ParseRange` is built on the same parser so the set of
ranges the UI accepts and the set the engine can expand cannot drift
apart. `ZoneGroupsWith` supplies the picker's options; it is a curated
subset of the IANA database, and it always includes whatever zone is
currently stored so a hand-set exotic zone survives a round trip
through the form.

## Effective hours

`db.EffectiveCalendarHours(cal, account, kind)` returns the JSON blob
the engine should use after applying both fallback chains: per-calendar
override first, then per-account fallback (with personal/meeting
fallback to working inside the account). Always call this rather than
reading either column directly, so the fallback logic is preserved.

It also resolves the blob's time zone. A blob stored without one gets
`db.CalendarZone(cal, account)` — Google's zone for the calendar, then
the account's, then UTC. A zone written into the blob always wins, which
is how an explicit per-calendar override works.

`db.Account.EffectiveHours(kind)` is the lower-level account-only
helper; use it only when you don't have a calendar in context (e.g.
for legacy migrations).
