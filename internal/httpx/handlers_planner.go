package httpx

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"time"

	gcal "google.golang.org/api/calendar/v3"

	"github.com/ryakel/skulid/internal/category"
	"github.com/ryakel/skulid/internal/db"
)

// plannerWindow is the visible vertical range each day. 6am-10pm covers most
// people's working + personal commitment hours without making blocks too tiny.
const (
	plannerStartHour = 6
	plannerEndHour   = 22
	plannerSpanMins  = (plannerEndHour - plannerStartHour) * 60
	plannerTimeout   = 90 * time.Second
)

type plannerEvent struct {
	Title         string
	Start         time.Time
	End           time.Time
	StartLabel    string  // "9:00 AM"
	DurationLabel string  // "30m"
	CategorySlug  string
	CategoryName  string
	CategoryColor string
	TopPct        float64
	HeightPct     float64
	// Lane / Lanes describe horizontal placement when events overlap. Lane is
	// 0-indexed; Lanes is the cluster's max concurrent count. A solo event
	// renders at Lane=0, Lanes=1 → full column width.
	Lane          int
	Lanes         int
}

type plannerDay struct {
	Date      time.Time
	Label     string // "Mon"
	DateLabel string // "Apr 27"
	IsToday   bool
	AllDay    []plannerEvent
	Timed     []plannerEvent
}

type plannerCategoryTotal struct {
	Slug    string
	Name    string
	Color   string
	Hours   float64 // total hours scheduled this week
}

func (s *Server) handlePlannerPage(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), plannerTimeout)
	defer cancel()

	loc := s.plannerLocation(ctx)
	weekStart := plannerWeekStart(time.Now().In(loc), r.URL.Query().Get("w"), loc)
	weekEnd := weekStart.AddDate(0, 0, 7)

	cats, _ := s.Categories.List(ctx)
	catBySlug := map[string]db.Category{}
	for _, c := range cats {
		catBySlug[c.Slug] = c
	}

	accounts, _ := s.Accounts.List(ctx)
	emails := make([]string, 0, len(accounts))
	for _, a := range accounts {
		emails = append(emails, a.Email)
	}
	ownerDomains := category.DomainsFromEmails(emails)

	calendars, _ := s.Calendars.ListAll(ctx)

	// Pull events per calendar in parallel-ish (sequential for simplicity).
	var allEvents []*gcal.Event
	calByEvent := map[*gcal.Event]db.Calendar{}
	for _, cal := range calendars {
		cli, err := s.ClientFor(ctx, cal.AccountID)
		if err != nil {
			continue
		}
		resp, err := cli.Service().Events.List(cal.GoogleCalendarID).
			Context(ctx).SingleEvents(true).
			TimeMin(weekStart.Format(time.RFC3339)).
			TimeMax(weekEnd.Format(time.RFC3339)).
			MaxResults(250).OrderBy("startTime").Do()
		if err != nil {
			continue
		}
		for _, ev := range resp.Items {
			allEvents = append(allEvents, ev)
			calByEvent[ev] = cal
		}
	}

	// Build per-day buckets and weekly totals.
	days := buildDays(weekStart, loc)
	totals := map[string]float64{}
	for _, ev := range allEvents {
		if ev.Status == "cancelled" {
			continue
		}
		cal := calByEvent[ev]
		ctx := category.Context{
			OwnerDomains:        ownerDomains,
			CalendarDefaultSlug: defaultCategorySlug(cal, catBySlug),
		}
		slug := category.Classify(ev, ctx)
		cat := catBySlug[slug]
		isAllDay := ev.Start != nil && ev.Start.DateTime == "" && ev.Start.Date != ""
		if isAllDay {
			placeAllDay(days, ev, slug, cat.Name, cat.Color, loc)
			continue
		}
		start, end, ok := timedBounds(ev, loc)
		if !ok {
			continue
		}
		// Skip events entirely outside the week (multi-week recurrences clipped).
		if !end.After(weekStart) || !start.Before(weekEnd) {
			continue
		}
		// Tally hour totals (clip to visible week).
		clipS, clipE := start, end
		if clipS.Before(weekStart) {
			clipS = weekStart
		}
		if clipE.After(weekEnd) {
			clipE = weekEnd
		}
		totals[slug] += clipE.Sub(clipS).Hours()

		placeTimed(days, start, end, ev.Summary, slug, cat.Name, cat.Color, loc)
	}

	// Sort each day's timed events and assign overlap lanes so concurrent
	// events render side-by-side instead of stacking unreadably.
	for i := range days {
		sort.Slice(days[i].Timed, func(a, b int) bool {
			return days[i].Timed[a].Start.Before(days[i].Timed[b].Start)
		})
		assignEventLanes(days[i].Timed)
	}

	// Build the totals strip in category sort order.
	sort.SliceStable(cats, func(i, j int) bool { return cats[i].SortOrder < cats[j].SortOrder })
	catTotals := make([]plannerCategoryTotal, 0, len(cats))
	for _, c := range cats {
		h := totals[c.Slug]
		if h == 0 {
			continue
		}
		catTotals = append(catTotals, plannerCategoryTotal{
			Slug: c.Slug, Name: c.Name, Color: c.Color, Hours: round1(h),
		})
	}

	hourLabels := make([]string, 0, plannerEndHour-plannerStartHour+1)
	for h := plannerStartHour; h <= plannerEndHour; h++ {
		hourLabels = append(hourLabels, formatHour(h))
	}

	data := s.pageData(r, "Planner")
	data["WeekStart"] = weekStart
	data["WeekStartLabel"] = weekStart.Format("Jan 2")
	data["WeekEndLabel"] = weekEnd.AddDate(0, 0, -1).Format("Jan 2, 2006")
	data["Days"] = days
	data["HourLabels"] = hourLabels
	data["CategoryTotals"] = catTotals
	data["PrevWeek"] = weekStart.AddDate(0, 0, -7).Format("2006-01-02")
	data["NextWeek"] = weekStart.AddDate(0, 0, 7).Format("2006-01-02")
	data["TodayWeek"] = ""
	s.render(w, "planner", data)
}

func (s *Server) plannerLocation(ctx context.Context) *time.Location {
	// Prefer the first account's working-hours timezone; fall back to UTC.
	accts, _ := s.Accounts.List(ctx)
	for _, a := range accts {
		if len(a.WorkingHours) == 0 {
			continue
		}
		// minimal parse — only need the time_zone field.
		var probe struct {
			TimeZone string `json:"time_zone"`
		}
		if err := json.Unmarshal(a.WorkingHours, &probe); err == nil && probe.TimeZone != "" {
			if loc, err := time.LoadLocation(probe.TimeZone); err == nil {
				return loc
			}
		}
	}
	return time.UTC
}

// plannerWeekStart parses the ?w=YYYY-MM-DD query param and snaps to the
// previous Monday. With no param, snaps from "now".
func plannerWeekStart(now time.Time, w string, loc *time.Location) time.Time {
	if w != "" {
		if t, err := time.ParseInLocation("2006-01-02", w, loc); err == nil {
			now = t
		}
	}
	// Go's time.Weekday: Sunday=0, Monday=1, ... Saturday=6. Snap to Monday.
	wday := int(now.Weekday())
	if wday == 0 {
		wday = 7 // Sunday becomes day 7 so Mon-Sun ordering works.
	}
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	return day.AddDate(0, 0, -(wday - 1))
}

func buildDays(weekStart time.Time, loc *time.Location) []plannerDay {
	out := make([]plannerDay, 7)
	today := time.Now().In(loc)
	todayStart := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, loc)
	for i := 0; i < 7; i++ {
		d := weekStart.AddDate(0, 0, i)
		out[i] = plannerDay{
			Date:      d,
			Label:     d.Format("Mon"),
			DateLabel: d.Format("Jan 2"),
			IsToday:   d.Equal(todayStart),
		}
	}
	return out
}

func placeTimed(days []plannerDay, start, end time.Time, title, slug, catName, catColor string, loc *time.Location) {
	// Iterate days the event covers. For each day, clip to the planner window
	// and emit a block; this handles overnight events cleanly.
	for i := range days {
		dayStart := days[i].Date
		dayEnd := dayStart.AddDate(0, 0, 1)
		if !end.After(dayStart) || !start.Before(dayEnd) {
			continue
		}
		clipS := start
		clipE := end
		if clipS.Before(dayStart) {
			clipS = dayStart
		}
		if clipE.After(dayEnd) {
			clipE = dayEnd
		}
		windowStart := dayStart.Add(time.Duration(plannerStartHour) * time.Hour)
		windowEnd := dayStart.Add(time.Duration(plannerEndHour) * time.Hour)
		if !clipE.After(windowStart) || !clipS.Before(windowEnd) {
			// Outside the visible 6am-10pm — drop. (Could spill into a "before
			// 6am / after 10pm" indicator in a future iteration.)
			continue
		}
		visS := clipS
		visE := clipE
		if visS.Before(windowStart) {
			visS = windowStart
		}
		if visE.After(windowEnd) {
			visE = windowEnd
		}
		topMins := visS.Sub(windowStart).Minutes()
		heightMins := visE.Sub(visS).Minutes()
		days[i].Timed = append(days[i].Timed, plannerEvent{
			Title:         title,
			Start:         clipS,
			End:           clipE,
			StartLabel:    clipS.In(loc).Format("3:04 PM"),
			DurationLabel: durationLabel(clipE.Sub(clipS)),
			CategorySlug:  slug,
			CategoryName:  catName,
			CategoryColor: catColor,
			TopPct:        100 * topMins / float64(plannerSpanMins),
			HeightPct:     100 * heightMins / float64(plannerSpanMins),
		})
	}
}

func placeAllDay(days []plannerDay, ev *gcal.Event, slug, catName, catColor string, loc *time.Location) {
	if ev.Start == nil || ev.End == nil {
		return
	}
	startDate, err := time.ParseInLocation("2006-01-02", ev.Start.Date, loc)
	if err != nil {
		return
	}
	endDate, err := time.ParseInLocation("2006-01-02", ev.End.Date, loc)
	if err != nil {
		// Some all-day events arrive without an end; assume same day.
		endDate = startDate.AddDate(0, 0, 1)
	}
	for i := range days {
		d := days[i].Date
		if !d.Before(startDate) && d.Before(endDate) {
			days[i].AllDay = append(days[i].AllDay, plannerEvent{
				Title:         ev.Summary,
				CategorySlug:  slug,
				CategoryName:  catName,
				CategoryColor: catColor,
			})
		}
	}
}

func timedBounds(ev *gcal.Event, loc *time.Location) (time.Time, time.Time, bool) {
	if ev.Start == nil || ev.End == nil || ev.Start.DateTime == "" || ev.End.DateTime == "" {
		return time.Time{}, time.Time{}, false
	}
	start, err := time.Parse(time.RFC3339, ev.Start.DateTime)
	if err != nil {
		return time.Time{}, time.Time{}, false
	}
	end, err := time.Parse(time.RFC3339, ev.End.DateTime)
	if err != nil {
		return time.Time{}, time.Time{}, false
	}
	return start.In(loc), end.In(loc), true
}

func defaultCategorySlug(cal db.Calendar, byID map[string]db.Category) string {
	// We don't have direct access to the calendar's default_category_id slug
	// without a join; for now, leave as empty so the categorizer's heuristics
	// take over. (A future commit can wire calendar.default_category_id ->
	// category.slug here.)
	_ = cal
	_ = byID
	return ""
}

// assignEventLanes lays overlapping events out in side-by-side lanes within
// each "cluster" of transitively-overlapping events. Standard calendar
// algorithm: sweep through start-sorted events, group anything that overlaps
// the running cluster end into a cluster, then greedy-assign each event to
// the first lane whose previous event has already ended. Cluster width =
// max concurrent lanes; every event in the cluster gets that count so the
// CSS width math works.
func assignEventLanes(events []plannerEvent) {
	if len(events) == 0 {
		return
	}
	flush := func(start, end int) {
		var laneEnds []time.Time
		for k := start; k < end; k++ {
			placed := -1
			for li, le := range laneEnds {
				if !le.After(events[k].Start) {
					placed = li
					laneEnds[li] = events[k].End
					break
				}
			}
			if placed == -1 {
				placed = len(laneEnds)
				laneEnds = append(laneEnds, events[k].End)
			}
			events[k].Lane = placed
		}
		total := len(laneEnds)
		if total < 1 {
			total = 1
		}
		for k := start; k < end; k++ {
			events[k].Lanes = total
		}
	}

	clusterStart := 0
	clusterEnd := events[0].End
	for i := 1; i < len(events); i++ {
		if events[i].Start.Before(clusterEnd) {
			if events[i].End.After(clusterEnd) {
				clusterEnd = events[i].End
			}
			continue
		}
		flush(clusterStart, i)
		clusterStart = i
		clusterEnd = events[i].End
	}
	flush(clusterStart, len(events))
}

func formatHour(h int) string {
	switch {
	case h == 0:
		return "12 AM"
	case h < 12:
		return fmt.Sprintf("%d AM", h)
	case h == 12:
		return "12 PM"
	default:
		return fmt.Sprintf("%d PM", h-12)
	}
}

func durationLabel(d time.Duration) string {
	mins := int(d.Minutes())
	if mins < 60 {
		return fmt.Sprintf("%dm", mins)
	}
	h := mins / 60
	m := mins % 60
	if m == 0 {
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dh%dm", h, m)
}

func round1(v float64) float64 {
	return float64(int(v*10+0.5)) / 10
}

