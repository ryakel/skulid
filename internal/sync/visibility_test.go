package sync

import (
	"testing"

	gcal "google.golang.org/api/calendar/v3"
)

func TestTransformForModeBusyForAllIsTheDefault(t *testing.T) {
	for _, mode := range []string{"", "busy_for_all", "garbage_unknown"} {
		got := TransformForMode(mode)
		if got.TitleTemplate != "Busy" || !got.MarkBusy ||
			!got.StripAttendees || !got.StripDescription || got.Visibility != "private" {
			t.Errorf("mode %q: expected the safe Busy preset, got %+v", mode, got)
		}
	}
}

func TestTransformForModePersonalCommitment(t *testing.T) {
	got := TransformForMode(VisibilityPersonalCommitment)
	if got.TitleTemplate != "Personal Commitment" {
		t.Errorf("title: %q", got.TitleTemplate)
	}
	if !got.MarkBusy || !got.StripAttendees || !got.StripDescription {
		t.Errorf("expected everything stripped/busied: %+v", got)
	}
	if got.Visibility != "private" {
		t.Errorf("visibility: %q", got.Visibility)
	}
}

func TestTransformForModeDetailsForYouOthers(t *testing.T) {
	got := TransformForMode(VisibilityDetailsForYouOthers)
	if got.TitleTemplate != "" {
		t.Errorf("title should preserve source, got %q", got.TitleTemplate)
	}
	if !got.MarkBusy {
		t.Error("expected busy")
	}
	if !got.StripAttendees {
		t.Error("expected attendees stripped (others shouldn't see who you meet with)")
	}
	if got.StripDescription {
		t.Error("expected description preserved (you see it on your end)")
	}
	if got.Visibility != "private" {
		t.Errorf("visibility: %q want private", got.Visibility)
	}
}

func TestTransformForModeDetailsForYouAndAccess(t *testing.T) {
	got := TransformForMode(VisibilityDetailsForYouAccess)
	if got.TitleTemplate != "" || got.StripAttendees || got.StripDescription {
		t.Errorf("expected no stripping; got %+v", got)
	}
	if !got.MarkBusy {
		t.Error("expected busy")
	}
	if got.Visibility != "default" {
		t.Errorf("visibility: %q want default", got.Visibility)
	}
}

func TestAllDayModeSkip(t *testing.T) {
	allDay := &gcal.Event{Start: &gcal.EventDateTime{Date: "2026-04-27"}}
	timed := &gcal.Event{Start: &gcal.EventDateTime{DateTime: "2026-04-27T09:00:00Z"}}
	if allowedByAllDayMode("skip", allDay) {
		t.Error("skip should reject all-day events")
	}
	if !allowedByAllDayMode("skip", timed) {
		t.Error("skip should allow timed events")
	}
}

func TestAllDayModeOnlyBusy(t *testing.T) {
	allDayBusy := &gcal.Event{
		Start: &gcal.EventDateTime{Date: "2026-04-27"},
	}
	allDayTransparent := &gcal.Event{
		Start:        &gcal.EventDateTime{Date: "2026-04-27"},
		Transparency: "transparent",
	}
	if !allowedByAllDayMode("only_busy", allDayBusy) {
		t.Error("only_busy should accept opaque all-day")
	}
	if allowedByAllDayMode("only_busy", allDayTransparent) {
		t.Error("only_busy should reject transparent all-day")
	}
}

func TestAllDayModeSyncAllAndDefault(t *testing.T) {
	allDay := &gcal.Event{Start: &gcal.EventDateTime{Date: "2026-04-27"}}
	for _, mode := range []string{"sync_all", "", "anything-else"} {
		if !allowedByAllDayMode(mode, allDay) {
			t.Errorf("mode %q should pass all-day events through", mode)
		}
	}
}

func TestIsAllDayEvent(t *testing.T) {
	cases := []struct {
		name string
		ev   *gcal.Event
		want bool
	}{
		{"date only", &gcal.Event{Start: &gcal.EventDateTime{Date: "2026-04-27"}}, true},
		{"datetime only", &gcal.Event{Start: &gcal.EventDateTime{DateTime: "2026-04-27T09:00:00Z"}}, false},
		{"nil start", &gcal.Event{}, false},
		{"both blank", &gcal.Event{Start: &gcal.EventDateTime{}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isAllDayEvent(tc.ev); got != tc.want {
				t.Errorf("got %v want %v", got, tc.want)
			}
		})
	}
}
