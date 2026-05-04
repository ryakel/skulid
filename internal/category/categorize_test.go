package category

import (
	"reflect"
	"testing"

	gcal "google.golang.org/api/calendar/v3"

	"github.com/ryakel/skulid/internal/db"
)

func TestClassifyNilEvent(t *testing.T) {
	if got := Classify(nil, Context{}); got != db.CategoryOther {
		t.Fatalf("nil event: got %q", got)
	}
}

func TestClassifyCancelledEvent(t *testing.T) {
	ev := &gcal.Event{Status: "cancelled", Summary: "Anything"}
	if got := Classify(ev, Context{}); got != db.CategoryOther {
		t.Fatalf("cancelled: got %q", got)
	}
}

func TestClassifyTransparentEventIsFree(t *testing.T) {
	ev := &gcal.Event{Transparency: "transparent", Summary: "Quick chat",
		Start: &gcal.EventDateTime{DateTime: "2026-04-27T09:00:00Z"},
		End:   &gcal.EventDateTime{DateTime: "2026-04-27T09:30:00Z"},
	}
	if got := Classify(ev, Context{}); got != db.CategoryFree {
		t.Fatalf("transparent: got %q want %q", got, db.CategoryFree)
	}
}

func TestClassifyTransparentRespectsCalendarDefault(t *testing.T) {
	ev := &gcal.Event{Transparency: "transparent", Summary: "Holiday"}
	got := Classify(ev, Context{CalendarDefaultSlug: db.CategoryPersonal})
	if got != db.CategoryPersonal {
		t.Fatalf("calendar default should win over Free: got %q", got)
	}
}

func TestClassifyTitleKeywordsHitFocusAndTravel(t *testing.T) {
	cases := map[string]string{
		"Lunch":              db.CategoryTravel,
		"LUNCH with Bob":     db.CategoryTravel,
		"Decompress":         db.CategoryTravel,
		"Commute home":       db.CategoryTravel,
		"Focus block":        db.CategoryFocus,
		"Deep Work session":  db.CategoryFocus,
		"Heads down — code":  db.CategoryFocus,
	}
	for title, want := range cases {
		ev := &gcal.Event{
			Summary: title,
			Start:   &gcal.EventDateTime{DateTime: "2026-04-27T12:00:00Z"},
			End:     &gcal.EventDateTime{DateTime: "2026-04-27T13:00:00Z"},
			Attendees: []*gcal.EventAttendee{ // 3 attendees would normally be Team
				{Email: "a@x.com"}, {Email: "b@x.com"}, {Email: "c@x.com"},
			},
		}
		if got := Classify(ev, Context{OwnerDomains: map[string]bool{"x.com": true}}); got != want {
			t.Errorf("%q: got %q want %q", title, got, want)
		}
	}
}

func TestClassifyAllDayEventIsPersonal(t *testing.T) {
	ev := &gcal.Event{
		Summary: "Birthday",
		Start:   &gcal.EventDateTime{Date: "2026-04-27"},
		End:     &gcal.EventDateTime{Date: "2026-04-28"},
	}
	if got := Classify(ev, Context{}); got != db.CategoryPersonal {
		t.Fatalf("all-day: got %q want %q", got, db.CategoryPersonal)
	}
}

func TestClassifyAllDayRespectsCalendarDefault(t *testing.T) {
	ev := &gcal.Event{
		Summary: "Sprint",
		Start:   &gcal.EventDateTime{Date: "2026-04-27"},
		End:     &gcal.EventDateTime{Date: "2026-04-28"},
	}
	got := Classify(ev, Context{CalendarDefaultSlug: db.CategoryTeam})
	if got != db.CategoryTeam {
		t.Fatalf("calendar default should win over Personal: got %q", got)
	}
}

func TestClassifySoloTimedEventIsFocus(t *testing.T) {
	ev := &gcal.Event{
		Summary: "Plan the week",
		Start:   &gcal.EventDateTime{DateTime: "2026-04-27T10:00:00Z"},
		End:     &gcal.EventDateTime{DateTime: "2026-04-27T11:00:00Z"},
	}
	if got := Classify(ev, Context{}); got != db.CategoryFocus {
		t.Fatalf("solo: got %q", got)
	}
}

func TestClassifyTwoAttendeesIsOneOnOne(t *testing.T) {
	ev := &gcal.Event{
		Summary: "Sync with Alice",
		Start:   &gcal.EventDateTime{DateTime: "2026-04-27T10:00:00Z"},
		End:     &gcal.EventDateTime{DateTime: "2026-04-27T10:30:00Z"},
		Attendees: []*gcal.EventAttendee{
			{Email: "me@x.com"}, {Email: "alice@x.com"},
		},
	}
	if got := Classify(ev, Context{OwnerDomains: map[string]bool{"x.com": true}}); got != db.CategoryOneOnOne {
		t.Fatalf("1:1: got %q", got)
	}
}

func TestClassifyTeamMeetingAllInternal(t *testing.T) {
	ev := &gcal.Event{
		Summary: "Standup",
		Start:   &gcal.EventDateTime{DateTime: "2026-04-27T10:00:00Z"},
		End:     &gcal.EventDateTime{DateTime: "2026-04-27T10:30:00Z"},
		Attendees: []*gcal.EventAttendee{
			{Email: "me@x.com"}, {Email: "alice@x.com"}, {Email: "bob@x.com"},
		},
	}
	got := Classify(ev, Context{OwnerDomains: map[string]bool{"x.com": true}})
	if got != db.CategoryTeam {
		t.Fatalf("team: got %q", got)
	}
}

func TestClassifyExternalMeetingHasNonInternalAttendee(t *testing.T) {
	ev := &gcal.Event{
		Summary: "Customer call",
		Start:   &gcal.EventDateTime{DateTime: "2026-04-27T10:00:00Z"},
		End:     &gcal.EventDateTime{DateTime: "2026-04-27T10:30:00Z"},
		Attendees: []*gcal.EventAttendee{
			{Email: "me@x.com"}, {Email: "alice@x.com"}, {Email: "cust@y.com"},
		},
	}
	got := Classify(ev, Context{OwnerDomains: map[string]bool{"x.com": true}})
	if got != db.CategoryExternal {
		t.Fatalf("external: got %q", got)
	}
}

func TestClassifyEmptyOwnerDomainsCollapsesToTeam(t *testing.T) {
	ev := &gcal.Event{
		Summary: "Meeting",
		Start:   &gcal.EventDateTime{DateTime: "2026-04-27T10:00:00Z"},
		End:     &gcal.EventDateTime{DateTime: "2026-04-27T10:30:00Z"},
		Attendees: []*gcal.EventAttendee{
			{Email: "a@x.com"}, {Email: "b@y.com"}, {Email: "c@z.com"},
		},
	}
	if got := Classify(ev, Context{}); got != db.CategoryTeam {
		t.Fatalf("with no owner domains, expected Team (conservative): got %q", got)
	}
}

func TestClassifyResourceAttendeesIgnored(t *testing.T) {
	ev := &gcal.Event{
		Summary: "Sync with Alice",
		Start:   &gcal.EventDateTime{DateTime: "2026-04-27T10:00:00Z"},
		End:     &gcal.EventDateTime{DateTime: "2026-04-27T10:30:00Z"},
		Attendees: []*gcal.EventAttendee{
			{Email: "me@x.com"},
			{Email: "alice@x.com"},
			{Email: "room-A@resource.calendar.google.com", Resource: true},
		},
	}
	got := Classify(ev, Context{OwnerDomains: map[string]bool{"x.com": true}})
	if got != db.CategoryOneOnOne {
		t.Fatalf("resource attendees should not bump 1:1 -> Team, got %q", got)
	}
}

func TestDomainsFromEmails(t *testing.T) {
	got := DomainsFromEmails([]string{"alice@Example.com", "bob@example.com", "carol@other.io", "no-domain"})
	want := map[string]bool{"example.com": true, "other.io": true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}
