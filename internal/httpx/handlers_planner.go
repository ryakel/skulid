package httpx

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
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

// PlannerView enumerates the supported view modes. Day/3-day/week share the
// timeline template; month renders to a 6×7 grid.
const (
	ViewDay   = "day"
	ViewThree = "3day"
	ViewWeek  = "week"
	ViewMonth = "month"
)

// dayCountForView returns the number of timeline columns for the timeline
// views, or 0 for month (which is rendered as a separate grid).
func dayCountForView(v string) int {
	switch v {
	case ViewDay:
		return 1
	case ViewThree:
		return 3
	case ViewMonth:
		return 0
	default:
		return 7
	}
}

type plannerEvent struct {
	Title         string
	Start         time.Time
	End           time.Time
	StartLabel    string  // "9:00 AM"
	DurationLabel string  // "30m"
	CategorySlug  string
	CategoryName  string
	CategoryColor string
	// CalendarColor drives the actual visual rendering — different calendars
	// get visually distinct events. Falls back to the category color when a
	// calendar has no color set.
	CalendarColor string
	CalendarName  string
	TopPct        float64
	HeightPct     float64
	// Short marks events ≤ 30 min so the template renders without the meta
	// line (which doesn't fit in tiny boxes).
	Short         bool
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
	DayNum    int    // day-of-month, used by month-grid cells
	IsToday   bool
	InMonth   bool   // true unless rendered in month view as a leading/trailing spillover cell
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
	weekStartDay := s.plannerWeekStartDay(ctx)
	view := s.resolveView(ctx, r.URL.Query().Get("view"))
	anchor := parseAnchor(r.URL.Query().Get("at"), loc)
	// Backwards compat: an old `?w=` query still works for the week view.
	if w := r.URL.Query().Get("w"); w != "" && view == ViewWeek {
		anchor = parseAnchor(w, loc)
	}

	rangeStart, rangeEnd, prevAt, nextAt, label := computeViewWindow(view, anchor, loc, weekStartDay)

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

	// Pull events per calendar — skip disabled ones. Sequential; ~20 cals is fine.
	var allEvents []*gcal.Event
	calByEvent := map[*gcal.Event]db.Calendar{}
	for _, cal := range calendars {
		if !cal.Enabled {
			continue
		}
		cli, err := s.ClientFor(ctx, cal.AccountID)
		if err != nil {
			continue
		}
		resp, err := cli.Service().Events.List(cal.GoogleCalendarID).
			Context(ctx).SingleEvents(true).
			TimeMin(rangeStart.Format(time.RFC3339)).
			TimeMax(rangeEnd.Format(time.RFC3339)).
			MaxResults(500).OrderBy("startTime").Do()
		if err != nil {
			continue
		}
		for _, ev := range resp.Items {
			allEvents = append(allEvents, ev)
			calByEvent[ev] = cal
		}
	}

	// Build per-day buckets across the visible range.
	days := buildDays(rangeStart, rangeEnd, loc)
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
			placeAllDay(days, ev, slug, cat.Name, cat.Color, cal.Color, cal.Summary, loc)
			continue
		}
		start, end, ok := timedBounds(ev, loc)
		if !ok {
			continue
		}
		// Skip events entirely outside the visible range.
		if !end.After(rangeStart) || !start.Before(rangeEnd) {
			continue
		}
		clipS, clipE := start, end
		if clipS.Before(rangeStart) {
			clipS = rangeStart
		}
		if clipE.After(rangeEnd) {
			clipE = rangeEnd
		}
		totals[slug] += clipE.Sub(clipS).Hours()

		placeTimed(days, start, end, ev.Summary, slug, cat.Name, cat.Color, cal.Color, cal.Summary, loc)
	}

	// Sort each day's timed events and assign overlap lanes.
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

	type weekStartOpt struct {
		Value int
		Label string
	}
	weekStartOpts := make([]weekStartOpt, 7)
	for i := 0; i < 7; i++ {
		weekStartOpts[i] = weekStartOpt{Value: i, Label: weekDayNames[i]}
	}
	tzSetting, _, _ := s.Settings.Get(ctx, db.SettingPlannerTimezone)
	defaultViewSetting, _, _ := s.Settings.Get(ctx, db.SettingPlannerDefaultView)

	// Month view needs a 7-cell weekday header keyed off weekStartDay.
	weekHeader := make([]string, 7)
	for i := 0; i < 7; i++ {
		weekHeader[i] = weekDayShortNames[(weekStartDay+i)%7]
	}

	data := s.pageData(r, "Planner")
	data["View"] = view
	data["DayCount"] = dayCountForView(view)
	data["RangeStart"] = rangeStart
	data["RangeLabel"] = label
	data["Days"] = days
	data["HourLabels"] = hourLabels
	data["CategoryTotals"] = catTotals
	data["PrevAt"] = prevAt.Format("2006-01-02")
	data["NextAt"] = nextAt.Format("2006-01-02")
	data["TimeZone"] = loc.String()
	data["TimeZoneSetting"] = tzSetting
	data["WeekStartDay"] = weekStartDay
	data["WeekStartOptions"] = weekStartOpts
	data["DefaultViewSetting"] = defaultViewSetting
	data["WeekHeader"] = weekHeader
	data["ViewOptions"] = []struct{ Value, Label string }{
		{ViewDay, "Day"},
		{ViewThree, "3 days"},
		{ViewWeek, "Week"},
		{ViewMonth, "Month"},
	}
	s.render(w, "planner", data)
}

// resolveView returns the requested view, falling back to the persisted
// default and then "week". Unknown values get clamped to "week".
func (s *Server) resolveView(ctx context.Context, q string) string {
	v := strings.TrimSpace(q)
	if v == "" {
		v, _, _ = s.Settings.Get(ctx, db.SettingPlannerDefaultView)
		v = strings.TrimSpace(v)
	}
	switch v {
	case ViewDay, ViewThree, ViewWeek, ViewMonth:
		return v
	}
	return ViewWeek
}

// parseAnchor reads a YYYY-MM-DD anchor in the planner's timezone, or returns
// today (in that timezone) when the input is empty/unparseable.
func parseAnchor(s string, loc *time.Location) time.Time {
	if s != "" {
		if t, err := time.ParseInLocation("2006-01-02", s, loc); err == nil {
			return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
		}
	}
	now := time.Now().In(loc)
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
}

// computeViewWindow returns the [start, end) range to fetch events for, the
// prev/next anchor dates for the navigation buttons, and a human label for
// the page header. start is always at 00:00 in loc; end is exclusive.
func computeViewWindow(view string, anchor time.Time, loc *time.Location, weekStartDay int) (start, end, prev, next time.Time, label string) {
	switch view {
	case ViewDay:
		start = anchor
		end = anchor.AddDate(0, 0, 1)
		prev = anchor.AddDate(0, 0, -1)
		next = anchor.AddDate(0, 0, 1)
		label = anchor.Format("Mon, Jan 2, 2006")
	case ViewThree:
		start = anchor
		end = anchor.AddDate(0, 0, 3)
		prev = anchor.AddDate(0, 0, -3)
		next = anchor.AddDate(0, 0, 3)
		label = anchor.Format("Jan 2") + " – " + anchor.AddDate(0, 0, 2).Format("Jan 2, 2006")
	case ViewMonth:
		// 6×7 grid spanning the month containing `anchor`. Pre-roll back to the
		// nearest weekStartDay before the 1st so the grid lines up.
		monthStart := time.Date(anchor.Year(), anchor.Month(), 1, 0, 0, 0, 0, loc)
		wday := int(monthStart.Weekday())
		preroll := (wday - weekStartDay + 7) % 7
		start = monthStart.AddDate(0, 0, -preroll)
		end = start.AddDate(0, 0, 42)
		prev = monthStart.AddDate(0, -1, 0)
		next = monthStart.AddDate(0, 1, 0)
		label = monthStart.Format("January 2006")
	default: // ViewWeek
		start = plannerWeekStart(anchor, "", loc, weekStartDay)
		end = start.AddDate(0, 0, 7)
		prev = start.AddDate(0, 0, -7)
		next = start.AddDate(0, 0, 7)
		label = start.Format("Jan 2") + " – " + end.AddDate(0, 0, -1).Format("Jan 2, 2006")
	}
	return
}

// handlePlannerPrefs saves the planner timezone + week-start preferences.
// Both are validated; bad TZ values are rejected (rather than silently kept
// and falling back to UTC at next render). Redirect back to /planner so the
// user sees the new layout immediately.
func (s *Server) handlePlannerPrefs(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	tz := strings.TrimSpace(r.FormValue("timezone"))
	if tz != "" {
		if _, err := time.LoadLocation(tz); err != nil {
			http.Error(w, "invalid timezone (must be IANA, e.g. America/Chicago): "+err.Error(), http.StatusBadRequest)
			return
		}
	}
	if err := s.Settings.Set(r.Context(), db.SettingPlannerTimezone, tz); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	weekStart := strings.TrimSpace(r.FormValue("week_start"))
	if weekStart != "" {
		n, err := strconv.Atoi(weekStart)
		if err != nil || n < 0 || n > 6 {
			http.Error(w, "week_start must be 0..6 (Sun..Sat)", http.StatusBadRequest)
			return
		}
	}
	if err := s.Settings.Set(r.Context(), db.SettingPlannerWeekStart, weekStart); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defaultView := strings.TrimSpace(r.FormValue("default_view"))
	switch defaultView {
	case "", ViewDay, ViewThree, ViewWeek, ViewMonth:
	default:
		http.Error(w, "default_view must be one of: day, 3day, week, month", http.StatusBadRequest)
		return
	}
	if err := s.Settings.Set(r.Context(), db.SettingPlannerDefaultView, defaultView); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/planner", http.StatusFound)
}

// weekDayName maps a 0..6 weekday to the display label used in the prefs
// form. Sunday=0 per Go's time.Weekday.
var weekDayNames = []string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}
var weekDayShortNames = []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}

// plannerLocation resolves the timezone the planner renders in. Priority:
//
//  1. Explicit operator setting (`planner_timezone`, settable from the
//     planner header form).
//  2. First connected account's working-hours timezone.
//  3. UTC.
func (s *Server) plannerLocation(ctx context.Context) *time.Location {
	if v, ok, _ := s.Settings.Get(ctx, db.SettingPlannerTimezone); ok && strings.TrimSpace(v) != "" {
		if loc, err := time.LoadLocation(strings.TrimSpace(v)); err == nil {
			return loc
		}
	}
	accts, _ := s.Accounts.List(ctx)
	for _, a := range accts {
		if len(a.WorkingHours) == 0 {
			continue
		}
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

// plannerWeekStartDay returns the configured week-start weekday (0=Sun..6=Sat).
// Defaults to Monday (1) when unset/invalid.
func (s *Server) plannerWeekStartDay(ctx context.Context) int {
	v, ok, _ := s.Settings.Get(ctx, db.SettingPlannerWeekStart)
	if !ok {
		return 1
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < 0 || n > 6 {
		return 1
	}
	return n
}

// plannerWeekStart parses the ?w=YYYY-MM-DD query param and snaps to the
// most recent occurrence of weekStartDay (0=Sun..6=Sat). With no param,
// snaps from "now".
func plannerWeekStart(now time.Time, w string, loc *time.Location, weekStartDay int) time.Time {
	if w != "" {
		if t, err := time.ParseInLocation("2006-01-02", w, loc); err == nil {
			now = t
		}
	}
	if weekStartDay < 0 || weekStartDay > 6 {
		weekStartDay = 1
	}
	wday := int(now.Weekday())
	daysBack := (wday - weekStartDay + 7) % 7
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	return day.AddDate(0, 0, -daysBack)
}

// buildDays builds one plannerDay per calendar day in [start, end). Used for
// every view — day (1 day), 3-day (3), week (7), month (42).
func buildDays(start, end time.Time, loc *time.Location) []plannerDay {
	today := time.Now().In(loc)
	todayStart := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, loc)
	// Use the anchor month to stamp `InMonth` for month-view cells. For
	// timeline views the flag is unused and stays true.
	anchorMonth := start.Month()
	if end.Sub(start) > 8*24*time.Hour {
		// Month view spans 42 days; use the middle of the range to identify
		// the "current" month (so month-spillover at start/end is correctly
		// flagged out-of-month).
		mid := start.Add(end.Sub(start) / 2)
		anchorMonth = mid.Month()
	}
	var out []plannerDay
	for d := start; d.Before(end); d = d.AddDate(0, 0, 1) {
		out = append(out, plannerDay{
			Date:      d,
			Label:     d.Format("Mon"),
			DateLabel: d.Format("Jan 2"),
			DayNum:    d.Day(),
			IsToday:   d.Equal(todayStart),
			InMonth:   d.Month() == anchorMonth,
		})
	}
	return out
}

func placeTimed(days []plannerDay, start, end time.Time, title, slug, catName, catColor, calColor, calName string, loc *time.Location) {
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
		// Use calendar color when present (different calendars look distinct);
		// fall back to category color so events still get tinted when a
		// calendar has no color set.
		eventColor := calColor
		if eventColor == "" {
			eventColor = catColor
		}
		days[i].Timed = append(days[i].Timed, plannerEvent{
			Title:         title,
			Start:         clipS,
			End:           clipE,
			StartLabel:    clipS.In(loc).Format("3:04 PM"),
			DurationLabel: durationLabel(clipE.Sub(clipS)),
			CategorySlug:  slug,
			CategoryName:  catName,
			CategoryColor: catColor,
			CalendarColor: eventColor,
			CalendarName:  calName,
			TopPct:        100 * topMins / float64(plannerSpanMins),
			HeightPct:     100 * heightMins / float64(plannerSpanMins),
			Short:         clipE.Sub(clipS) <= 30*time.Minute,
		})
	}
}

func placeAllDay(days []plannerDay, ev *gcal.Event, slug, catName, catColor, calColor, calName string, loc *time.Location) {
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
	eventColor := calColor
	if eventColor == "" {
		eventColor = catColor
	}
	for i := range days {
		d := days[i].Date
		if !d.Before(startDate) && d.Before(endDate) {
			days[i].AllDay = append(days[i].AllDay, plannerEvent{
				Title:         ev.Summary,
				CategorySlug:  slug,
				CategoryName:  catName,
				CategoryColor: catColor,
				CalendarColor: eventColor,
				CalendarName:  calName,
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

