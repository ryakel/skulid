package httpx

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/ryakel/skulid/internal/db"
	"github.com/ryakel/skulid/internal/hours"
)

// hoursForm holds the per-account view-model rendered into hours.html.
type hoursForm struct {
	AccountID    int64
	Email        string
	TZ           string
	WorkingDays  map[string]string // day key -> CSV ranges
	PersonalDays map[string]string
	MeetingDays  map[string]string
}

func (s *Server) handleHoursPage(w http.ResponseWriter, r *http.Request) {
	accounts, err := s.Accounts.List(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	forms := make([]hoursForm, 0, len(accounts))
	for _, a := range accounts {
		forms = append(forms, hoursFormFor(a))
	}
	data := s.pageData(r, "Hours")
	data["Forms"] = forms
	data["Days"] = weekDays
	s.render(w, "hours", data)
}

func (s *Server) handleHoursSave(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	tz := strOr(strings.TrimSpace(r.FormValue("tz")), "UTC")

	working := buildHoursFromForm(r, "working", tz)
	personal := buildHoursFromForm(r, "personal", tz)
	meeting := buildHoursFromForm(r, "meeting", tz)

	workingJSON := mustMarshal(working)
	personalJSON := jsonOrNil(personal)
	meetingJSON := jsonOrNil(meeting)

	if err := s.Accounts.UpdateHours(r.Context(), id, workingJSON, personalJSON, meetingJSON); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/settings/hours", http.StatusFound)
}

// hoursFormFor builds the view model. Empty Personal/Meeting render as blank
// inputs, signaling "fall back to Working" — saving with all-blank stays NULL.
func hoursFormFor(a db.Account) hoursForm {
	working, _ := hours.Parse(a.WorkingHours)
	personalRaw, _ := hours.Parse(emptyToNil(a.PersonalHours))
	meetingRaw, _ := hours.Parse(emptyToNil(a.MeetingHours))

	out := hoursForm{
		AccountID:    a.ID,
		Email:        a.Email,
		TZ:           working.TimeZone,
		WorkingDays:  daysToCSV(working),
		PersonalDays: daysToCSV(blankIfFallback(a.PersonalHours, personalRaw)),
		MeetingDays:  daysToCSV(blankIfFallback(a.MeetingHours, meetingRaw)),
	}
	return out
}

// blankIfFallback returns an empty WorkingHours when the underlying column was
// NULL/empty, so the form renders blank inputs (signal: "use Working").
func blankIfFallback(raw []byte, parsed hours.WorkingHours) hours.WorkingHours {
	if len(raw) == 0 {
		return hours.WorkingHours{Days: map[string][]string{}}
	}
	return parsed
}

// emptyToNil avoids hours.Parse short-circuiting to Default() for an empty
// stored value when we want "blank means blank, not Mon-Fri 9-5".
func emptyToNil(raw []byte) []byte {
	if len(raw) == 0 {
		return []byte(`{"time_zone":"","days":{}}`)
	}
	return raw
}

func daysToCSV(wh hours.WorkingHours) map[string]string {
	m := make(map[string]string, len(weekDays))
	for _, d := range weekDays {
		m[d.Key] = strings.Join(wh.Days[d.Key], ",")
	}
	return m
}

// buildHoursFromForm reads a set of `<prefix>_<dayKey>` form fields and
// produces a WorkingHours. If every day's value is blank, returns the zero
// WorkingHours which the caller persists as SQL NULL.
func buildHoursFromForm(r *http.Request, prefix, tz string) hours.WorkingHours {
	out := hours.WorkingHours{TimeZone: tz, Days: map[string][]string{}}
	any := false
	for _, d := range weekDays {
		raw := strings.TrimSpace(r.FormValue(prefix + "_" + d.Key))
		if raw == "" {
			out.Days[d.Key] = nil
			continue
		}
		any = true
		ranges := []string{}
		for _, p := range strings.Split(raw, ",") {
			if p = strings.TrimSpace(p); p != "" {
				ranges = append(ranges, p)
			}
		}
		out.Days[d.Key] = ranges
	}
	if !any {
		// No values at all — blank the struct so jsonOrNil returns nil.
		return hours.WorkingHours{}
	}
	return out
}

func mustMarshal(wh hours.WorkingHours) json.RawMessage {
	b, err := json.Marshal(wh)
	if err != nil {
		// Marshaling our own struct shouldn't fail; if it does, surface as empty.
		return nil
	}
	return b
}

func jsonOrNil(wh hours.WorkingHours) json.RawMessage {
	if wh.TimeZone == "" && len(wh.Days) == 0 {
		return nil
	}
	return mustMarshal(wh)
}

// ---------------------------------------------------------------------------
// Buffers
// ---------------------------------------------------------------------------

func (s *Server) handleBuffersPage(w http.ResponseWriter, r *http.Request) {
	data := s.pageData(r, "Buffers")
	data["Buffers"] = db.LoadBuffers(r.Context(), s.Settings)
	s.render(w, "buffers", data)
}

func (s *Server) handleBuffersSave(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	b := db.BufferSettings{
		TaskHabitBreakMinutes: int(parseInt64(r.FormValue("task_habit_break_minutes"))),
		DecompressionMinutes:  int(parseInt64(r.FormValue("decompression_minutes"))),
		TravelMinutes:         int(parseInt64(r.FormValue("travel_minutes"))),
	}
	if err := db.SaveBuffers(r.Context(), s.Settings, b); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/settings/buffers", http.StatusFound)
}
