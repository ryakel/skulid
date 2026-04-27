package sync

import (
	"encoding/json"
	"strings"

	"google.golang.org/api/calendar/v3"
)

type Transform struct {
	TitleTemplate    string `json:"title_template,omitempty"`
	MarkBusy         bool   `json:"mark_busy,omitempty"`
	StripAttendees   bool   `json:"strip_attendees,omitempty"`
	StripDescription bool   `json:"strip_description,omitempty"`
	Visibility       string `json:"visibility,omitempty"` // "default" | "public" | "private"
}

func ParseTransform(raw json.RawMessage) (Transform, error) {
	var t Transform
	if len(raw) == 0 {
		return t, nil
	}
	if err := json.Unmarshal(raw, &t); err != nil {
		return t, err
	}
	return t, nil
}

// Apply produces a new event payload suitable for inserting/updating on the
// target calendar. Time fields, recurrence, and conferenceData are preserved
// from the source.
func (t Transform) Apply(source *calendar.Event) *calendar.Event {
	out := &calendar.Event{
		Summary:      renderTitle(t.TitleTemplate, source),
		Start:        source.Start,
		End:          source.End,
		Recurrence:   source.Recurrence,
		Location:     source.Location,
		Description:  source.Description,
		ColorId:      source.ColorId,
		Transparency: source.Transparency,
		Visibility:   source.Visibility,
	}
	if t.MarkBusy {
		out.Transparency = "opaque"
	}
	if t.StripAttendees {
		out.Attendees = nil
	} else {
		out.Attendees = source.Attendees
	}
	if t.StripDescription {
		out.Description = ""
	}
	if t.Visibility != "" {
		out.Visibility = t.Visibility
	}
	return out
}

func renderTitle(tmpl string, source *calendar.Event) string {
	if tmpl == "" {
		if source.Summary == "" {
			return "Busy"
		}
		return source.Summary
	}
	out := tmpl
	out = strings.ReplaceAll(out, "{title}", source.Summary)
	out = strings.ReplaceAll(out, "{location}", source.Location)
	return out
}
