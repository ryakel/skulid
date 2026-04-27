package sync

import (
	"reflect"
	"testing"
	"time"
)

// mustLoad loads a location or fails the test.
func mustLoad(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Skipf("timezone data %q unavailable: %v", name, err)
	}
	return loc
}

func ts(t *testing.T, layout, value string, loc *time.Location) time.Time {
	t.Helper()
	v, err := time.ParseInLocation(layout, value, loc)
	if err != nil {
		t.Fatalf("parse %q: %v", value, err)
	}
	return v
}

func TestParseRangeHappyPath(t *testing.T) {
	loc := mustLoad(t, "UTC")
	day := ts(t, "2006-01-02", "2026-04-27", loc)
	start, end, ok := parseRange("09:00-17:30", day, loc)
	if !ok {
		t.Fatal("expected parse ok")
	}
	if start.Hour() != 9 || start.Minute() != 0 {
		t.Errorf("start wrong: %v", start)
	}
	if end.Hour() != 17 || end.Minute() != 30 {
		t.Errorf("end wrong: %v", end)
	}
}

func TestParseRangeRejectsInverted(t *testing.T) {
	loc := mustLoad(t, "UTC")
	day := ts(t, "2006-01-02", "2026-04-27", loc)
	if _, _, ok := parseRange("17:00-09:00", day, loc); ok {
		t.Fatal("expected rejection of end-before-start")
	}
}

func TestParseRangeRejectsGarbage(t *testing.T) {
	loc := mustLoad(t, "UTC")
	day := ts(t, "2006-01-02", "2026-04-27", loc)
	for _, bad := range []string{"", "9-5", "09:00", "9:00-17:00:00", "abc"} {
		if _, _, ok := parseRange(bad, day, loc); ok {
			t.Errorf("%q should not parse", bad)
		}
	}
}

func TestDayKeyCoversAllWeekdays(t *testing.T) {
	want := map[time.Weekday]string{
		time.Monday: "mon", time.Tuesday: "tue", time.Wednesday: "wed",
		time.Thursday: "thu", time.Friday: "fri", time.Saturday: "sat", time.Sunday: "sun",
	}
	for d, w := range want {
		if got := dayKey(d); got != w {
			t.Errorf("dayKey(%v): got %q want %q", d, got, w)
		}
	}
}

func TestMergeWindowsCoalescesOverlapAndAdjacent(t *testing.T) {
	loc := mustLoad(t, "UTC")
	w := func(s, e string) window {
		return window{
			Start: ts(t, "15:04", s, loc),
			End:   ts(t, "15:04", e, loc),
		}
	}
	in := []window{w("09:00", "10:00"), w("09:30", "11:00"), w("11:00", "12:00"), w("13:00", "14:00")}
	got := mergeWindows(in)
	want := []window{w("09:00", "12:00"), w("13:00", "14:00")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v want %+v", got, want)
	}
}

func TestMergeWindowsEmpty(t *testing.T) {
	if mergeWindows(nil) != nil {
		t.Fatal("expected nil for empty input")
	}
}

func TestSubtractBusyAllShapes(t *testing.T) {
	loc := mustLoad(t, "UTC")
	w := func(s, e string) window {
		return window{
			Start: ts(t, "15:04", s, loc),
			End:   ts(t, "15:04", e, loc),
		}
	}

	tests := []struct {
		name      string
		avail     []window
		busy      []window
		want      []window
	}{
		{
			name:  "no busy returns avail unchanged",
			avail: []window{w("09:00", "12:00")},
			busy:  nil,
			want:  []window{w("09:00", "12:00")},
		},
		{
			name:  "busy fully inside splits",
			avail: []window{w("09:00", "17:00")},
			busy:  []window{w("12:00", "13:00")},
			want:  []window{w("09:00", "12:00"), w("13:00", "17:00")},
		},
		{
			name:  "busy at left edge trims",
			avail: []window{w("09:00", "17:00")},
			busy:  []window{w("09:00", "10:00")},
			want:  []window{w("10:00", "17:00")},
		},
		{
			name:  "busy at right edge trims",
			avail: []window{w("09:00", "17:00")},
			busy:  []window{w("16:00", "17:00")},
			want:  []window{w("09:00", "16:00")},
		},
		{
			name:  "busy fully covers eliminates",
			avail: []window{w("09:00", "10:00")},
			busy:  []window{w("08:00", "11:00")},
			want:  nil,
		},
		{
			name:  "busy outside avail no-op",
			avail: []window{w("09:00", "10:00")},
			busy:  []window{w("12:00", "13:00")},
			want:  []window{w("09:00", "10:00")},
		},
		{
			name:  "two busies split avail into three",
			avail: []window{w("09:00", "17:00")},
			busy:  []window{w("10:00", "11:00"), w("13:00", "14:00")},
			want:  []window{w("09:00", "10:00"), w("11:00", "13:00"), w("14:00", "17:00")},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := subtractBusy(tc.avail, tc.busy)
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %+v want %+v", got, tc.want)
			}
		})
	}
}

func TestMergeWithGap(t *testing.T) {
	loc := mustLoad(t, "UTC")
	w := func(s, e string) window {
		return window{
			Start: ts(t, "15:04", s, loc),
			End:   ts(t, "15:04", e, loc),
		}
	}
	in := []window{w("09:00", "10:00"), w("10:10", "11:00"), w("12:00", "13:00")}
	// gap=15min: first two merge, third stays separate.
	got := mergeWithGap(in, 15*time.Minute)
	want := []window{w("09:00", "11:00"), w("12:00", "13:00")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v want %+v", got, want)
	}
	// gap=0: nothing merges.
	got = mergeWithGap(in, 0)
	if !reflect.DeepEqual(got, in) {
		t.Fatalf("gap=0 should not merge, got %+v", got)
	}
}

func TestWindowsOverlap(t *testing.T) {
	loc := mustLoad(t, "UTC")
	w := func(s, e string) window {
		return window{
			Start: ts(t, "15:04", s, loc),
			End:   ts(t, "15:04", e, loc),
		}
	}
	if !windowsOverlap(w("09:00", "10:00"), w("09:30", "10:30")) {
		t.Fatal("expected overlap for partial intersection")
	}
	if windowsOverlap(w("09:00", "10:00"), w("10:00", "11:00")) {
		t.Fatal("touching at the boundary is not overlap")
	}
	if windowsOverlap(w("09:00", "10:00"), w("11:00", "12:00")) {
		t.Fatal("disjoint should not overlap")
	}
}

func TestWorkingWindowsHonorsTimezoneAcrossDST(t *testing.T) {
	loc := mustLoad(t, "America/Chicago")
	wh := WorkingHours{
		TimeZone: "America/Chicago",
		Days:     map[string][]string{"sat": {"09:00-17:00"}, "sun": {"09:00-17:00"}},
	}
	// Spring forward in 2026: Mar 8 (Sun) at 02:00 local.
	from := ts(t, "2006-01-02", "2026-03-07", loc) // Saturday
	to := ts(t, "2006-01-02", "2026-03-09", loc)   // Monday (exclusive)

	got := workingWindows(wh, from, to, loc)
	if len(got) != 2 {
		t.Fatalf("expected 2 windows (Sat+Sun), got %d: %+v", len(got), got)
	}
	for i, want := range []struct {
		day, sh int
	}{{7, 9}, {8, 9}} {
		if got[i].Start.Day() != want.day {
			t.Errorf("window %d: day %d want %d", i, got[i].Start.Day(), want.day)
		}
		if got[i].Start.Hour() != want.sh {
			t.Errorf("window %d: hour %d want %d", i, got[i].Start.Hour(), want.sh)
		}
		// Wall clock duration is 8h on both days.
		if dur := got[i].End.Sub(got[i].Start); dur != 8*time.Hour {
			t.Errorf("window %d: duration %v want 8h", i, dur)
		}
	}
	// Different UTC offset before vs after DST.
	_, off1 := got[0].Start.Zone()
	_, off2 := got[1].Start.Zone()
	if off1 == off2 {
		t.Fatalf("expected DST offset change between Sat and Sun, both %d", off1)
	}
}

func TestParseWorkingHoursDefaults(t *testing.T) {
	wh, err := ParseWorkingHours(nil)
	if err != nil {
		t.Fatal(err)
	}
	if wh.TimeZone == "" {
		t.Fatal("expected default time zone")
	}
	for _, day := range []string{"mon", "tue", "wed", "thu", "fri"} {
		if _, ok := wh.Days[day]; !ok {
			t.Errorf("default working hours missing %s", day)
		}
	}
}

func TestParseWorkingHoursCustom(t *testing.T) {
	raw := []byte(`{"time_zone":"Europe/Berlin","days":{"mon":["10:00-14:00"]}}`)
	wh, err := ParseWorkingHours(raw)
	if err != nil {
		t.Fatal(err)
	}
	if wh.TimeZone != "Europe/Berlin" {
		t.Errorf("tz: got %q", wh.TimeZone)
	}
	if got := wh.Days["mon"]; !reflect.DeepEqual(got, []string{"10:00-14:00"}) {
		t.Errorf("mon: got %v", got)
	}
}

func TestParseWorkingHoursFillsMissingTZ(t *testing.T) {
	raw := []byte(`{"days":{"mon":["10:00-14:00"]}}`)
	wh, err := ParseWorkingHours(raw)
	if err != nil {
		t.Fatal(err)
	}
	if wh.TimeZone == "" {
		t.Fatal("expected default tz when omitted")
	}
}
