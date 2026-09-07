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

// hoursColumn is one column of the availability grid: a kind of hours, what
// this calendar has set for each day, and what a blank cell falls back to.
// Rendering the fallback is the whole point — without it a blank box reads as
// "never available" when it actually means "whatever the account says".
type hoursColumn struct {
	Key       string // form field prefix: working | personal | meeting
	Title     string
	Help      string
	Values    map[string]string // day key -> comma-separated ranges ("" = inherit)
	Inherited map[string]string // day key -> what a blank cell resolves to
}

type calendarSettingsForm struct {
	Calendar     db.Calendar
	AccountEmail string
	Categories   []db.Category
	CategoryID   string // kept as a string so a rejected save re-renders the choice

	// TZ is the calendar's explicit zone override. Empty is the normal case:
	// the times below are read in InheritedTZ, whatever Google currently says
	// the calendar's zone is.
	TZ          string
	InheritedTZ string
	Zones       []hours.ZoneGroup

	Columns []hoursColumn

	Buffers        db.BufferSettings
	GlobalBuffers  db.BufferSettings
	BuffersFromCal bool
}

func (s *Server) handleCalendarSettings(w http.ResponseWriter, r *http.Request) {
	cal, ok := s.calendarFromURL(w, r)
	if !ok {
		return
	}
	s.renderCalendarSettings(w, r, cal, nil, "")
}

// renderCalendarSettings draws the page. When overlay is non-nil the submitted
// values are shown back instead of the stored ones, so a rejected save doesn't
// throw away everything the owner just typed.
func (s *Server) renderCalendarSettings(w http.ResponseWriter, r *http.Request, cal *db.Calendar, overlay *http.Request, errMsg string) {
	ctx := r.Context()
	acct, _ := s.Accounts.Get(ctx, cal.AccountID)
	cats, _ := s.Categories.List(ctx)

	form := calendarSettingsForm{
		Calendar:      *cal,
		AccountEmail:  accountEmail(acct),
		Categories:    cats,
		CategoryID:    derefID(cal.DefaultCategoryID),
		TZ:            calendarZoneOverride(cal),
		InheritedTZ:   db.CalendarZone(cal, acct),
		GlobalBuffers: db.LoadBuffers(ctx, s.Settings),
		// Start from what is actually in effect, so ticking the override box
		// begins at today's behavior instead of at zeros.
		Buffers:        db.EffectiveCalendarBuffers(ctx, s.Settings, cal),
		BuffersFromCal: cal.Buffers != "",
	}
	form.Columns = calendarHoursColumns(cal, acct)
	form.Zones = hours.ZoneGroupsWith(form.TZ)

	if overlay != nil {
		applyCalendarOverlay(&form, overlay)
		form.Zones = hours.ZoneGroupsWith(form.TZ)
	}

	data := s.pageData(r, cal.Summary)
	data["Form"] = form
	data["Days"] = weekDays
	if errMsg != "" {
		data["Error"] = errMsg
	}
	s.render(w, "calendar_settings", data)
}

// calendarHoursColumns builds the three grid columns. Each column's Inherited
// map is computed by asking the real override chain what this calendar would
// resolve to with that one field cleared — so the placeholder can never drift
// from what the engines actually do.
func calendarHoursColumns(cal *db.Calendar, acct *db.Account) []hoursColumn {
	specs := []struct {
		key   string
		title string
		help  string
		kind  db.HoursKind
	}{
		{"working", "Working", "Work tasks and meetings land here.", db.HoursWorking},
		{"personal", "Personal", "Habits like Lunch land here.", db.HoursPersonal},
		{"meeting", "Meeting", "When others may book you.", db.HoursMeeting},
	}

	out := make([]hoursColumn, 0, len(specs))
	for _, spec := range specs {
		stored := calendarHoursFor(cal, spec.kind)
		parsed, _ := hours.Parse(emptyToNil(stored))

		cleared := *cal
		clearCalendarHours(&cleared, spec.kind)
		fallback, _ := hours.Parse(emptyToNil(db.EffectiveCalendarHours(&cleared, acct, spec.kind)))

		out = append(out, hoursColumn{
			Key:       spec.key,
			Title:     spec.title,
			Help:      spec.help,
			Values:    daysToCSV(blankIfFallback(stored, parsed)),
			Inherited: daysToCSV(fallback),
		})
	}
	return out
}

func calendarHoursFor(cal *db.Calendar, kind db.HoursKind) []byte {
	switch kind {
	case db.HoursPersonal:
		return cal.PersonalHours
	case db.HoursMeeting:
		return cal.MeetingHours
	default:
		return cal.WorkingHours
	}
}

func clearCalendarHours(cal *db.Calendar, kind db.HoursKind) {
	switch kind {
	case db.HoursPersonal:
		cal.PersonalHours = nil
	case db.HoursMeeting:
		cal.MeetingHours = nil
	default:
		cal.WorkingHours = nil
	}
}

// calendarZoneOverride is the zone explicitly pinned on this calendar, if any.
// The form writes the same zone into all three blobs, so the first one that
// carries it answers for the calendar. Empty means the zone is inherited.
//
// It reads the JSON directly rather than going through hours.Parse, which
// substitutes UTC for an absent zone and would turn "inherited" into an
// explicit UTC override the first time the page was saved.
func calendarZoneOverride(cal *db.Calendar) string {
	for _, raw := range []json.RawMessage{cal.WorkingHours, cal.PersonalHours, cal.MeetingHours} {
		if len(raw) == 0 {
			continue
		}
		var probe struct {
			TimeZone string `json:"time_zone"`
		}
		if err := json.Unmarshal(raw, &probe); err != nil {
			continue
		}
		if z := strings.TrimSpace(probe.TimeZone); z != "" {
			return z
		}
	}
	return ""
}

func applyCalendarOverlay(form *calendarSettingsForm, r *http.Request) {
	form.CategoryID = strings.TrimSpace(r.FormValue("default_category_id"))
	form.TZ = strings.TrimSpace(r.FormValue("tz"))
	for i := range form.Columns {
		col := &form.Columns[i]
		for _, d := range weekDays {
			col.Values[d.Key] = strings.TrimSpace(r.FormValue(col.Key + "_" + d.Key))
		}
	}
	form.BuffersFromCal = r.FormValue("use_calendar_buffers") != ""
	form.Buffers = db.BufferSettings{
		TaskHabitBreakMinutes: clampMinutes(parseInt64(r.FormValue("task_habit_break_minutes"))),
		DecompressionMinutes:  clampMinutes(parseInt64(r.FormValue("decompression_minutes"))),
		TravelMinutes:         clampMinutes(parseInt64(r.FormValue("travel_minutes"))),
	}
}

func (s *Server) handleCalendarSettingsSave(w http.ResponseWriter, r *http.Request) {
	cal, ok := s.calendarFromURL(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}

	// Validate the whole form before touching anything. The save is three
	// separate writes; failing halfway through would leave the calendar in a
	// state the owner never asked for.
	tz := strings.TrimSpace(r.FormValue("tz"))
	if tz != "" {
		if _, err := time.LoadLocation(tz); err != nil {
			s.renderCalendarSettings(w, r, cal, r,
				fmt.Sprintf("%q is not a time zone this server can load.", tz))
			return
		}
	}

	parsed := make([]hours.WorkingHours, 0, 3)
	for _, prefix := range []string{"working", "personal", "meeting"} {
		wh, err := buildHoursFromForm(r, prefix, tz)
		if err != nil {
			s.renderCalendarSettings(w, r, cal, r, err.Error())
			return
		}
		parsed = append(parsed, wh)
	}

	if err := s.saveCalendarSettings(r, cal, parsed[0], parsed[1], parsed[2]); err != nil {
		s.renderCalendarSettings(w, r, cal, r, err.Error())
		return
	}

	http.Redirect(w, r, "/calendars/"+strconv.FormatInt(cal.ID, 10), http.StatusFound)
}

func (s *Server) saveCalendarSettings(r *http.Request, cal *db.Calendar, working, personal, meeting hours.WorkingHours) error {
	ctx := r.Context()

	var catID *int64
	if v := strings.TrimSpace(r.FormValue("default_category_id")); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			catID = &n
		}
	}
	if err := s.Calendars.SetDefaultCategory(ctx, cal.ID, catID); err != nil {
		return err
	}
	if err := s.Calendars.UpdateHours(ctx, cal.ID,
		jsonOrNil(working), jsonOrNil(personal), jsonOrNil(meeting)); err != nil {
		return err
	}

	// An unticked override box clears the column so the calendar tracks the
	// global values again.
	var bufStr string
	if r.FormValue("use_calendar_buffers") != "" {
		bufStr = db.EncodeBuffers(db.BufferSettings{
			TaskHabitBreakMinutes: clampMinutes(parseInt64(r.FormValue("task_habit_break_minutes"))),
			DecompressionMinutes:  clampMinutes(parseInt64(r.FormValue("decompression_minutes"))),
			TravelMinutes:         clampMinutes(parseInt64(r.FormValue("travel_minutes"))),
		})
	}
	return s.Calendars.UpdateBuffers(ctx, cal.ID, bufStr)
}

func (s *Server) calendarFromURL(w http.ResponseWriter, r *http.Request) (*db.Calendar, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return nil, false
	}
	cal, err := s.Calendars.Get(r.Context(), id)
	if err != nil || cal == nil {
		http.NotFound(w, r)
		return nil, false
	}
	return cal, true
}

// clampMinutes keeps a buffer inside the range the number inputs advertise, so
// a hand-crafted POST can't store a value the form claims is impossible.
func clampMinutes(v int64) int {
	switch {
	case v < 0:
		return 0
	case v > 240:
		return 240
	default:
		return int(v)
	}
}

func derefID(p *int64) string {
	if p == nil {
		return ""
	}
	return strconv.FormatInt(*p, 10)
}

func accountEmail(a *db.Account) string {
	if a == nil {
		return ""
	}
	return a.Email
}

// handleCalendarToggleHidden collapses a calendar out of the Accounts list, or
// brings it back. Display only: it deliberately does not touch the watch
// channel or enqueue a sync, because hiding a row is not a statement about
// whether that calendar should be syncing.
func (s *Server) handleCalendarToggleHidden(w http.ResponseWriter, r *http.Request) {
	cal, ok := s.calendarFromURL(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if err := s.Calendars.SetHidden(r.Context(), cal.ID, r.FormValue("hidden") == "1"); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/accounts", http.StatusFound)
}

// handleCalendarToggleEnabled flips the calendar's enabled flag. When turning
// off, also tears down the watch channel so Google stops billing notifications
// we'd just discard. When turning on, kicks the worker to re-register the
// watch and run the next sync.
func (s *Server) handleCalendarToggleEnabled(w http.ResponseWriter, r *http.Request) {
	cal, ok := s.calendarFromURL(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	enabled := r.FormValue("enabled") == "1"
	if err := s.Calendars.SetEnabled(r.Context(), cal.ID, enabled); err != nil {
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
	}(cal.AccountID, cal.ID)
	if enabled {
		// Immediately enqueue a sync so the user sees fresh events without
		// waiting for the 5-minute polling tick.
		s.Worker.EnqueueCalendar(cal.AccountID, cal.ID)
	}
	http.Redirect(w, r, "/accounts", http.StatusFound)
}
