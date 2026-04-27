package sync

import (
	"testing"

	gcal "google.golang.org/api/calendar/v3"
)

func TestIsDecompressibleMeeting(t *testing.T) {
	twoAttendees := []*gcal.EventAttendee{{Email: "a@x.com"}, {Email: "b@y.com"}}
	cases := []struct {
		name string
		ev   *gcal.Event
		want bool
	}{
		{"nil event", nil, false},
		{"cancelled", &gcal.Event{Status: "cancelled", Start: &gcal.EventDateTime{DateTime: "2026-04-27T10:00:00Z"}, Attendees: twoAttendees}, false},
		{"all-day event", &gcal.Event{Start: &gcal.EventDateTime{Date: "2026-04-27"}, Attendees: twoAttendees}, false},
		{"transparent", &gcal.Event{Start: &gcal.EventDateTime{DateTime: "2026-04-27T10:00:00Z"}, Transparency: "transparent", Attendees: twoAttendees}, false},
		{"solo block", &gcal.Event{Start: &gcal.EventDateTime{DateTime: "2026-04-27T10:00:00Z"}}, false},
		{"one attendee", &gcal.Event{
			Start:     &gcal.EventDateTime{DateTime: "2026-04-27T10:00:00Z"},
			Attendees: []*gcal.EventAttendee{{Email: "self@x.com"}},
		}, false},
		{"resource-only attendee", &gcal.Event{
			Start: &gcal.EventDateTime{DateTime: "2026-04-27T10:00:00Z"},
			Attendees: []*gcal.EventAttendee{
				{Email: "self@x.com"},
				{Email: "room-201@x.com", Resource: true},
			},
		}, false},
		{"two human attendees", &gcal.Event{
			Start:     &gcal.EventDateTime{DateTime: "2026-04-27T10:00:00Z"},
			Attendees: twoAttendees,
		}, true},
		{"managed event", &gcal.Event{
			Start:     &gcal.EventDateTime{DateTime: "2026-04-27T10:00:00Z"},
			Attendees: twoAttendees,
			ExtendedProperties: &gcal.EventExtendedProperties{
				Private: map[string]string{"skulidManaged": "1"},
			},
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isDecompressibleMeeting(tc.ev); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestParseEvEnd(t *testing.T) {
	if _, ok := parseEvEnd(nil); ok {
		t.Error("nil event should not parse")
	}
	if _, ok := parseEvEnd(&gcal.Event{}); ok {
		t.Error("empty event should not parse")
	}
	if _, ok := parseEvEnd(&gcal.Event{End: &gcal.EventDateTime{Date: "2026-04-27"}}); ok {
		t.Error("all-day event should not parse (no DateTime)")
	}
	if _, ok := parseEvEnd(&gcal.Event{End: &gcal.EventDateTime{DateTime: "garbage"}}); ok {
		t.Error("garbage timestamp should not parse")
	}
	end, ok := parseEvEnd(&gcal.Event{End: &gcal.EventDateTime{DateTime: "2026-04-27T11:00:00Z"}})
	if !ok || end.Hour() != 11 {
		t.Errorf("expected 11:00, got ok=%v end=%v", ok, end)
	}
}
