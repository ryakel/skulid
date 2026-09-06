package db

import (
	"encoding/json"
	"testing"
)

func zoneOfBlob(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var probe struct {
		TimeZone string `json:"time_zone"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatalf("unmarshal %s: %v", raw, err)
	}
	return probe.TimeZone
}

// Hours saved without a zone follow the calendar. Snapshotting Google's value
// at save time would leave the availability behind when the calendar moves.
func TestEffectiveCalendarHoursInheritsTheGoogleZone(t *testing.T) {
	cal := &Calendar{
		TimeZone:     "America/Chicago",
		WorkingHours: json.RawMessage(`{"time_zone":"","days":{"mon":["09:00-17:00"]}}`),
	}
	acct := &Account{
		WorkingHours: json.RawMessage(`{"time_zone":"Europe/London","days":{"mon":["08:00-16:00"]}}`),
	}

	got := EffectiveCalendarHours(cal, acct, HoursWorking)
	if z := zoneOfBlob(t, got); z != "America/Chicago" {
		t.Errorf("zone = %q, want the calendar's own America/Chicago", z)
	}

	// The days must survive the zone being stamped on.
	var wh struct {
		Days map[string][]string `json:"days"`
	}
	if err := json.Unmarshal(got, &wh); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(wh.Days["mon"]) != 1 || wh.Days["mon"][0] != "09:00-17:00" {
		t.Errorf("days = %v, want the calendar's own hours preserved", wh.Days)
	}
}

func TestEffectiveCalendarHoursKeepsAnExplicitZone(t *testing.T) {
	cal := &Calendar{
		TimeZone:     "America/Chicago",
		WorkingHours: json.RawMessage(`{"time_zone":"Asia/Tokyo","days":{"mon":["09:00-17:00"]}}`),
	}
	if z := zoneOfBlob(t, EffectiveCalendarHours(cal, nil, HoursWorking)); z != "Asia/Tokyo" {
		t.Errorf("zone = %q, want the pinned Asia/Tokyo — an override must win", z)
	}
}

// When the account's hours are the ones in play, they keep the account's zone.
// Reading the account's 09:00 in the calendar's zone would silently shift
// everything for a calendar that lives somewhere else.
func TestEffectiveCalendarHoursFallsBackWithTheAccountZone(t *testing.T) {
	cal := &Calendar{TimeZone: "America/Chicago"}
	acct := &Account{
		WorkingHours: json.RawMessage(`{"time_zone":"Europe/London","days":{"mon":["08:00-16:00"]}}`),
	}
	if z := zoneOfBlob(t, EffectiveCalendarHours(cal, acct, HoursWorking)); z != "Europe/London" {
		t.Errorf("zone = %q, want the account's Europe/London", z)
	}
}

func TestCalendarZonePrefersGoogleThenAccountThenUTC(t *testing.T) {
	acct := &Account{WorkingHours: json.RawMessage(`{"time_zone":"Europe/London","days":{}}`)}

	if got := CalendarZone(&Calendar{TimeZone: "America/Chicago"}, acct); got != "America/Chicago" {
		t.Errorf("got %q, want America/Chicago", got)
	}
	if got := CalendarZone(&Calendar{}, acct); got != "Europe/London" {
		t.Errorf("got %q, want Europe/London", got)
	}
	if got := CalendarZone(&Calendar{}, &Account{}); got != "UTC" {
		t.Errorf("got %q, want UTC", got)
	}
	if got := CalendarZone(nil, nil); got != "UTC" {
		t.Errorf("got %q, want UTC", got)
	}
}
