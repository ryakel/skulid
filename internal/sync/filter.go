package sync

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"google.golang.org/api/calendar/v3"
)

type Filter struct {
	TitleRegex   string   `json:"title_regex,omitempty"`
	ColorIDs     []string `json:"color_ids,omitempty"`
	AttendeeAny  []string `json:"attendee_match,omitempty"`
	FreeBusy     string   `json:"free_busy,omitempty"` // "busy" | "free" | ""
	AllDay       string   `json:"all_day,omitempty"`   // "only" | "exclude" | ""
	StartHour    int      `json:"start_hour,omitempty"`
	EndHour      int      `json:"end_hour,omitempty"`
}

func ParseFilter(raw json.RawMessage) (Filter, error) {
	var f Filter
	if len(raw) == 0 {
		return f, nil
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		return f, err
	}
	return f, nil
}

// Match reports whether the event passes the filter.
func (f Filter) Match(ev *calendar.Event) bool {
	if ev.Status == "cancelled" {
		return false
	}
	if f.TitleRegex != "" {
		re, err := regexp.Compile(f.TitleRegex)
		if err != nil || !re.MatchString(ev.Summary) {
			return false
		}
	}
	if len(f.ColorIDs) > 0 {
		ok := false
		for _, c := range f.ColorIDs {
			if c == ev.ColorId {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	if len(f.AttendeeAny) > 0 {
		ok := false
		for _, want := range f.AttendeeAny {
			want = strings.ToLower(want)
			for _, att := range ev.Attendees {
				if strings.ToLower(att.Email) == want {
					ok = true
					break
				}
			}
			if ok {
				break
			}
		}
		if !ok {
			return false
		}
	}
	if f.FreeBusy != "" {
		isFree := ev.Transparency == "transparent"
		if f.FreeBusy == "busy" && isFree {
			return false
		}
		if f.FreeBusy == "free" && !isFree {
			return false
		}
	}
	if f.AllDay != "" {
		isAllDay := ev.Start != nil && ev.Start.DateTime == "" && ev.Start.Date != ""
		if f.AllDay == "only" && !isAllDay {
			return false
		}
		if f.AllDay == "exclude" && isAllDay {
			return false
		}
	}
	if f.StartHour != 0 || f.EndHour != 0 {
		if ev.Start == nil || ev.Start.DateTime == "" {
			return false
		}
		t, err := time.Parse(time.RFC3339, ev.Start.DateTime)
		if err != nil {
			return false
		}
		h := t.Hour()
		if f.StartHour != 0 && h < f.StartHour {
			return false
		}
		if f.EndHour != 0 && h >= f.EndHour {
			return false
		}
	}
	return true
}
