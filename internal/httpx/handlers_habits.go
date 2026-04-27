package httpx

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/ryakel/skulid/internal/db"
)

const habitTimeout = 5 * time.Minute

type habitRow struct {
	Habit         db.Habit
	CalendarLabel string
}

func (s *Server) handleHabitsPage(w http.ResponseWriter, r *http.Request) {
	hs, err := s.Habits.List(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_, idx, err := s.calendarOptions(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	rows := make([]habitRow, 0, len(hs))
	for _, h := range hs {
		rows = append(rows, habitRow{Habit: h, CalendarLabel: calLabel(idx, h.TargetCalendarID)})
	}
	data := s.pageData(r, "Habits")
	data["Habits"] = rows
	s.render(w, "habits", data)
}

func (s *Server) handleHabitEditPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	h := &db.Habit{
		DurationMinutes: 60,
		IdealTime:       "12:00",
		FlexMinutes:     90,
		HoursKind:       string(db.HoursPersonal),
		HorizonDays:     14,
		Enabled:         true,
		DaysOfWeek:      []string{"mon", "tue", "wed", "thu", "fri"},
	}
	if idStr := chi.URLParam(r, "id"); idStr != "" {
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		got, err := s.Habits.Get(ctx, id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if got == nil {
			http.NotFound(w, r)
			return
		}
		h = got
	}
	cals, _, err := s.calendarOptions(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	cats, err := s.Categories.List(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	dowSet := map[string]bool{}
	for _, d := range h.DaysOfWeek {
		dowSet[d] = true
	}
	data := s.pageData(r, "Habit")
	data["Habit"] = h
	data["Calendars"] = cals
	data["Categories"] = cats
	data["Days"] = weekDays
	data["DowSet"] = dowSet
	s.render(w, "habit_edit", data)
}

func (s *Server) handleHabitSave(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	h := &db.Habit{}
	if idStr := chi.URLParam(r, "id"); idStr != "" {
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		got, err := s.Habits.Get(r.Context(), id)
		if err != nil || got == nil {
			http.NotFound(w, r)
			return
		}
		h = got
	}

	h.Title = strings.TrimSpace(r.FormValue("title"))
	h.TargetCalendarID = parseInt64(r.FormValue("target_calendar_id"))
	h.DurationMinutes = int(parseInt64(r.FormValue("duration_minutes")))
	if h.DurationMinutes <= 0 {
		h.DurationMinutes = 60
	}
	h.IdealTime = strOr(strings.TrimSpace(r.FormValue("ideal_time")), "12:00")
	h.FlexMinutes = int(parseInt64(r.FormValue("flex_minutes")))
	if h.FlexMinutes < 0 {
		h.FlexMinutes = 0
	}
	h.HoursKind = strOr(r.FormValue("hours_kind"), "personal")
	h.HorizonDays = int(parseInt64(r.FormValue("horizon_days")))
	if h.HorizonDays <= 0 {
		h.HorizonDays = 14
	}
	h.Enabled = r.FormValue("enabled") != ""
	if catID := parseInt64(r.FormValue("category_id")); catID > 0 {
		h.CategoryID = &catID
	} else {
		h.CategoryID = nil
	}
	dows := r.Form["dow"]
	clean := make([]string, 0, len(dows))
	for _, d := range dows {
		if d = strings.TrimSpace(d); d != "" {
			clean = append(clean, d)
		}
	}
	h.DaysOfWeek = clean

	if h.Title == "" || h.TargetCalendarID == 0 || len(h.DaysOfWeek) == 0 {
		http.Error(w, "title, target calendar, and at least one weekday are required", http.StatusBadRequest)
		return
	}

	if h.ID == 0 {
		newID, err := s.Habits.Create(r.Context(), h)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		h.ID = newID
	} else {
		// Edited habit — drop today-and-future occurrences so the scheduler
		// rebuilds them under the new rules.
		_ = s.Occurrences.DeleteFromDate(r.Context(), h.ID, time.Now())
		if err := s.Habits.Update(r.Context(), h); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	go s.placeHabitAsync(h.ID)
	http.Redirect(w, r, "/habits", http.StatusFound)
}

func (s *Server) handleHabitDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	if err := s.deleteHabitAndEvents(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/habits", http.StatusFound)
}

func (s *Server) handleHabitPlace(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	go s.placeHabitAsync(id)
	http.Redirect(w, r, "/habits", http.StatusFound)
}

// deleteHabitAndEvents removes every Google event the habit has placed before
// deleting the habit row itself. ON DELETE CASCADE on habit_occurrence cleans
// up the rows, but Google still has the events.
func (s *Server) deleteHabitAndEvents(ctx context.Context, habitID int64) error {
	h, _ := s.Habits.Get(ctx, habitID)
	if h != nil {
		occ, _ := s.Occurrences.ListByHabit(ctx, habitID)
		if cal, err := s.Calendars.Get(ctx, h.TargetCalendarID); err == nil {
			if cli, err := s.ClientFor(ctx, cal.AccountID); err == nil {
				for _, o := range occ {
					_ = cli.DeleteEvent(ctx, cal.GoogleCalendarID, o.TargetEventID)
				}
			}
		}
	}
	return s.Habits.Delete(ctx, habitID)
}

func (s *Server) placeHabitAsync(habitID int64) {
	ctx, cancel := context.WithTimeout(context.Background(), habitTimeout)
	defer cancel()
	if err := s.Scheduler.PlaceHabit(ctx, habitID); err != nil {
		s.Log.Error("place habit failed", "habit_id", habitID, "err", err)
	}
}
