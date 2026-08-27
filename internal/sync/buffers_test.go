package sync

import (
	"testing"
	"time"

	gcal "google.golang.org/api/calendar/v3"

	"github.com/ryakel/skulid/internal/db"
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

func TestParseEvStart(t *testing.T) {
	if _, ok := parseEvStart(nil); ok {
		t.Error("nil event should not parse")
	}
	if _, ok := parseEvStart(&gcal.Event{Start: &gcal.EventDateTime{Date: "2026-04-27"}}); ok {
		t.Error("all-day event should not parse (no DateTime)")
	}
	start, ok := parseEvStart(&gcal.Event{Start: &gcal.EventDateTime{DateTime: "2026-04-27T09:30:00Z"}})
	if !ok || start.Hour() != 9 || start.Minute() != 30 {
		t.Errorf("expected 09:30, got ok=%v start=%v", ok, start)
	}
}

func TestIsVirtualLocation(t *testing.T) {
	virtual := []string{
		"https://meet.google.com/abc-defg-hij",
		"https://acme.zoom.us/j/123456",
		"Microsoft Teams Meeting — https://teams.microsoft.com/l/x",
		"MEET.GOOGLE.COM/xyz",
		"https://whereby.com/standup",
	}
	for _, l := range virtual {
		if !isVirtualLocation(l) {
			t.Errorf("%q should be virtual", l)
		}
	}
	physical := []string{
		"1600 Amphitheatre Pkwy, Mountain View",
		"Room 201",
		"The coffee place on 5th",
		"",
	}
	for _, l := range physical {
		if isVirtualLocation(l) {
			t.Errorf("%q should not be virtual", l)
		}
	}
}

func TestIsTravelWorthy(t *testing.T) {
	at := func(loc string) *gcal.Event {
		return &gcal.Event{
			Id:       "e1",
			Start:    &gcal.EventDateTime{DateTime: "2026-04-27T10:00:00Z"},
			End:      &gcal.EventDateTime{DateTime: "2026-04-27T11:00:00Z"},
			Location: loc,
		}
	}
	cases := []struct {
		name string
		ev   *gcal.Event
		want bool
	}{
		{"nil", nil, false},
		{"no location", at(""), false},
		{"whitespace location", at("   "), false},
		{"physical location", at("Room 201"), true},
		// A solo event still earns travel: you have to get there whether or
		// not anyone else is on the invite.
		{"video call location", at("https://meet.google.com/abc"), false},
		{"cancelled", func() *gcal.Event { e := at("Room 201"); e.Status = "cancelled"; return e }(), false},
		{"transparent", func() *gcal.Event { e := at("Room 201"); e.Transparency = "transparent"; return e }(), false},
		{"all-day", &gcal.Event{Start: &gcal.EventDateTime{Date: "2026-04-27"}, Location: "Room 201"}, false},
		{"managed", func() *gcal.Event {
			e := at("Room 201")
			e.ExtendedProperties = &gcal.EventExtendedProperties{Private: map[string]string{"skulidManaged": "1"}}
			return e
		}(), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isTravelWorthy(tc.ev); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parsing %q: %v", s, err)
	}
	return v
}

// meeting builds a 10:00-11:00 event, optionally located and/or attended.
func meeting(location string, attendees int) *gcal.Event {
	ev := &gcal.Event{
		Id:       "src",
		Start:    &gcal.EventDateTime{DateTime: "2026-04-27T10:00:00Z"},
		End:      &gcal.EventDateTime{DateTime: "2026-04-27T11:00:00Z"},
		Location: location,
	}
	for i := 0; i < attendees; i++ {
		ev.Attendees = append(ev.Attendees, &gcal.EventAttendee{Email: "a@x.com"})
	}
	return ev
}

func TestPlanBuffersNothingWhenAllZero(t *testing.T) {
	got := planBuffers(meeting("Room 201", 2), db.BufferSettings{})
	if len(got) != 0 {
		t.Fatalf("zero minutes should plan nothing, got %d", len(got))
	}
}

func TestPlanBuffersTravelOnly(t *testing.T) {
	got := planBuffers(meeting("Room 201", 0), db.BufferSettings{TravelMinutes: 20})
	if len(got) != 2 {
		t.Fatalf("want travel before + after, got %d", len(got))
	}
	if got[0].Key.Placement != db.PlacementBefore || got[1].Key.Placement != db.PlacementAfter {
		t.Fatalf("want before then after, got %q then %q", got[0].Key.Placement, got[1].Key.Placement)
	}
	if !got[0].Start.Equal(mustTime(t, "2026-04-27T09:40:00Z")) || !got[0].End.Equal(mustTime(t, "2026-04-27T10:00:00Z")) {
		t.Errorf("travel-before = %v..%v, want 09:40..10:00", got[0].Start, got[0].End)
	}
	if !got[1].Start.Equal(mustTime(t, "2026-04-27T11:00:00Z")) || !got[1].End.Equal(mustTime(t, "2026-04-27T11:20:00Z")) {
		t.Errorf("travel-after = %v..%v, want 11:00..11:20", got[1].Start, got[1].End)
	}
	for _, p := range got {
		if p.Key.BufferType != db.BufferTravel || p.Summary != "Travel" || p.Key.SourceEventID != "src" {
			t.Errorf("unexpected buffer %+v", p)
		}
	}
}

func TestPlanBuffersNoTravelWithoutLocation(t *testing.T) {
	if got := planBuffers(meeting("", 2), db.BufferSettings{TravelMinutes: 20}); len(got) != 0 {
		t.Fatalf("an unlocated meeting earns no travel, got %d", len(got))
	}
}

func TestPlanBuffersDecompressionSitsBeforeTravelBack(t *testing.T) {
	// You decompress at the meeting, then drive home — so the trailing travel
	// block starts where decompression ends, not where the meeting ends.
	got := planBuffers(meeting("Room 201", 2), db.BufferSettings{
		DecompressionMinutes: 15,
		TravelMinutes:        20,
	})
	if len(got) != 3 {
		t.Fatalf("want travel + decompress + travel, got %d", len(got))
	}
	if got[1].Key.BufferType != db.BufferDecompression {
		t.Fatalf("decompression should be the middle buffer, got %q", got[1].Key.BufferType)
	}
	if !got[1].Start.Equal(mustTime(t, "2026-04-27T11:00:00Z")) || !got[1].End.Equal(mustTime(t, "2026-04-27T11:15:00Z")) {
		t.Errorf("decompress = %v..%v, want 11:00..11:15", got[1].Start, got[1].End)
	}
	if !got[2].Start.Equal(mustTime(t, "2026-04-27T11:15:00Z")) || !got[2].End.Equal(mustTime(t, "2026-04-27T11:35:00Z")) {
		t.Errorf("travel-after = %v..%v, want 11:15..11:35", got[2].Start, got[2].End)
	}
	// Nothing overlaps, and nothing has a gap.
	for i := 1; i < len(got); i++ {
		if got[i].Start.Before(got[i-1].End) {
			t.Errorf("buffer %d starts before %d ends", i, i-1)
		}
	}
}

func TestPlanBuffersKeysAreDistinct(t *testing.T) {
	// The reconciler maps on Key; two travel blocks for one meeting must not
	// collide, or the second would overwrite the first every recompute.
	got := planBuffers(meeting("Room 201", 2), db.BufferSettings{DecompressionMinutes: 15, TravelMinutes: 20})
	seen := map[db.BufferKey]bool{}
	for _, p := range got {
		if seen[p.Key] {
			t.Fatalf("duplicate key %+v", p.Key)
		}
		seen[p.Key] = true
	}
}

func TestPlanBuffersSkipsManagedEvents(t *testing.T) {
	// Padding our own buffer would compound on every recompute.
	ev := meeting("Room 201", 2)
	ev.ExtendedProperties = &gcal.EventExtendedProperties{
		Private: map[string]string{"skulidManaged": "1", "skulidBufferType": "travel"},
	}
	if got := planBuffers(ev, db.BufferSettings{DecompressionMinutes: 15, TravelMinutes: 20}); len(got) != 0 {
		t.Fatalf("managed events earn no buffers, got %d", len(got))
	}
}

func TestPlanBuffersUnparseableTimes(t *testing.T) {
	ev := meeting("Room 201", 2)
	ev.End = &gcal.EventDateTime{DateTime: "garbage"}
	if got := planBuffers(ev, db.BufferSettings{TravelMinutes: 20}); len(got) != 0 {
		t.Fatalf("an unparseable event earns no buffers, got %d", len(got))
	}
}
