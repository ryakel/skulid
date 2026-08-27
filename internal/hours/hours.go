// Package hours holds the pure timewindow + working-hours helpers shared
// across the sync engine, the smart-block engine, the upcoming task/habit
// scheduler, and any availability calculation. No I/O, no context.Context.
package hours

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// WorkingHours describes per-weekday availability windows in a specific IANA
// timezone. Each window is "HH:MM-HH:MM" in 24h local time.
type WorkingHours struct {
	TimeZone string              `json:"time_zone"`
	Days     map[string][]string `json:"days"` // "mon" -> ["09:00-12:00","13:00-17:00"]
}

// Default returns a sensible default: Mon-Fri 9-5 UTC.
func Default() WorkingHours {
	return WorkingHours{
		TimeZone: "UTC",
		Days: map[string][]string{
			"mon": {"09:00-17:00"},
			"tue": {"09:00-17:00"},
			"wed": {"09:00-17:00"},
			"thu": {"09:00-17:00"},
			"fri": {"09:00-17:00"},
		},
	}
}

// Parse decodes a working-hours JSON blob. An empty/null blob returns the
// Default; missing time zone defaults to UTC; nil days map is normalized.
func Parse(raw json.RawMessage) (WorkingHours, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return Default(), nil
	}
	var w WorkingHours
	if err := json.Unmarshal(raw, &w); err != nil {
		return w, err
	}
	if w.TimeZone == "" {
		w.TimeZone = "UTC"
	}
	if w.Days == nil {
		w.Days = map[string][]string{}
	}
	return w, nil
}

// Window is a half-open [Start, End) interval.
type Window struct {
	Start time.Time
	End   time.Time
}

// Expand renders the configured weekday hours into concrete windows in the
// supplied location across [from, to). Honors DST naturally because each day's
// HH:MM is interpreted in loc.
func Expand(wh WorkingHours, from, to time.Time, loc *time.Location) []Window {
	var out []Window
	day := from
	for day.Before(to) {
		key := DayKey(day.Weekday())
		for _, r := range wh.Days[key] {
			start, end, ok := ParseRange(r, day, loc)
			if !ok {
				continue
			}
			if end.Before(from) || start.After(to) {
				continue
			}
			if start.Before(from) {
				start = from
			}
			if end.After(to) {
				end = to
			}
			out = append(out, Window{start, end})
		}
		day = day.AddDate(0, 0, 1)
	}
	return out
}

// DayKey maps a time.Weekday to the lowercase three-letter key used in
// WorkingHours.Days ("mon", "tue", ...).
func DayKey(d time.Weekday) string {
	switch d {
	case time.Monday:
		return "mon"
	case time.Tuesday:
		return "tue"
	case time.Wednesday:
		return "wed"
	case time.Thursday:
		return "thu"
	case time.Friday:
		return "fri"
	case time.Saturday:
		return "sat"
	case time.Sunday:
		return "sun"
	}
	return ""
}

// ParseRange turns "HH:MM-HH:MM" into a concrete window on the given day in
// loc. Returns ok=false on any parse error, range inversion, or out-of-bounds
// hour/minute.
func ParseRange(r string, day time.Time, loc *time.Location) (time.Time, time.Time, bool) {
	var sh, sm, eh, em int
	var trailing rune
	n, _ := fmt.Sscanf(r, "%d:%d-%d:%d%c", &sh, &sm, &eh, &em, &trailing)
	if n != 4 {
		return time.Time{}, time.Time{}, false
	}
	if sh < 0 || sh > 23 || eh < 0 || eh > 23 || sm < 0 || sm > 59 || em < 0 || em > 59 {
		return time.Time{}, time.Time{}, false
	}
	start := time.Date(day.Year(), day.Month(), day.Day(), sh, sm, 0, 0, loc)
	end := time.Date(day.Year(), day.Month(), day.Day(), eh, em, 0, 0, loc)
	if !end.After(start) {
		return time.Time{}, time.Time{}, false
	}
	return start, end, true
}

// Merge coalesces overlapping or touching windows. Touching at the boundary
// counts as overlap (we want [9-10] + [10-11] = [9-11]).
func Merge(in []Window) []Window {
	if len(in) == 0 {
		return nil
	}
	sort.Slice(in, func(i, j int) bool { return in[i].Start.Before(in[j].Start) })
	out := []Window{in[0]}
	for _, w := range in[1:] {
		last := &out[len(out)-1]
		if !w.Start.After(last.End) {
			if w.End.After(last.End) {
				last.End = w.End
			}
			continue
		}
		out = append(out, w)
	}
	return out
}

// SubtractBusy returns the parts of avail not covered by any busy window.
func SubtractBusy(avail, busy []Window) []Window {
	var out []Window
	for _, a := range avail {
		segs := []Window{a}
		for _, b := range busy {
			var next []Window
			for _, s := range segs {
				if !Overlap(s, b) {
					next = append(next, s)
					continue
				}
				if s.Start.Before(b.Start) {
					next = append(next, Window{s.Start, b.Start})
				}
				if s.End.After(b.End) {
					next = append(next, Window{b.End, s.End})
				}
			}
			segs = next
			if len(segs) == 0 {
				break
			}
		}
		out = append(out, segs...)
	}
	return out
}

// MergeWithGap is like Merge but treats two windows separated by ≤ gap as
// adjacent. gap=0 collapses to plain Merge semantics on touching boundaries.
func MergeWithGap(in []Window, gap time.Duration) []Window {
	if len(in) == 0 {
		return nil
	}
	sort.Slice(in, func(i, j int) bool { return in[i].Start.Before(in[j].Start) })
	out := []Window{in[0]}
	for _, w := range in[1:] {
		last := &out[len(out)-1]
		if w.Start.Sub(last.End) <= gap {
			if w.End.After(last.End) {
				last.End = w.End
			}
			continue
		}
		out = append(out, w)
	}
	return out
}

// Overlap reports whether two half-open windows have any intersection.
// Touching at the boundary does not count as overlap.
func Overlap(a, b Window) bool {
	return a.Start.Before(b.End) && b.Start.Before(a.End)
}

// FirstFitSlot returns the earliest contiguous slot of `duration` lying inside
// (avail - busy) and starting at or after `notBefore`. Used to place tasks
// onto the next available working-hours window.
func FirstFitSlot(avail, busy []Window, duration time.Duration, notBefore time.Time) (Window, bool) {
	free := SubtractBusy(avail, busy)
	for _, f := range free {
		start := f.Start
		if notBefore.After(start) {
			start = notBefore
		}
		end := start.Add(duration)
		if !end.After(f.End) {
			return Window{Start: start, End: end}, true
		}
	}
	return Window{}, false
}

// NearestFitSlot returns the slot of `duration` whose start time is closest
// to `ideal`, considered only within ±flex of ideal and only within
// (avail - busy). Ties go to the earlier slot. Used to place habits at
// their preferred time, drifting only as far as flex allows.
func NearestFitSlot(avail, busy []Window, duration, flex time.Duration, ideal time.Time) (Window, bool) {
	earliest := ideal.Add(-flex)
	latest := ideal.Add(flex)
	free := SubtractBusy(avail, busy)

	var best Window
	bestFound := false
	var bestDist time.Duration
	for _, f := range free {
		// In each free window, the latest start that still fits is f.End - duration.
		// We're allowed to start anywhere in [max(f.Start, earliest), min(f.End-duration, latest)].
		lo := f.Start
		if earliest.After(lo) {
			lo = earliest
		}
		hi := f.End.Add(-duration)
		if latest.Before(hi) {
			hi = latest
		}
		if hi.Before(lo) {
			continue
		}
		// Clamp ideal into [lo, hi] — that's the closest legal start to ideal
		// inside this free window.
		start := ideal
		if start.Before(lo) {
			start = lo
		}
		if start.After(hi) {
			start = hi
		}
		dist := start.Sub(ideal)
		if dist < 0 {
			dist = -dist
		}
		if !bestFound || dist < bestDist {
			best = Window{Start: start, End: start.Add(duration)}
			bestFound = true
			bestDist = dist
		}
	}
	return best, bestFound
}

// ChunkPlan describes how a task's duration should be spread across the
// calendar. Slots are in chronological order and together account for exactly
// the requested total.
type ChunkPlan struct {
	Slots []Window
	// Placeable is how much of the requested total could be placed. It equals
	// the total when Fits is true, and is the best that was available
	// otherwise -- enough to tell the user "6 of your 8 hours would fit".
	Placeable time.Duration
	Fits      bool
}

// ChunkedSlots spreads `total` across the earliest free time in
// (avail - busy) at or after `notBefore`, in at most `maxChunks` pieces of at
// least `minChunk` each.
//
// It always prefers one contiguous block: if the whole total fits in a single
// free window, that is what comes back, so an ordinary task keeps landing
// where it always did and nothing churns.
//
// Free windows shorter than minChunk are skipped rather than filled, so an
// eight-hour task doesn't shatter into a dozen twenty-minute fragments
// wedged between meetings. The final piece is the one exception: whatever
// remains is taken even if it falls under minChunk, since a 15-minute tail
// beats leaving the whole task unplaced over it.
//
// When the free time runs out before the total does, the plan comes back with
// Fits false and every slot it did find. The caller decides whether a partial
// placement is useful; reporting how much would have fit is more useful than
// a bare failure either way.
func ChunkedSlots(avail, busy []Window, total, minChunk time.Duration, notBefore time.Time, maxChunks int) ChunkPlan {
	if total <= 0 || maxChunks <= 0 {
		return ChunkPlan{Fits: total <= 0}
	}
	if minChunk <= 0 {
		minChunk = total
	}
	if minChunk > total {
		minChunk = total
	}

	free := SubtractBusy(avail, busy)

	// One contiguous block wins whenever it can.
	for _, f := range free {
		start := f.Start
		if notBefore.After(start) {
			start = notBefore
		}
		if end := start.Add(total); !end.After(f.End) {
			return ChunkPlan{
				Slots:     []Window{{Start: start, End: end}},
				Placeable: total,
				Fits:      true,
			}
		}
	}

	var slots []Window
	remaining := total
	for _, f := range free {
		if remaining <= 0 || len(slots) >= maxChunks {
			break
		}
		start := f.Start
		if notBefore.After(start) {
			start = notBefore
		}
		capacity := f.End.Sub(start)
		if capacity <= 0 {
			continue
		}
		take := remaining
		if capacity < take {
			take = capacity
		}
		// Skip a window too small to be worth breaking into, unless taking it
		// finishes the task.
		if take < minChunk && take < remaining {
			continue
		}
		slots = append(slots, Window{Start: start, End: start.Add(take)})
		remaining -= take
	}

	return ChunkPlan{
		Slots:     slots,
		Placeable: total - remaining,
		Fits:      remaining <= 0,
	}
}
