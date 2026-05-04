package sync

import (
	"testing"

	gcal "google.golang.org/api/calendar/v3"
)

func TestFilterMatchEmptyAcceptsConfirmed(t *testing.T) {
	var f Filter
	ev := &gcal.Event{Summary: "anything", Status: "confirmed"}
	if !f.Match(ev) {
		t.Fatal("empty filter should accept any non-cancelled event")
	}
}

func TestFilterRejectsCancelled(t *testing.T) {
	var f Filter
	if f.Match(&gcal.Event{Status: "cancelled", Summary: "anything"}) {
		t.Fatal("cancelled events must always be filtered out")
	}
}

func TestFilterTitleRegex(t *testing.T) {
	f := Filter{TitleRegex: "^Standup"}
	if !f.Match(&gcal.Event{Summary: "Standup Mon"}) {
		t.Fatal("expected Standup Mon to match")
	}
	if f.Match(&gcal.Event{Summary: "Sprint planning"}) {
		t.Fatal("expected Sprint planning to be rejected")
	}
}

func TestFilterTitleRegexInvalidIsRejected(t *testing.T) {
	f := Filter{TitleRegex: "[unterminated"}
	if f.Match(&gcal.Event{Summary: "anything"}) {
		t.Fatal("invalid regex should drop the event, not match it")
	}
}

func TestFilterColorIDs(t *testing.T) {
	f := Filter{ColorIDs: []string{"3", "4"}}
	if !f.Match(&gcal.Event{ColorId: "3"}) {
		t.Fatal("ColorId 3 should match")
	}
	if f.Match(&gcal.Event{ColorId: "5"}) {
		t.Fatal("ColorId 5 should not match")
	}
}

func TestFilterAttendeeAny(t *testing.T) {
	f := Filter{AttendeeAny: []string{"boss@x.com", "vip@y.com"}}
	hit := &gcal.Event{Attendees: []*gcal.EventAttendee{
		{Email: "Boss@X.com"}, {Email: "alice@x.com"},
	}}
	if !f.Match(hit) {
		t.Fatal("expected case-insensitive attendee match")
	}
	miss := &gcal.Event{Attendees: []*gcal.EventAttendee{{Email: "alice@x.com"}}}
	if f.Match(miss) {
		t.Fatal("expected no attendee match")
	}
	none := &gcal.Event{}
	if f.Match(none) {
		t.Fatal("expected event without attendees to be rejected")
	}
}

func TestFilterFreeBusy(t *testing.T) {
	busy := &gcal.Event{Transparency: ""} // default opaque == busy
	free := &gcal.Event{Transparency: "transparent"}

	if got := (Filter{FreeBusy: "busy"}).Match(busy); !got {
		t.Fatal("busy filter should accept opaque event")
	}
	if got := (Filter{FreeBusy: "busy"}).Match(free); got {
		t.Fatal("busy filter should reject transparent event")
	}
	if got := (Filter{FreeBusy: "free"}).Match(free); !got {
		t.Fatal("free filter should accept transparent event")
	}
	if got := (Filter{FreeBusy: "free"}).Match(busy); got {
		t.Fatal("free filter should reject opaque event")
	}
}

func TestFilterAllDay(t *testing.T) {
	allDay := &gcal.Event{Start: &gcal.EventDateTime{Date: "2026-04-27"}}
	timed := &gcal.Event{Start: &gcal.EventDateTime{DateTime: "2026-04-27T09:00:00-05:00"}}

	if got := (Filter{AllDay: "only"}).Match(allDay); !got {
		t.Fatal("only filter should accept all-day event")
	}
	if got := (Filter{AllDay: "only"}).Match(timed); got {
		t.Fatal("only filter should reject timed event")
	}
	if got := (Filter{AllDay: "exclude"}).Match(allDay); got {
		t.Fatal("exclude filter should reject all-day event")
	}
	if got := (Filter{AllDay: "exclude"}).Match(timed); !got {
		t.Fatal("exclude filter should accept timed event")
	}
}

func TestFilterStartHourBounds(t *testing.T) {
	morning := &gcal.Event{Start: &gcal.EventDateTime{DateTime: "2026-04-27T08:30:00-05:00"}}
	noon := &gcal.Event{Start: &gcal.EventDateTime{DateTime: "2026-04-27T12:30:00-05:00"}}
	evening := &gcal.Event{Start: &gcal.EventDateTime{DateTime: "2026-04-27T19:30:00-05:00"}}

	f := Filter{StartHour: 9, EndHour: 17}
	if f.Match(morning) {
		t.Fatal("8:30 should be before StartHour=9")
	}
	if !f.Match(noon) {
		t.Fatal("12:30 should be inside [9,17)")
	}
	if f.Match(evening) {
		t.Fatal("19:30 should be after EndHour=17")
	}
}

func TestFilterStartHourRejectsAllDay(t *testing.T) {
	allDay := &gcal.Event{Start: &gcal.EventDateTime{Date: "2026-04-27"}}
	f := Filter{StartHour: 9, EndHour: 17}
	if f.Match(allDay) {
		t.Fatal("hour-bounded filter should reject all-day events (no DateTime)")
	}
}

func TestParseFilterRoundTrip(t *testing.T) {
	raw := []byte(`{"title_regex":"^x","color_ids":["1","2"],"start_hour":9,"end_hour":17}`)
	f, err := ParseFilter(raw)
	if err != nil {
		t.Fatalf("ParseFilter: %v", err)
	}
	if f.TitleRegex != "^x" || len(f.ColorIDs) != 2 || f.StartHour != 9 || f.EndHour != 17 {
		t.Fatalf("unexpected filter: %+v", f)
	}
}

func TestParseFilterEmpty(t *testing.T) {
	f, err := ParseFilter(nil)
	if err != nil {
		t.Fatal(err)
	}
	if f.TitleRegex != "" || len(f.ColorIDs) != 0 {
		t.Fatalf("expected zero-value filter, got %+v", f)
	}
}
