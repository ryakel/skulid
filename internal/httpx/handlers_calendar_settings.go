package httpx

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/ryakel/skulid/internal/db"
	"github.com/ryakel/skulid/internal/hours"
)

type calendarSettingsForm struct {
	Calendar       db.Calendar
	AccountEmail   string
	Categories     []db.Category
	WorkingDays    map[string]string
	PersonalDays   map[string]string
	MeetingDays    map[string]string
	TZ             string
	Buffers        db.BufferSettings
	BuffersFromCal bool // override is set on the calendar (vs falling back to global)
}

func (s *Server) handleCalendarSettings(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	cal, err := s.Calendars.Get(r.Context(), id)
	if err != nil || cal == nil {
		http.NotFound(w, r)
		return
	}
	acct, _ := s.Accounts.Get(r.Context(), cal.AccountID)
	cats, _ := s.Categories.List(r.Context())

	working, _ := hours.Parse(emptyToNil(cal.WorkingHours))
	personal, _ := hours.Parse(emptyToNil(cal.PersonalHours))
	meeting, _ := hours.Parse(emptyToNil(cal.MeetingHours))

	tz := working.TimeZone
	if tz == "" {
		tz = cal.TimeZone
	}

	form := calendarSettingsForm{
		Calendar:       *cal,
		AccountEmail:   accountEmail(acct),
		Categories:     cats,
		TZ:             tz,
		WorkingDays:    daysToCSV(blankIfFallback(cal.WorkingHours, working)),
		PersonalDays:   daysToCSV(blankIfFallback(cal.PersonalHours, personal)),
		MeetingDays:    daysToCSV(blankIfFallback(cal.MeetingHours, meeting)),
		BuffersFromCal: cal.Buffers != "",
	}
	if cal.Buffers != "" {
		form.Buffers = db.EffectiveCalendarBuffers(r.Context(), s.Settings, cal)
	}

	data := s.pageData(r, cal.Summary)
	data["Form"] = form
	data["Days"] = weekDays
	s.render(w, "calendar_settings", data)
}

func (s *Server) handleCalendarSettingsSave(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	cal, err := s.Calendars.Get(r.Context(), id)
	if err != nil || cal == nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}

	// Default category — empty means clear.
	var catID *int64
	if v := strings.TrimSpace(r.FormValue("default_category_id")); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			catID = &n
		}
	}
	if err := s.Calendars.SetDefaultCategory(r.Context(), id, catID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Hours — same form shape as the account-level hours page.
	tz := strOr(strings.TrimSpace(r.FormValue("tz")), cal.TimeZone)
	working := buildHoursFromForm(r, "working", tz)
	personal := buildHoursFromForm(r, "personal", tz)
	meeting := buildHoursFromForm(r, "meeting", tz)
	if err := s.Calendars.UpdateHours(r.Context(), id,
		jsonOrNil(working), jsonOrNil(personal), jsonOrNil(meeting)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Buffers — when "use_calendar_override" isn't checked, clear the override.
	var bufStr string
	if r.FormValue("use_calendar_buffers") != "" {
		b := db.BufferSettings{
			TaskHabitBreakMinutes: int(parseInt64(r.FormValue("task_habit_break_minutes"))),
			DecompressionMinutes:  int(parseInt64(r.FormValue("decompression_minutes"))),
			TravelMinutes:         int(parseInt64(r.FormValue("travel_minutes"))),
		}
		bufStr = db.EncodeBuffers(b)
	}
	if err := s.Calendars.UpdateBuffers(r.Context(), id, bufStr); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/calendars/"+strconv.FormatInt(id, 10), http.StatusFound)
}

func accountEmail(a *db.Account) string {
	if a == nil {
		return ""
	}
	return a.Email
}

// handleCalendarToggleEnabled flips the calendar's enabled flag. When turning
// off, also tears down the watch channel so Google stops billing notifications
// we'd just discard. When turning on, kicks the worker to re-register the
// watch and run the next sync.
func (s *Server) handleCalendarToggleEnabled(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	cal, err := s.Calendars.Get(r.Context(), id)
	if err != nil || cal == nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	enabled := r.FormValue("enabled") == "1"
	if err := s.Calendars.SetEnabled(r.Context(), id, enabled); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Re-registering the watch handles both directions: the manager
	// short-circuits and tears down the existing channel for disabled
	// calendars; for newly-enabled ones it issues a fresh one.
	go func(accountID, calID int64) {
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
		defer cancel()
		if err := s.Worker.RegisterWatch(ctx, accountID, calID); err != nil {
			s.Log.Warn("watch toggle on enable change failed", "cal_id", calID, "err", err)
		}
	}(cal.AccountID, id)
	if enabled {
		// Immediately enqueue a sync so the user sees fresh events without
		// waiting for the 5-minute polling tick.
		s.Worker.EnqueueCalendar(cal.AccountID, id)
	}
	http.Redirect(w, r, "/accounts", http.StatusFound)
}
