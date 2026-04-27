package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ryakel/skulid/internal/db"
)

// ---------------------------------------------------------------------------
// Tasks
// ---------------------------------------------------------------------------

type listTasksInput struct {
	IncludeCompleted bool `json:"include_completed"`
}

type taskOut struct {
	ID                int64   `json:"id"`
	Title             string  `json:"title"`
	Notes             string  `json:"notes,omitempty"`
	Priority          string  `json:"priority"`
	DurationMinutes   int     `json:"duration_minutes"`
	DueAt             string  `json:"due_at,omitempty"`
	Status            string  `json:"status"`
	TargetCalendarID  int64   `json:"target_calendar_id"`
	CategoryID        *int64  `json:"category_id,omitempty"`
	ScheduledStartsAt string  `json:"scheduled_starts_at,omitempty"`
	ScheduledEndsAt   string  `json:"scheduled_ends_at,omitempty"`
	ScheduledEventID  string  `json:"scheduled_event_id,omitempty"`
}

func (t *Toolbox) listTasks(ctx context.Context, input json.RawMessage) (string, error) {
	var p listTasksInput
	if len(input) > 0 {
		_ = json.Unmarshal(input, &p)
	}
	tasks, err := t.tasks.List(ctx)
	if err != nil {
		return "", err
	}
	out := make([]taskOut, 0, len(tasks))
	for _, x := range tasks {
		if !p.IncludeCompleted && (x.Status == db.TaskCompleted || x.Status == db.TaskCancelled) {
			continue
		}
		out = append(out, taskToOut(x))
	}
	return marshalToolResult(out)
}

type createTaskInput struct {
	Title            string `json:"title"`
	TargetCalendarID int64  `json:"target_calendar_id"`
	DurationMinutes  int    `json:"duration_minutes"`
	Priority         string `json:"priority"`
	DueAt            string `json:"due_at"`
	CategoryID       *int64 `json:"category_id,omitempty"`
	Notes            string `json:"notes"`
}

func (t *Toolbox) createTask(ctx context.Context, input json.RawMessage) (string, error) {
	var p createTaskInput
	if err := json.Unmarshal(input, &p); err != nil {
		return "", err
	}
	if strings.TrimSpace(p.Title) == "" || p.TargetCalendarID == 0 {
		return "", fmt.Errorf("title and target_calendar_id are required")
	}
	task := &db.Task{
		Title:            strings.TrimSpace(p.Title),
		Notes:            p.Notes,
		Priority:         strOrDefault(p.Priority, db.PriorityMedium),
		DurationMinutes:  p.DurationMinutes,
		TargetCalendarID: p.TargetCalendarID,
		CategoryID:       p.CategoryID,
		Status:           db.TaskPending,
	}
	if task.DurationMinutes <= 0 {
		task.DurationMinutes = 30
	}
	if p.DueAt != "" {
		due, err := time.Parse(time.RFC3339, p.DueAt)
		if err != nil {
			return "", fmt.Errorf("due_at: %w", err)
		}
		task.DueAt = &due
	}
	id, err := t.tasks.Create(ctx, task)
	if err != nil {
		return "", err
	}
	t.logAudit(ctx, "create_task", "", fmt.Sprintf("created task #%d %q", id, task.Title))
	if t.scheduler != nil {
		if err := t.scheduler.PlaceTask(ctx, id); err != nil {
			// Surface as a non-fatal warning in the result; row is created.
			return marshalToolResult(map[string]any{
				"task_id": id, "status": "created", "schedule_warning": err.Error(),
			})
		}
	}
	return marshalToolResult(map[string]any{"task_id": id, "status": "created_and_scheduled"})
}

type updateTaskInput struct {
	TaskID           int64   `json:"task_id"`
	Title            string  `json:"title"`
	TargetCalendarID int64   `json:"target_calendar_id"`
	DurationMinutes  int     `json:"duration_minutes"`
	Priority         string  `json:"priority"`
	DueAt            *string `json:"due_at,omitempty"`
	CategoryID       *int64  `json:"category_id,omitempty"`
	Notes            *string `json:"notes,omitempty"`
	Status           string  `json:"status"`
}

func (t *Toolbox) updateTask(ctx context.Context, input json.RawMessage) (string, error) {
	var p updateTaskInput
	if err := json.Unmarshal(input, &p); err != nil {
		return "", err
	}
	if p.TaskID == 0 {
		return "", fmt.Errorf("task_id is required")
	}
	task, err := t.tasks.Get(ctx, p.TaskID)
	if err != nil {
		return "", err
	}
	if task == nil {
		return "", fmt.Errorf("task #%d not found", p.TaskID)
	}
	if p.Title != "" {
		task.Title = strings.TrimSpace(p.Title)
	}
	if p.TargetCalendarID != 0 {
		task.TargetCalendarID = p.TargetCalendarID
	}
	if p.DurationMinutes > 0 {
		task.DurationMinutes = p.DurationMinutes
	}
	if p.Priority != "" {
		task.Priority = p.Priority
	}
	if p.Status != "" {
		task.Status = p.Status
	}
	if p.CategoryID != nil {
		task.CategoryID = p.CategoryID
	}
	if p.Notes != nil {
		task.Notes = *p.Notes
	}
	if p.DueAt != nil {
		v := strings.TrimSpace(*p.DueAt)
		if v == "" {
			task.DueAt = nil
		} else {
			due, err := time.Parse(time.RFC3339, v)
			if err != nil {
				return "", fmt.Errorf("due_at: %w", err)
			}
			task.DueAt = &due
		}
	}
	if err := t.tasks.Update(ctx, task); err != nil {
		return "", err
	}
	t.logAudit(ctx, "update_task", "", fmt.Sprintf("updated task #%d", task.ID))
	if t.scheduler != nil && task.Status != db.TaskCompleted && task.Status != db.TaskCancelled {
		if err := t.scheduler.PlaceTask(ctx, task.ID); err != nil {
			return marshalToolResult(map[string]any{
				"task_id": task.ID, "status": "updated", "schedule_warning": err.Error(),
			})
		}
	}
	return marshalToolResult(map[string]any{"task_id": task.ID, "status": "updated"})
}

type taskIDOnlyInput struct {
	TaskID int64 `json:"task_id"`
}

func (t *Toolbox) completeTask(ctx context.Context, input json.RawMessage) (string, error) {
	var p taskIDOnlyInput
	if err := json.Unmarshal(input, &p); err != nil {
		return "", err
	}
	task, err := t.tasks.Get(ctx, p.TaskID)
	if err != nil || task == nil {
		return "", fmt.Errorf("task #%d not found", p.TaskID)
	}
	task.Status = db.TaskCompleted
	if err := t.tasks.Update(ctx, task); err != nil {
		return "", err
	}
	t.logAudit(ctx, "complete_task", "", fmt.Sprintf("completed task #%d", task.ID))
	return marshalToolResult(map[string]any{"task_id": task.ID, "status": "completed"})
}

func (t *Toolbox) deleteTask(ctx context.Context, input json.RawMessage) (string, error) {
	var p taskIDOnlyInput
	if err := json.Unmarshal(input, &p); err != nil {
		return "", err
	}
	task, err := t.tasks.Get(ctx, p.TaskID)
	if err != nil || task == nil {
		return "", fmt.Errorf("task #%d not found", p.TaskID)
	}
	if task.ScheduledEventID != "" {
		if cal, err := t.calendars.Get(ctx, task.TargetCalendarID); err == nil {
			if cli, err := t.clientFor(ctx, cal.AccountID); err == nil {
				_ = cli.DeleteEvent(ctx, cal.GoogleCalendarID, task.ScheduledEventID)
			}
		}
	}
	if err := t.tasks.Delete(ctx, p.TaskID); err != nil {
		return "", err
	}
	t.logAudit(ctx, "delete_task", "", fmt.Sprintf("deleted task #%d", p.TaskID))
	return marshalToolResult(map[string]any{"task_id": p.TaskID, "status": "deleted"})
}

func taskToOut(x db.Task) taskOut {
	o := taskOut{
		ID:               x.ID,
		Title:            x.Title,
		Notes:            x.Notes,
		Priority:         x.Priority,
		DurationMinutes:  x.DurationMinutes,
		Status:           x.Status,
		TargetCalendarID: x.TargetCalendarID,
		CategoryID:       x.CategoryID,
		ScheduledEventID: x.ScheduledEventID,
	}
	if x.DueAt != nil {
		o.DueAt = x.DueAt.Format(time.RFC3339)
	}
	if x.ScheduledStartsAt != nil {
		o.ScheduledStartsAt = x.ScheduledStartsAt.Format(time.RFC3339)
	}
	if x.ScheduledEndsAt != nil {
		o.ScheduledEndsAt = x.ScheduledEndsAt.Format(time.RFC3339)
	}
	return o
}

// ---------------------------------------------------------------------------
// Habits
// ---------------------------------------------------------------------------

type habitOut struct {
	ID               int64    `json:"id"`
	Title            string   `json:"title"`
	DurationMinutes  int      `json:"duration_minutes"`
	IdealTime        string   `json:"ideal_time"`
	FlexMinutes      int      `json:"flex_minutes"`
	DaysOfWeek       []string `json:"days_of_week"`
	HoursKind        string   `json:"hours_kind"`
	TargetCalendarID int64    `json:"target_calendar_id"`
	CategoryID       *int64   `json:"category_id,omitempty"`
	HorizonDays      int      `json:"horizon_days"`
	Enabled          bool     `json:"enabled"`
}

func (t *Toolbox) listHabits(ctx context.Context) (string, error) {
	hs, err := t.habits.List(ctx)
	if err != nil {
		return "", err
	}
	out := make([]habitOut, 0, len(hs))
	for _, h := range hs {
		out = append(out, habitOut{
			ID: h.ID, Title: h.Title, DurationMinutes: h.DurationMinutes,
			IdealTime: h.IdealTime, FlexMinutes: h.FlexMinutes,
			DaysOfWeek: h.DaysOfWeek, HoursKind: h.HoursKind,
			TargetCalendarID: h.TargetCalendarID, CategoryID: h.CategoryID,
			HorizonDays: h.HorizonDays, Enabled: h.Enabled,
		})
	}
	return marshalToolResult(out)
}

type createHabitInput struct {
	Title            string   `json:"title"`
	TargetCalendarID int64    `json:"target_calendar_id"`
	DurationMinutes  int      `json:"duration_minutes"`
	IdealTime        string   `json:"ideal_time"`
	FlexMinutes      int      `json:"flex_minutes"`
	DaysOfWeek       []string `json:"days_of_week"`
	HoursKind        string   `json:"hours_kind"`
	HorizonDays      int      `json:"horizon_days"`
	CategoryID       *int64   `json:"category_id,omitempty"`
}

func (t *Toolbox) createHabit(ctx context.Context, input json.RawMessage) (string, error) {
	var p createHabitInput
	if err := json.Unmarshal(input, &p); err != nil {
		return "", err
	}
	if strings.TrimSpace(p.Title) == "" || p.TargetCalendarID == 0 ||
		strings.TrimSpace(p.IdealTime) == "" || len(p.DaysOfWeek) == 0 {
		return "", fmt.Errorf("title, target_calendar_id, ideal_time, days_of_week are required")
	}
	h := &db.Habit{
		Title:            strings.TrimSpace(p.Title),
		TargetCalendarID: p.TargetCalendarID,
		DurationMinutes:  p.DurationMinutes,
		IdealTime:        strings.TrimSpace(p.IdealTime),
		FlexMinutes:      p.FlexMinutes,
		DaysOfWeek:       cleanDows(p.DaysOfWeek),
		HoursKind:        strOrDefault(p.HoursKind, string(db.HoursPersonal)),
		HorizonDays:      p.HorizonDays,
		Enabled:          true,
		CategoryID:       p.CategoryID,
	}
	id, err := t.habits.Create(ctx, h)
	if err != nil {
		return "", err
	}
	t.logAudit(ctx, "create_habit", "", fmt.Sprintf("created habit #%d %q", id, h.Title))
	if t.scheduler != nil {
		if err := t.scheduler.PlaceHabit(ctx, id); err != nil {
			return marshalToolResult(map[string]any{
				"habit_id": id, "status": "created", "schedule_warning": err.Error(),
			})
		}
	}
	return marshalToolResult(map[string]any{"habit_id": id, "status": "created_and_scheduled"})
}

type updateHabitInput struct {
	HabitID          int64    `json:"habit_id"`
	Title            string   `json:"title"`
	TargetCalendarID int64    `json:"target_calendar_id"`
	DurationMinutes  int      `json:"duration_minutes"`
	IdealTime        string   `json:"ideal_time"`
	FlexMinutes      *int     `json:"flex_minutes,omitempty"`
	DaysOfWeek       []string `json:"days_of_week"`
	HoursKind        string   `json:"hours_kind"`
	HorizonDays      int      `json:"horizon_days"`
	CategoryID       *int64   `json:"category_id,omitempty"`
	Enabled          *bool    `json:"enabled,omitempty"`
}

func (t *Toolbox) updateHabit(ctx context.Context, input json.RawMessage) (string, error) {
	var p updateHabitInput
	if err := json.Unmarshal(input, &p); err != nil {
		return "", err
	}
	if p.HabitID == 0 {
		return "", fmt.Errorf("habit_id is required")
	}
	h, err := t.habits.Get(ctx, p.HabitID)
	if err != nil || h == nil {
		return "", fmt.Errorf("habit #%d not found", p.HabitID)
	}
	if p.Title != "" {
		h.Title = strings.TrimSpace(p.Title)
	}
	if p.TargetCalendarID != 0 {
		h.TargetCalendarID = p.TargetCalendarID
	}
	if p.DurationMinutes > 0 {
		h.DurationMinutes = p.DurationMinutes
	}
	if p.IdealTime != "" {
		h.IdealTime = strings.TrimSpace(p.IdealTime)
	}
	if p.FlexMinutes != nil {
		h.FlexMinutes = *p.FlexMinutes
	}
	if len(p.DaysOfWeek) > 0 {
		h.DaysOfWeek = cleanDows(p.DaysOfWeek)
	}
	if p.HoursKind != "" {
		h.HoursKind = p.HoursKind
	}
	if p.HorizonDays > 0 {
		h.HorizonDays = p.HorizonDays
	}
	if p.CategoryID != nil {
		h.CategoryID = p.CategoryID
	}
	if p.Enabled != nil {
		h.Enabled = *p.Enabled
	}
	// Drop today-and-future occurrences so the scheduler rebuilds under new rules.
	_ = t.occurrences.DeleteFromDate(ctx, h.ID, time.Now())
	if err := t.habits.Update(ctx, h); err != nil {
		return "", err
	}
	t.logAudit(ctx, "update_habit", "", fmt.Sprintf("updated habit #%d", h.ID))
	if t.scheduler != nil && h.Enabled {
		if err := t.scheduler.PlaceHabit(ctx, h.ID); err != nil {
			return marshalToolResult(map[string]any{
				"habit_id": h.ID, "status": "updated", "schedule_warning": err.Error(),
			})
		}
	}
	return marshalToolResult(map[string]any{"habit_id": h.ID, "status": "updated"})
}

type habitIDOnlyInput struct {
	HabitID int64 `json:"habit_id"`
}

func (t *Toolbox) deleteHabit(ctx context.Context, input json.RawMessage) (string, error) {
	var p habitIDOnlyInput
	if err := json.Unmarshal(input, &p); err != nil {
		return "", err
	}
	h, err := t.habits.Get(ctx, p.HabitID)
	if err != nil || h == nil {
		return "", fmt.Errorf("habit #%d not found", p.HabitID)
	}
	occ, _ := t.occurrences.ListByHabit(ctx, h.ID)
	if cal, err := t.calendars.Get(ctx, h.TargetCalendarID); err == nil {
		if cli, err := t.clientFor(ctx, cal.AccountID); err == nil {
			for _, o := range occ {
				_ = cli.DeleteEvent(ctx, cal.GoogleCalendarID, o.TargetEventID)
			}
		}
	}
	if err := t.habits.Delete(ctx, p.HabitID); err != nil {
		return "", err
	}
	t.logAudit(ctx, "delete_habit", "", fmt.Sprintf("deleted habit #%d", p.HabitID))
	return marshalToolResult(map[string]any{"habit_id": p.HabitID, "status": "deleted"})
}

// cleanDows lowercases + dedupes weekday strings, keeping only valid keys.
func cleanDows(in []string) []string {
	valid := map[string]bool{
		"mon": true, "tue": true, "wed": true, "thu": true,
		"fri": true, "sat": true, "sun": true,
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, d := range in {
		d = strings.ToLower(strings.TrimSpace(d))
		if !valid[d] || seen[d] {
			continue
		}
		seen[d] = true
		out = append(out, d)
	}
	return out
}
