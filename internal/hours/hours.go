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
