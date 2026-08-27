package hours

import (
	"reflect"
	"testing"
	"time"
)

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
	start, end, ok := ParseRange("09:00-17:30", day, loc)
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
	if _, _, ok := ParseRange("17:00-09:00", day, loc); ok {
		t.Fatal("expected rejection of end-before-start")
	}
}

func TestParseRangeRejectsGarbage(t *testing.T) {
	loc := mustLoad(t, "UTC")
	day := ts(t, "2006-01-02", "2026-04-27", loc)
	for _, bad := range []string{"", "9-5", "09:00", "9:00-17:00:00", "abc", "25:00-26:00", "09:60-10:00"} {
		if _, _, ok := ParseRange(bad, day, loc); ok {
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
		if got := DayKey(d); got != w {
			t.Errorf("DayKey(%v): got %q want %q", d, got, w)
		}
	}
}

func TestMergeCoalescesOverlapAndAdjacent(t *testing.T) {
	loc := mustLoad(t, "UTC")
	w := func(s, e string) Window {
		return Window{
			Start: ts(t, "15:04", s, loc),
			End:   ts(t, "15:04", e, loc),
		}
	}
	in := []Window{w("09:00", "10:00"), w("09:30", "11:00"), w("11:00", "12:00"), w("13:00", "14:00")}
	got := Merge(in)
	want := []Window{w("09:00", "12:00"), w("13:00", "14:00")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v want %+v", got, want)
	}
}

func TestMergeEmpty(t *testing.T) {
	if Merge(nil) != nil {
		t.Fatal("expected nil for empty input")
	}
}

func TestSubtractBusyAllShapes(t *testing.T) {
	loc := mustLoad(t, "UTC")
	w := func(s, e string) Window {
		return Window{
			Start: ts(t, "15:04", s, loc),
			End:   ts(t, "15:04", e, loc),
		}
	}

	tests := []struct {
		name  string
		avail []Window
		busy  []Window
		want  []Window
	}{
		{"no busy returns avail unchanged", []Window{w("09:00", "12:00")}, nil, []Window{w("09:00", "12:00")}},
		{"busy fully inside splits", []Window{w("09:00", "17:00")}, []Window{w("12:00", "13:00")}, []Window{w("09:00", "12:00"), w("13:00", "17:00")}},
		{"busy at left edge trims", []Window{w("09:00", "17:00")}, []Window{w("09:00", "10:00")}, []Window{w("10:00", "17:00")}},
		{"busy at right edge trims", []Window{w("09:00", "17:00")}, []Window{w("16:00", "17:00")}, []Window{w("09:00", "16:00")}},
		{"busy fully covers eliminates", []Window{w("09:00", "10:00")}, []Window{w("08:00", "11:00")}, nil},
		{"busy outside avail no-op", []Window{w("09:00", "10:00")}, []Window{w("12:00", "13:00")}, []Window{w("09:00", "10:00")}},
		{"two busies split avail into three", []Window{w("09:00", "17:00")}, []Window{w("10:00", "11:00"), w("13:00", "14:00")}, []Window{w("09:00", "10:00"), w("11:00", "13:00"), w("14:00", "17:00")}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := SubtractBusy(tc.avail, tc.busy)
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
	w := func(s, e string) Window {
		return Window{Start: ts(t, "15:04", s, loc), End: ts(t, "15:04", e, loc)}
	}
	in := []Window{w("09:00", "10:00"), w("10:10", "11:00"), w("12:00", "13:00")}
	got := MergeWithGap(in, 15*time.Minute)
	want := []Window{w("09:00", "11:00"), w("12:00", "13:00")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v want %+v", got, want)
	}
	got = MergeWithGap(in, 0)
	if !reflect.DeepEqual(got, in) {
		t.Fatalf("gap=0 should not merge, got %+v", got)
	}
}

func TestOverlapBoundary(t *testing.T) {
	loc := mustLoad(t, "UTC")
	w := func(s, e string) Window {
		return Window{Start: ts(t, "15:04", s, loc), End: ts(t, "15:04", e, loc)}
	}
	if !Overlap(w("09:00", "10:00"), w("09:30", "10:30")) {
		t.Fatal("partial intersection should overlap")
	}
	if Overlap(w("09:00", "10:00"), w("10:00", "11:00")) {
		t.Fatal("touching boundary should not overlap")
	}
	if Overlap(w("09:00", "10:00"), w("11:00", "12:00")) {
		t.Fatal("disjoint should not overlap")
	}
}

func TestExpandHonorsTimezoneAcrossDST(t *testing.T) {
	loc := mustLoad(t, "America/Chicago")
	wh := WorkingHours{
		TimeZone: "America/Chicago",
		Days:     map[string][]string{"sat": {"09:00-17:00"}, "sun": {"09:00-17:00"}},
	}
	// Spring forward in 2026: Mar 8 (Sun) at 02:00 local.
	from := ts(t, "2006-01-02", "2026-03-07", loc)
	to := ts(t, "2006-01-02", "2026-03-09", loc)

	got := Expand(wh, from, to, loc)
	if len(got) != 2 {
		t.Fatalf("expected 2 windows, got %d: %+v", len(got), got)
	}
	for i, want := range []struct{ day, sh int }{{7, 9}, {8, 9}} {
		if got[i].Start.Day() != want.day {
			t.Errorf("window %d: day %d want %d", i, got[i].Start.Day(), want.day)
		}
		if got[i].Start.Hour() != want.sh {
			t.Errorf("window %d: hour %d want %d", i, got[i].Start.Hour(), want.sh)
		}
		if dur := got[i].End.Sub(got[i].Start); dur != 8*time.Hour {
			t.Errorf("window %d: duration %v want 8h", i, dur)
		}
	}
	_, off1 := got[0].Start.Zone()
	_, off2 := got[1].Start.Zone()
	if off1 == off2 {
		t.Fatalf("expected DST offset change Sat→Sun, both %d", off1)
	}
}

func TestParseDefaults(t *testing.T) {
	wh, err := Parse(nil)
	if err != nil {
		t.Fatal(err)
	}
	if wh.TimeZone == "" {
		t.Fatal("expected default time zone")
	}
	for _, day := range []string{"mon", "tue", "wed", "thu", "fri"} {
		if _, ok := wh.Days[day]; !ok {
			t.Errorf("default missing %s", day)
		}
	}
}

func TestParseCustom(t *testing.T) {
	raw := []byte(`{"time_zone":"Europe/Berlin","days":{"mon":["10:00-14:00"]}}`)
	wh, err := Parse(raw)
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

func TestParseFillsMissingTZ(t *testing.T) {
	raw := []byte(`{"days":{"mon":["10:00-14:00"]}}`)
	wh, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if wh.TimeZone == "" {
		t.Fatal("expected default tz when omitted")
	}
}

func TestParseNullStringIsTreatedAsEmpty(t *testing.T) {
	wh, err := Parse([]byte("null"))
	if err != nil {
		t.Fatalf("Parse(null): %v", err)
	}
	if wh.TimeZone == "" {
		t.Fatal("expected default tz for null input")
	}
}

func TestFirstFitSlotReturnsEarliest(t *testing.T) {
	loc := mustLoad(t, "UTC")
	w := func(s, e string) Window {
		return Window{Start: ts(t, "15:04", s, loc), End: ts(t, "15:04", e, loc)}
	}
	avail := []Window{w("09:00", "12:00"), w("13:00", "17:00")}
	busy := []Window{w("09:30", "10:00")}
	got, ok := FirstFitSlot(avail, busy, 30*time.Minute, ts(t, "15:04", "09:00", loc))
	if !ok {
		t.Fatal("expected a slot")
	}
	if got.Start != ts(t, "15:04", "09:00", loc) || got.End != ts(t, "15:04", "09:30", loc) {
		t.Fatalf("got %+v want 09:00-09:30", got)
	}
}

func TestFirstFitSlotRespectsNotBefore(t *testing.T) {
	loc := mustLoad(t, "UTC")
	w := func(s, e string) Window {
		return Window{Start: ts(t, "15:04", s, loc), End: ts(t, "15:04", e, loc)}
	}
	avail := []Window{w("09:00", "17:00")}
	got, ok := FirstFitSlot(avail, nil, 60*time.Minute, ts(t, "15:04", "10:30", loc))
	if !ok {
		t.Fatal("expected a slot")
	}
	if got.Start != ts(t, "15:04", "10:30", loc) {
		t.Fatalf("got %+v want start 10:30", got)
	}
}

func TestFirstFitSlotNoFit(t *testing.T) {
	loc := mustLoad(t, "UTC")
	w := func(s, e string) Window {
		return Window{Start: ts(t, "15:04", s, loc), End: ts(t, "15:04", e, loc)}
	}
	avail := []Window{w("09:00", "10:00")}
	if _, ok := FirstFitSlot(avail, nil, 90*time.Minute, ts(t, "15:04", "09:00", loc)); ok {
		t.Fatal("expected no fit for 90m in a 60m window")
	}
}

func TestNearestFitSlotPrefersIdeal(t *testing.T) {
	loc := mustLoad(t, "UTC")
	w := func(s, e string) Window {
		return Window{Start: ts(t, "15:04", s, loc), End: ts(t, "15:04", e, loc)}
	}
	avail := []Window{w("09:00", "17:00")}
	ideal := ts(t, "15:04", "12:00", loc)
	got, ok := NearestFitSlot(avail, nil, 60*time.Minute, 90*time.Minute, ideal)
	if !ok {
		t.Fatal("expected a slot")
	}
	if got.Start != ideal {
		t.Fatalf("got %+v want start at ideal 12:00", got)
	}
}

func TestNearestFitSlotDriftsToNearestFreeSlot(t *testing.T) {
	loc := mustLoad(t, "UTC")
	w := func(s, e string) Window {
		return Window{Start: ts(t, "15:04", s, loc), End: ts(t, "15:04", e, loc)}
	}
	avail := []Window{w("09:00", "17:00")}
	// Busy from 11:30 to 12:30 — ideal 12:00 is occluded; we should slide right to 12:30.
	busy := []Window{w("11:30", "12:30")}
	ideal := ts(t, "15:04", "12:00", loc)
	got, ok := NearestFitSlot(avail, busy, 60*time.Minute, 90*time.Minute, ideal)
	if !ok {
		t.Fatal("expected a slot")
	}
	want := ts(t, "15:04", "12:30", loc)
	if got.Start != want {
		t.Fatalf("got %+v want start 12:30", got)
	}
}

func TestNearestFitSlotRespectsFlex(t *testing.T) {
	loc := mustLoad(t, "UTC")
	w := func(s, e string) Window {
		return Window{Start: ts(t, "15:04", s, loc), End: ts(t, "15:04", e, loc)}
	}
	avail := []Window{w("09:00", "17:00")}
	// Whole afternoon busy, leaving only morning options — but flex is 30min
	// around ideal=14:00, so morning is out of reach: expect no fit.
	busy := []Window{w("13:30", "17:00")}
	ideal := ts(t, "15:04", "14:00", loc)
	if _, ok := NearestFitSlot(avail, busy, 60*time.Minute, 30*time.Minute, ideal); ok {
		t.Fatal("expected no fit within ±30m of 14:00")
	}
}

// chunkWin builds a window from two "15:04" times on a fixed UTC day.
func chunkWin(t *testing.T, s, e string) Window {
	t.Helper()
	loc := mustLoad(t, "UTC")
	return Window{Start: ts(t, "15:04", s, loc), End: ts(t, "15:04", e, loc)}
}

func chunkAt(t *testing.T, s string) time.Time {
	t.Helper()
	return ts(t, "15:04", s, mustLoad(t, "UTC"))
}

func TestChunkedSlotsPrefersOneContiguousBlock(t *testing.T) {
	avail := []Window{chunkWin(t, "09:00", "17:00")}
	got := ChunkedSlots(avail, nil, 3*time.Hour, 30*time.Minute, chunkAt(t, "09:00"), 8)
	if !got.Fits {
		t.Fatalf("3h should fit in an 8h day")
	}
	want := []Window{chunkWin(t, "09:00", "12:00")}
	if !reflect.DeepEqual(got.Slots, want) {
		t.Fatalf("got %+v want %+v", got.Slots, want)
	}
}

func TestChunkedSlotsSplitsAcrossGaps(t *testing.T) {
	// A 4-hour task on a day with two 2-hour gaps: it must split rather than
	// stay unplaced, which is the whole point of the change.
	avail := []Window{chunkWin(t, "09:00", "17:00")}
	busy := []Window{chunkWin(t, "11:00", "13:00"), chunkWin(t, "15:00", "17:00")}
	got := ChunkedSlots(avail, busy, 4*time.Hour, 30*time.Minute, chunkAt(t, "09:00"), 8)
	if !got.Fits {
		t.Fatalf("4h should fit across two 2h gaps, got %+v", got)
	}
	want := []Window{chunkWin(t, "09:00", "11:00"), chunkWin(t, "13:00", "15:00")}
	if !reflect.DeepEqual(got.Slots, want) {
		t.Fatalf("got %+v want %+v", got.Slots, want)
	}
}

func TestChunkedSlotsSkipsWindowsBelowMinChunk(t *testing.T) {
	// Three 15-minute slivers and one 2-hour block. A 2-hour task must land in
	// the block alone rather than shattering across the slivers.
	avail := []Window{chunkWin(t, "09:00", "17:00")}
	busy := []Window{
		chunkWin(t, "09:15", "10:00"),
		chunkWin(t, "10:15", "11:00"),
		chunkWin(t, "11:15", "12:00"),
	}
	got := ChunkedSlots(avail, busy, 2*time.Hour, 30*time.Minute, chunkAt(t, "09:00"), 8)
	if !got.Fits {
		t.Fatalf("2h should fit in the 12:00-17:00 block, got %+v", got)
	}
	want := []Window{chunkWin(t, "12:00", "14:00")}
	if !reflect.DeepEqual(got.Slots, want) {
		t.Fatalf("got %+v want %+v", got.Slots, want)
	}
}

func TestChunkedSlotsTakesShortTailToFinish(t *testing.T) {
	// The final piece may fall under minChunk: a 15-minute tail beats leaving
	// the whole task unplaced over it.
	avail := []Window{chunkWin(t, "09:00", "10:00"), chunkWin(t, "11:00", "11:15")}
	got := ChunkedSlots(avail, nil, 75*time.Minute, 30*time.Minute, chunkAt(t, "09:00"), 8)
	if !got.Fits {
		t.Fatalf("1h15m should fit across 1h + 15m, got %+v", got)
	}
	want := []Window{chunkWin(t, "09:00", "10:00"), chunkWin(t, "11:00", "11:15")}
	if !reflect.DeepEqual(got.Slots, want) {
		t.Fatalf("got %+v want %+v", got.Slots, want)
	}
}

func TestChunkedSlotsOnlyTheLastPieceMayBeShort(t *testing.T) {
	// A 2h15m task across a 2h window and a 1h window: the tail is 15m, under
	// the 30m minimum. That is allowed, because the alternative is not placing
	// the task — but it must be the *last* piece, and the total must be exact.
	avail := []Window{chunkWin(t, "09:00", "11:00"), chunkWin(t, "13:00", "14:00")}
	got := ChunkedSlots(avail, nil, 135*time.Minute, 30*time.Minute, chunkAt(t, "09:00"), 8)
	if !got.Fits {
		t.Fatalf("2h15m should fit across 2h + 1h, got %+v", got)
	}
	if len(got.Slots) != 2 {
		t.Fatalf("want 2 slots, got %+v", got.Slots)
	}
	for i, s := range got.Slots[:len(got.Slots)-1] {
		if d := s.End.Sub(s.Start); d < 30*time.Minute {
			t.Errorf("slot %d is %v, below the 30m minimum; only the last may be short", i, d)
		}
	}
	var total time.Duration
	for _, s := range got.Slots {
		total += s.End.Sub(s.Start)
	}
	if total != 135*time.Minute {
		t.Errorf("slots total %v, want 2h15m", total)
	}
}

func TestChunkedSlotsReportsPartialFit(t *testing.T) {
	// Not enough room anywhere: report what would have fit rather than
	// failing bare, so the task can say "6 of your 8 hours".
	avail := []Window{chunkWin(t, "09:00", "11:00"), chunkWin(t, "13:00", "15:00")}
	got := ChunkedSlots(avail, nil, 8*time.Hour, 30*time.Minute, chunkAt(t, "09:00"), 8)
	if got.Fits {
		t.Fatalf("8h cannot fit in 4h of free time")
	}
	if got.Placeable != 4*time.Hour {
		t.Errorf("Placeable = %v, want 4h", got.Placeable)
	}
}

func TestChunkedSlotsRespectsMaxChunks(t *testing.T) {
	avail := []Window{
		chunkWin(t, "09:00", "10:00"),
		chunkWin(t, "11:00", "12:00"),
		chunkWin(t, "13:00", "14:00"),
		chunkWin(t, "15:00", "16:00"),
	}
	got := ChunkedSlots(avail, nil, 4*time.Hour, 30*time.Minute, chunkAt(t, "09:00"), 2)
	if got.Fits {
		t.Fatalf("4h across 4 one-hour windows cannot fit in 2 chunks")
	}
	if len(got.Slots) != 2 {
		t.Fatalf("want at most 2 slots, got %d", len(got.Slots))
	}
	if got.Placeable != 2*time.Hour {
		t.Errorf("Placeable = %v, want 2h", got.Placeable)
	}
}

func TestChunkedSlotsHonoursNotBefore(t *testing.T) {
	avail := []Window{chunkWin(t, "09:00", "17:00")}
	got := ChunkedSlots(avail, nil, time.Hour, 30*time.Minute, chunkAt(t, "13:30"), 8)
	if !got.Fits || len(got.Slots) != 1 {
		t.Fatalf("got %+v", got)
	}
	if !got.Slots[0].Start.Equal(chunkAt(t, "13:30")) {
		t.Errorf("start = %v, want 13:30", got.Slots[0].Start)
	}
}

func TestChunkedSlotsEdgeCases(t *testing.T) {
	avail := []Window{chunkWin(t, "09:00", "17:00")}
	if got := ChunkedSlots(avail, nil, 0, 30*time.Minute, chunkAt(t, "09:00"), 8); !got.Fits || len(got.Slots) != 0 {
		t.Errorf("zero duration should trivially fit with no slots, got %+v", got)
	}
	if got := ChunkedSlots(avail, nil, time.Hour, 30*time.Minute, chunkAt(t, "09:00"), 0); got.Fits {
		t.Errorf("zero maxChunks should not fit, got %+v", got)
	}
	if got := ChunkedSlots(nil, nil, time.Hour, 30*time.Minute, chunkAt(t, "09:00"), 8); got.Fits {
		t.Errorf("no availability should not fit, got %+v", got)
	}
	// A minChunk larger than the task itself must not make the task unplaceable.
	if got := ChunkedSlots(avail, nil, 20*time.Minute, time.Hour, chunkAt(t, "09:00"), 8); !got.Fits {
		t.Errorf("a 20m task should fit even with a 1h minChunk, got %+v", got)
	}
}
