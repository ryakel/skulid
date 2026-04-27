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

// taskTimeout bounds a single scheduling pass — generous because freebusy +
// event write is the slow path.
const taskTimeout = 90 * time.Second

type taskRow struct {
	Task          db.Task
	CalendarLabel string
}

func (s *Server) handleTasksPage(w http.ResponseWriter, r *http.Request) {
	tasks, err := s.Tasks.List(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_, idx, err := s.calendarOptions(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	rows := make([]taskRow, 0, len(tasks))
	for _, t := range tasks {
		rows = append(rows, taskRow{
			Task:          t,
			CalendarLabel: calLabel(idx, t.TargetCalendarID),
		})
	}
	data := s.pageData(r, "Tasks")
	data["Tasks"] = rows
	s.render(w, "tasks", data)
}

func (s *Server) handleTaskEditPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	t := &db.Task{Priority: db.PriorityMedium, Status: db.TaskPending, DurationMinutes: 30}
	if idStr := chi.URLParam(r, "id"); idStr != "" {
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		got, err := s.Tasks.Get(ctx, id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if got == nil {
			http.NotFound(w, r)
			return
		}
		t = got
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
	data := s.pageData(r, "Task")
	data["Task"] = t
	data["Calendars"] = cals
	data["Categories"] = cats
	data["DueAtLocal"] = formatDatetimeLocal(t.DueAt)
	s.render(w, "task_edit", data)
}

func (s *Server) handleTaskSave(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	t := &db.Task{}
	if idStr := chi.URLParam(r, "id"); idStr != "" {
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		got, err := s.Tasks.Get(r.Context(), id)
		if err != nil || got == nil {
			http.NotFound(w, r)
			return
		}
		t = got
	}

	t.Title = strings.TrimSpace(r.FormValue("title"))
	t.Notes = strings.TrimSpace(r.FormValue("notes"))
	t.TargetCalendarID = parseInt64(r.FormValue("target_calendar_id"))
	t.Priority = strOr(r.FormValue("priority"), db.PriorityMedium)
	t.DurationMinutes = int(parseInt64(r.FormValue("duration_minutes")))
	if t.DurationMinutes <= 0 {
		t.DurationMinutes = 30
	}
	t.Status = strOr(r.FormValue("status"), db.TaskPending)
	if catID := parseInt64(r.FormValue("category_id")); catID > 0 {
		t.CategoryID = &catID
	} else {
		t.CategoryID = nil
	}
	if v := strings.TrimSpace(r.FormValue("due_at")); v != "" {
		due, err := parseDatetimeLocal(v)
		if err == nil {
			t.DueAt = &due
		}
	} else {
		t.DueAt = nil
	}

	if t.Title == "" || t.TargetCalendarID == 0 {
		http.Error(w, "title and target calendar are required", http.StatusBadRequest)
		return
	}

	if t.ID == 0 {
		newID, err := s.Tasks.Create(r.Context(), t)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		t.ID = newID
	} else {
		if err := s.Tasks.Update(r.Context(), t); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	go s.placeTaskAsync(t.ID)
	http.Redirect(w, r, "/tasks", http.StatusFound)
}

func (s *Server) handleTaskDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	t, _ := s.Tasks.Get(r.Context(), id)
	if t != nil && t.ScheduledEventID != "" {
		if cal, err := s.Calendars.Get(r.Context(), t.TargetCalendarID); err == nil {
			if cli, err := s.ClientFor(r.Context(), cal.AccountID); err == nil {
				_ = cli.DeleteEvent(r.Context(), cal.GoogleCalendarID, t.ScheduledEventID)
			}
		}
	}
	if err := s.Tasks.Delete(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/tasks", http.StatusFound)
}

func (s *Server) handleTaskPlace(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	go s.placeTaskAsync(id)
	http.Redirect(w, r, "/tasks", http.StatusFound)
}

func (s *Server) handleTaskComplete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	t, err := s.Tasks.Get(r.Context(), id)
	if err != nil || t == nil {
		http.NotFound(w, r)
		return
	}
	t.Status = db.TaskCompleted
	if err := s.Tasks.Update(r.Context(), t); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Leave the scheduled event in place — completing a task means it
	// happened, not that the calendar block was wrong.
	http.Redirect(w, r, "/tasks", http.StatusFound)
}

// placeTaskAsync runs the scheduler outside the request context with its own
// timeout so the user gets a fast redirect.
func (s *Server) placeTaskAsync(taskID int64) {
	ctx, cancel := context.WithTimeout(context.Background(), taskTimeout)
	defer cancel()
	if err := s.Scheduler.PlaceTask(ctx, taskID); err != nil {
		s.Log.Error("place task failed", "task_id", taskID, "err", err)
	}
}

// parseDatetimeLocal accepts the value an <input type="datetime-local"> emits:
// YYYY-MM-DDTHH:MM or YYYY-MM-DDTHH:MM:SS, treated as UTC since browsers don't
// include a zone. Good enough for v1; users can adjust by setting Working
// hours in the right timezone.
func parseDatetimeLocal(v string) (time.Time, error) {
	for _, layout := range []string{"2006-01-02T15:04", "2006-01-02T15:04:05"} {
		if t, err := time.Parse(layout, v); err == nil {
			return t, nil
		}
	}
	return time.Time{}, errInvalidDatetimeLocal
}

func formatDatetimeLocal(t *time.Time) string {
	if t == nil || t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02T15:04")
}

var errInvalidDatetimeLocal = newConstErr("invalid datetime-local")

type constErr string

func newConstErr(s string) error  { return constErr(s) }
func (e constErr) Error() string  { return string(e) }
