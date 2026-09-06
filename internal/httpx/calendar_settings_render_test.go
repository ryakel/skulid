package httpx

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/ryakel/skulid/internal/db"
	"github.com/ryakel/skulid/internal/hours"
)

func testCalendar() *db.Calendar {
	return &db.Calendar{
		ID:               7,
		AccountID:        3,
		GoogleCalendarID: "work@example.com",
		Summary:          "Work",
		TimeZone:         "America/Chicago",
		Enabled:          true,
	}
}

func testAccount() *db.Account {
	return &db.Account{
		ID:    3,
		Email: "me@example.com",
		WorkingHours: json.RawMessage(
			`{"time_zone":"America/New_York","days":{"mon":["09:00-17:00"],"tue":["09:00-17:00"]}}`),
	}
}

func renderCalendarSettingsPage(t *testing.T, form calendarSettingsForm) string {
	t.Helper()
	r, err := NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}
	var buf bytes.Buffer
	err = r.Render(&buf, "calendar_settings", map[string]any{
		"Title":    "Work",
		"Features": map[string]bool{"Assistant": false},
		"Version":  "test",
		"Days":     weekDays,
		"Form":     form,
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	return buf.String()
}

// The page is built out of shared partials invoked with `dict`, and a template
// executed with the wrong argument shape only fails at request time. Render it
// for real so a broken invocation is a failing test, not a 500 in the browser.
func TestCalendarSettingsPageRenders(t *testing.T) {
	cal := testCalendar()
	acct := testAccount()
	form := calendarSettingsForm{
		Calendar:      *cal,
		AccountEmail:  acct.Email,
		Categories:    []db.Category{{ID: 4, Name: "Focus"}},
		CategoryID:    "4",
		InheritedTZ:   db.CalendarZone(cal, acct),
		Zones:         hours.ZoneGroupsWith(""),
		Columns:       calendarHoursColumns(cal, acct),
		GlobalBuffers: db.BufferSettings{TaskHabitBreakMinutes: 10},
		Buffers:       db.BufferSettings{TaskHabitBreakMinutes: 10},
	}

	out := renderCalendarSettingsPage(t, form)

	for _, want := range []string{
		`name="working_mon"`,
		`name="personal_sun"`,
		`name="meeting_fri"`,
		`data-hours-fill="working"`,
		`data-fill-apply="weekdays"`,
		`name="tz"`,
		`<optgroup label="Americas">`,
		"America/Chicago",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered page missing %q", want)
		}
	}

	// With no explicit override the picker must sit on "inherit", not on a
	// zone the owner never chose.
	if !strings.Contains(out, `<option value="" selected>Use the calendar&#39;s own zone`) {
		t.Error("the inherit option should be selected when no zone is pinned")
	}
}

// A blank box means "inherit", so the form has to say what it would inherit.
// Showing nothing makes an inherited row indistinguishable from an unavailable
// one, which is the single most confusing thing about the old page.
func TestCalendarSettingsShowsInheritedHoursAsPlaceholders(t *testing.T) {
	cal := testCalendar()
	acct := testAccount()
	cols := calendarHoursColumns(cal, acct)

	if got := cols[0].Inherited["mon"]; got != "09:00-17:00" {
		t.Errorf("Working/mon inherits %q, want the account's 09:00-17:00", got)
	}
	if got := cols[0].Values["mon"]; got != "" {
		t.Errorf("Working/mon has value %q, want blank (nothing is overridden)", got)
	}

	// Personal falls back to this calendar's own Working before the account's.
	cal.WorkingHours = json.RawMessage(`{"days":{"mon":["10:00-16:00"]}}`)
	cols = calendarHoursColumns(cal, acct)
	if got := cols[1].Inherited["mon"]; got != "10:00-16:00" {
		t.Errorf("Personal/mon inherits %q, want the calendar's own Working 10:00-16:00", got)
	}
}

func TestHoursPageRenders(t *testing.T) {
	r, err := NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}
	var buf bytes.Buffer
	err = r.Render(&buf, "hours", map[string]any{
		"Title":    "Hours",
		"Features": map[string]bool{"Assistant": false},
		"Version":  "test",
		"Days":     weekDays,
		"Forms":    []hoursForm{hoursFormFor(*testAccount())},
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		`name="working_mon"`,
		`data-fill-apply="all"`,
		`<option value="America/New_York" selected>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered page missing %q", want)
		}
	}
}

func TestBlockEditPageRenders(t *testing.T) {
	r, err := NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}
	wh := hours.Default()
	var buf bytes.Buffer
	err = r.Render(&buf, "block_edit", map[string]any{
		"Title":     "Smart block",
		"Features":  map[string]bool{"Assistant": false},
		"Version":   "test",
		"Days":      weekDays,
		"Block":     &db.SmartBlock{Name: "Focus", HorizonDays: 30},
		"WH":        wh,
		"Zones":     hours.ZoneGroupsWith(wh.TimeZone),
		"Columns":   []hoursColumn{{Key: "wh", Title: "Available", Values: map[string]string{"mon": "09:00-17:00"}, Inherited: map[string]string{}}},
		"Calendars": []calendarOption{},
		"SourceSet": map[int64]bool{},
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	out := buf.String()
	for _, want := range []string{`name="wh_mon"`, `name="wh_tz"`, `data-hours-fill="wh"`} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered page missing %q", want)
		}
	}
	// A block's hours stand alone, so an empty day must not claim to inherit.
	if !strings.Contains(out, `placeholder="unavailable"`) {
		t.Error("empty days on a block should read as unavailable, not as inheriting")
	}
}

// ---------------------------------------------------------------------------
// Form parsing
// ---------------------------------------------------------------------------

func hoursPost(t *testing.T, values url.Values) *http.Request {
	t.Helper()
	req := httptest.NewRequest("POST", "/calendars/7", strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := req.ParseForm(); err != nil {
		t.Fatalf("ParseForm: %v", err)
	}
	return req
}

func TestBuildHoursFromFormNormalizes(t *testing.T) {
	req := hoursPost(t, url.Values{
		"working_mon": {"9:00-17:00"},
		"working_tue": {"09:00 – 12:00, 13:00-17:00"},
	})
	wh, err := buildHoursFromForm(req, "working", "America/Chicago")
	if err != nil {
		t.Fatalf("buildHoursFromForm: %v", err)
	}
	if got := wh.Days["mon"]; len(got) != 1 || got[0] != "09:00-17:00" {
		t.Errorf("mon = %v, want [09:00-17:00]", got)
	}
	if got := wh.Days["tue"]; len(got) != 2 || got[0] != "09:00-12:00" || got[1] != "13:00-17:00" {
		t.Errorf("tue = %v, want the en-dash range normalized and split", got)
	}
	if wh.Days["wed"] != nil {
		t.Errorf("wed = %v, want nil for an untouched day", wh.Days["wed"])
	}
}

// A range the form accepts but the scheduler cannot expand is worse than a
// rejection: the calendar silently has no availability and nothing says why.
func TestBuildHoursFromFormRejectsAMalformedRange(t *testing.T) {
	req := hoursPost(t, url.Values{"working_wed": {"9-5"}})
	_, err := buildHoursFromForm(req, "working", "UTC")
	if err == nil {
		t.Fatal("want an error for a malformed range")
	}
	for _, want := range []string{"Wednesday", "9-5", "HH:MM-HH:MM"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q", err, want)
		}
	}
}

func TestBuildHoursFromFormAllBlankMeansInherit(t *testing.T) {
	req := hoursPost(t, url.Values{"working_mon": {"  "}})
	wh, err := buildHoursFromForm(req, "working", "America/Chicago")
	if err != nil {
		t.Fatalf("buildHoursFromForm: %v", err)
	}
	if jsonOrNil(wh) != nil {
		t.Error("an all-blank column must persist as NULL so the calendar keeps inheriting")
	}
}

// Leaving the zone picker on "inherit" must store no zone at all. Storing one
// snapshots today's Google value and quietly stops tracking it.
func TestInheritedZoneIsNotStored(t *testing.T) {
	req := hoursPost(t, url.Values{"working_mon": {"09:00-17:00"}})
	wh, err := buildHoursFromForm(req, "working", "")
	if err != nil {
		t.Fatalf("buildHoursFromForm: %v", err)
	}
	raw := jsonOrNil(wh)
	if raw == nil {
		t.Fatal("hours were set, so something must be stored")
	}
	if strings.Contains(string(raw), `"time_zone":"America`) {
		t.Errorf("stored blob pinned a zone: %s", raw)
	}

	cal := testCalendar()
	cal.WorkingHours = raw
	if got := calendarZoneOverride(cal); got != "" {
		t.Errorf("calendarZoneOverride = %q, want empty so the picker shows inherit", got)
	}
}

func TestClampMinutes(t *testing.T) {
	cases := map[int64]int{-5: 0, 0: 0, 30: 30, 240: 240, 1000: 240}
	for in, want := range cases {
		if got := clampMinutes(in); got != want {
			t.Errorf("clampMinutes(%d) = %d, want %d", in, got, want)
		}
	}
}
