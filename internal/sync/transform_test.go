package sync

import (
	"testing"

	gcal "google.golang.org/api/calendar/v3"
)

func sourceEvent() *gcal.Event {
	return &gcal.Event{
		Summary:      "Standup",
		Location:     "Zoom",
		Description:  "agenda...",
		Transparency: "transparent",
		Visibility:   "public",
		Attendees: []*gcal.EventAttendee{
			{Email: "alice@example.com"},
			{Email: "bob@example.com"},
		},
		Start: &gcal.EventDateTime{DateTime: "2026-04-27T09:00:00-05:00"},
		End:   &gcal.EventDateTime{DateTime: "2026-04-27T09:30:00-05:00"},
	}
}

func TestTransformDefaultPreservesSummary(t *testing.T) {
	out := Transform{}.Apply(sourceEvent())
	if out.Summary != "Standup" {
		t.Fatalf("expected Standup, got %q", out.Summary)
	}
}

func TestTransformEmptySummaryFallsBackToBusy(t *testing.T) {
	src := &gcal.Event{}
	out := Transform{}.Apply(src)
	if out.Summary != "Busy" {
		t.Fatalf("empty source summary should default to Busy, got %q", out.Summary)
	}
}

func TestTransformTitleTemplateSubstitutes(t *testing.T) {
	tr := Transform{TitleTemplate: "[{title}] @ {location}"}
	out := tr.Apply(sourceEvent())
	if out.Summary != "[Standup] @ Zoom" {
		t.Fatalf("unexpected title: %q", out.Summary)
	}
}

func TestTransformMarkBusyOverridesTransparency(t *testing.T) {
	tr := Transform{MarkBusy: true}
	out := tr.Apply(sourceEvent())
	if out.Transparency != "opaque" {
		t.Fatalf("expected opaque, got %q", out.Transparency)
	}
}

func TestTransformStripAttendees(t *testing.T) {
	tr := Transform{StripAttendees: true}
	out := tr.Apply(sourceEvent())
	if out.Attendees != nil {
		t.Fatalf("expected attendees nil, got %v", out.Attendees)
	}
}

func TestTransformPreservesAttendeesByDefault(t *testing.T) {
	out := Transform{}.Apply(sourceEvent())
	if len(out.Attendees) != 2 {
		t.Fatalf("expected 2 attendees, got %d", len(out.Attendees))
	}
}

func TestTransformStripDescription(t *testing.T) {
	tr := Transform{StripDescription: true}
	out := tr.Apply(sourceEvent())
	if out.Description != "" {
		t.Fatalf("expected empty description, got %q", out.Description)
	}
}

func TestTransformVisibilityOverride(t *testing.T) {
	tr := Transform{Visibility: "private"}
	out := tr.Apply(sourceEvent())
	if out.Visibility != "private" {
		t.Fatalf("expected private, got %q", out.Visibility)
	}
}

func TestTransformPreservesTimes(t *testing.T) {
	out := Transform{}.Apply(sourceEvent())
	if out.Start == nil || out.Start.DateTime != "2026-04-27T09:00:00-05:00" {
		t.Fatalf("Start not preserved: %+v", out.Start)
	}
	if out.End == nil || out.End.DateTime != "2026-04-27T09:30:00-05:00" {
		t.Fatalf("End not preserved: %+v", out.End)
	}
}

func TestParseTransformRoundTrip(t *testing.T) {
	raw := []byte(`{"title_template":"X","mark_busy":true,"strip_attendees":true}`)
	tr, err := ParseTransform(raw)
	if err != nil {
		t.Fatalf("ParseTransform: %v", err)
	}
	if tr.TitleTemplate != "X" || !tr.MarkBusy || !tr.StripAttendees {
		t.Fatalf("unexpected transform: %+v", tr)
	}
}
