package httpx

import (
	"net/http"
	"time"

	"github.com/ryakel/skulid/internal/db"
)

// Reclaim-equivalent priority palette. Kept here (not in the DB) because
// these are the four built-in buckets the Kanban renders against; users
// can't add new priorities, only retitle the categories.
var priorityColumns = []struct {
	Slug, Name, Color string
}{
	{db.PriorityCritical, "Critical", "#d64545"},
	{db.PriorityHigh, "High", "#b86e00"},
	{db.PriorityMedium, "Medium", "#5b6cff"},
	{db.PriorityLow, "Low", "#6b7286"},
}

type prioCard struct {
	ID              int64
	Title           string
	DurationMinutes int
	Status          string
	DueLabel        string
	ScheduledLabel  string
}

type prioCol struct {
	Slug, Name, Color string
	Cards             []prioCard
	Count             int
}

func (s *Server) handlePrioritiesPage(w http.ResponseWriter, r *http.Request) {
	tasks, err := s.Tasks.ListAllActive(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	byPrio := map[string][]prioCard{}
	for _, t := range tasks {
		byPrio[t.Priority] = append(byPrio[t.Priority], prioCard{
			ID:              t.ID,
			Title:           t.Title,
			DurationMinutes: t.DurationMinutes,
			Status:          t.Status,
			DueLabel:        formatRelative(t.DueAt),
			ScheduledLabel:  formatScheduled(t.ScheduledStartsAt),
		})
	}
	cols := make([]prioCol, 0, len(priorityColumns))
	for _, p := range priorityColumns {
		cards := byPrio[p.Slug]
		cols = append(cols, prioCol{
			Slug: p.Slug, Name: p.Name, Color: p.Color,
			Cards: cards, Count: len(cards),
		})
	}
	data := s.pageData(r, "Priorities")
	data["Columns"] = cols
	s.render(w, "priorities", data)
}

// formatRelative returns "today", "tomorrow", or a "Mon Apr 27" date label
// for a due date. Empty string when t is nil.
func formatRelative(t *time.Time) string {
	if t == nil || t.IsZero() {
		return ""
	}
	now := time.Now()
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	switch day.Sub(today) {
	case 0:
		return "today"
	case 24 * time.Hour:
		return "tomorrow"
	}
	return t.Format("Mon Jan 2")
}

// formatScheduled returns "Mon 9:00 AM" for a scheduled start time, or "" when
// the task hasn't been placed yet.
func formatScheduled(t *time.Time) string {
	if t == nil || t.IsZero() {
		return ""
	}
	return t.Format("Mon 3:04 PM")
}
