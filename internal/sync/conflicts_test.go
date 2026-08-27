package sync

import (
	"testing"
	"time"

	gcal "google.golang.org/api/calendar/v3"

	"github.com/ryakel/skulid/internal/db"
)

func TestEnabledCalendars(t *testing.T) {
	in := []db.Calendar{
		{ID: 1, Summary: "work", Enabled: true},
		{ID: 2, Summary: "archived", Enabled: false},
		{ID: 3, Summary: "personal", Enabled: true},
	}

	got := enabledCalendars(in)
	if len(got) != 2 {
		t.Fatalf("want 2 enabled calendars, got %d", len(got))
	}
	for _, c := range got {
		if !c.Enabled {
			t.Errorf("disabled calendar %q survived the filter", c.Summary)
		}
	}

	if none := enabledCalendars(nil); len(none) != 0 {
		t.Errorf("nil input should yield nothing, got %d", len(none))
	}
}

// One freebusy call per account, not per calendar.
func TestGroupByAccount(t *testing.T) {
	in := []db.Calendar{
		{ID: 1, AccountID: 7, GoogleCalendarID: "work@corp"},
		{ID: 2, AccountID: 7, GoogleCalendarID: "team@corp"},
		{ID: 3, AccountID: 1, GoogleCalendarID: "me@gmail"},
	}

	got := groupByAccount(in)
	if len(got) != 2 {
		t.Fatalf("want 2 accounts, got %d", len(got))
	}
	if len(got[7]) != 2 {
		t.Errorf("account 7 should hold 2 calendars, got %d", len(got[7]))
	}
	if len(got[1]) != 1 {
		t.Errorf("account 1 should hold 1 calendar, got %d", len(got[1]))
	}
}

func TestPeriodsToWindows(t *testing.T) {
	periods := []*gcal.TimePeriod{
		{Start: "2026-04-27T10:00:00Z", End: "2026-04-27T11:00:00Z"},
		{Start: "2026-04-27T14:00:00Z", End: "2026-04-27T15:30:00Z"},
	}

	got := periodsToWindows(periods)
	if len(got) != 2 {
		t.Fatalf("want 2 windows, got %d", len(got))
	}
	wantStart := time.Date(2026, 4, 27, 10, 0, 0, 0, time.UTC)
	if !got[0].Start.Equal(wantStart) {
		t.Errorf("first window starts %v, want %v", got[0].Start, wantStart)
	}
	if !got[0].End.Equal(time.Date(2026, 4, 27, 11, 0, 0, 0, time.UTC)) {
		t.Errorf("first window ends %v", got[0].End)
	}
}

// An unparseable period is skipped rather than taking the whole placement
// down -- but the ones around it must survive.
func TestPeriodsToWindowsSkipsUnparseable(t *testing.T) {
	periods := []*gcal.TimePeriod{
		{Start: "2026-04-27T10:00:00Z", End: "2026-04-27T11:00:00Z"},
		{Start: "not a timestamp", End: "2026-04-27T12:00:00Z"},
		{Start: "2026-04-27T13:00:00Z", End: "also not a timestamp"},
		{Start: "2026-04-27T14:00:00Z", End: "2026-04-27T15:00:00Z"},
	}

	got := periodsToWindows(periods)
	if len(got) != 2 {
		t.Fatalf("want the 2 parseable windows, got %d", len(got))
	}
	for _, w := range got {
		if w.Start.IsZero() || w.End.IsZero() {
			t.Errorf("a zero-valued window escaped: %+v", w)
		}
	}
}

func TestPeriodsToWindowsEmpty(t *testing.T) {
	if got := periodsToWindows(nil); len(got) != 0 {
		t.Errorf("nil periods should yield nothing, got %d", len(got))
	}
}
