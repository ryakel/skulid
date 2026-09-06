package httpx

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/ryakel/skulid/internal/db"
	"github.com/ryakel/skulid/internal/hours"
)

// hoursForm holds the per-account view-model rendered into hours.html.
type hoursForm struct {
	AccountID int64
	Email     string
	TZ        string
	Zones     []hours.ZoneGroup
	Columns   []hoursColumn
}

func (s *Server) handleHoursPage(w http.ResponseWriter, r *http.Request) {
	s.renderHoursPage(w, r, 0, nil, "")
}

// renderHoursPage draws every account's form. When overlay is non-nil the
// values posted for account overlayID are shown back instead of the stored
// ones, so a rejected save doesn't discard what was just typed.
func (s *Server) renderHoursPage(w http.ResponseWriter, r *http.Request, overlayID int64, overlay *http.Request, errMsg string) {
	accounts, err := s.Accounts.List(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	forms := make([]hoursForm, 0, len(accounts))
	for _, a := range accounts {
		f := hoursFormFor(a)
		if overlay != nil && a.ID == overlayID {
			applyHoursOverlay(&f, overlay)
		}
		forms = append(forms, f)
	}
	data := s.pageData(r, "Hours")
	data["Forms"] = forms
	data["Days"] = weekDays
	if errMsg != "" {
		data["Error"] = errMsg
	}
	s.render(w, "hours", data)
}

func applyHoursOverlay(f *hoursForm, r *http.Request) {
	f.TZ = strings.TrimSpace(r.FormValue("tz"))
	f.Zones = hours.ZoneGroupsWith(f.TZ)
	for i := range f.Columns {
		col := &f.Columns[i]
		for _, d := range weekDays {
			col.Values[d.Key] = strings.TrimSpace(r.FormValue(col.Key + "_" + d.Key))
		}
	}
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
	// The account is the root of the override chain, so its zone has to be a
	// real one — there is nothing above it to inherit from.
	tz := strOr(strings.TrimSpace(r.FormValue("tz")), "UTC")
	if _, err := time.LoadLocation(tz); err != nil {
		s.renderHoursPage(w, r, id, r, fmt.Sprintf("%q is not a time zone this server can load.", tz))
		return
	}

	parsed := make([]hours.WorkingHours, 0, 3)
	for _, prefix := range []string{"working", "personal", "meeting"} {
		wh, err := buildHoursFromForm(r, prefix, tz)
		if err != nil {
			s.renderHoursPage(w, r, id, r, err.Error())
			return
		}
		parsed = append(parsed, wh)
	}

	if err := s.Accounts.UpdateHours(r.Context(), id,
		mustMarshal(parsed[0]), jsonOrNil(parsed[1]), jsonOrNil(parsed[2])); err != nil {
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

	workingDays := daysToCSV(working)
	return hoursForm{
		AccountID: a.ID,
		Email:     a.Email,
		TZ:        working.TimeZone,
		Zones:     hours.ZoneGroupsWith(working.TimeZone),
		Columns: []hoursColumn{{
			Key:       "working",
			Title:     "Working",
			Help:      "Work tasks and meetings land here.",
			Values:    workingDays,
			Inherited: map[string]string{},
		}, {
			Key:       "personal",
			Title:     "Personal",
			Help:      "Habits like Lunch land here.",
			Values:    daysToCSV(blankIfFallback(a.PersonalHours, personalRaw)),
			Inherited: workingDays,
		}, {
			Key:       "meeting",
			Title:     "Meeting",
			Help:      "When others may book you.",
			Values:    daysToCSV(blankIfFallback(a.MeetingHours, meetingRaw)),
			Inherited: workingDays,
		}},
	}
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
//
// Ranges are normalized on the way in and a malformed one is an error rather
// than a silent drop: an unparsable range used to be stored happily and then
// ignored by the scheduler, so the calendar simply had no availability and
// nothing anywhere said why.
func buildHoursFromForm(r *http.Request, prefix, tz string) (hours.WorkingHours, error) {
	out := hours.WorkingHours{TimeZone: tz, Days: map[string][]string{}}
	any := false
	for _, d := range weekDays {
		raw := strings.TrimSpace(r.FormValue(prefix + "_" + d.Key))
		if raw == "" {
			out.Days[d.Key] = nil
			continue
		}
		ranges, bad := hours.NormalizeRanges(raw)
		if bad != "" {
			return hours.WorkingHours{}, fmt.Errorf(
				"%s hours, %s: %q is not a time range — write it as HH:MM-HH:MM, e.g. 09:00-17:00",
				prefix, d.Label, bad)
		}
		if len(ranges) == 0 {
			out.Days[d.Key] = nil
			continue
		}
		any = true
		out.Days[d.Key] = ranges
	}
	if !any {
		// No values at all — blank the struct so jsonOrNil returns nil.
		return hours.WorkingHours{}, nil
	}
	return out, nil
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
	data["TaskMinChunkMinutes"] = db.TaskMinChunkMinutes(r.Context(), s.Settings)
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
	// Empty means "use the default"; anything else has to be a positive whole
	// number of minutes, since a zero or negative minimum would let a task
	// shatter into arbitrarily small fragments.
	minChunk := strings.TrimSpace(r.FormValue("task_min_chunk_minutes"))
	if minChunk != "" {
		n, err := strconv.Atoi(minChunk)
		if err != nil || n < 5 || n > 480 {
			http.Error(w, "minimum task block must be between 5 and 480 minutes", http.StatusBadRequest)
			return
		}
	}
	if err := s.Settings.Set(r.Context(), db.SettingTaskMinChunkMinutes, minChunk); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Buffer values changed -> re-derive decompress events on every calendar.
	go s.Worker.RecomputeAllBuffers(context.Background())
	http.Redirect(w, r, "/settings/buffers", http.StatusFound)
}

// handleBuffersRecompute is the manual "Recompute now" button; runs the
// decompression engine for every connected calendar in the background.
func (s *Server) handleBuffersRecompute(w http.ResponseWriter, r *http.Request) {
	go s.Worker.RecomputeAllBuffers(context.Background())
	http.Redirect(w, r, "/settings/buffers", http.StatusFound)
}
