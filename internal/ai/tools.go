package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	gcal "google.golang.org/api/calendar/v3"

	"github.com/ryakel/skulid/internal/calendar"
	"github.com/ryakel/skulid/internal/db"
	syncengine "github.com/ryakel/skulid/internal/sync"
)

// PropAISession stamps every assistant-driven write so it's attributable later
// and the rule engine still treats it as managed (via PropManaged).
const PropAISession = "skulidAiSession"

// Toolbox bundles the dependencies a tool executor needs.
type Toolbox struct {
	accounts       *db.AccountRepo
	calendars      *db.CalendarRepo
	audit          *db.AuditRepo
	clientFor      syncengine.ClientFor
	conversationID int64
}

func NewToolbox(accounts *db.AccountRepo, calendars *db.CalendarRepo, audit *db.AuditRepo,
	clientFor syncengine.ClientFor, conversationID int64) *Toolbox {
	return &Toolbox{
		accounts:       accounts,
		calendars:      calendars,
		audit:          audit,
		clientFor:      clientFor,
		conversationID: conversationID,
	}
}

// writeTools is the set of tools that must NOT auto-execute. The agent loop
// stages these as ai_pending_action rows and waits for user confirmation.
var writeTools = map[string]bool{
	"create_event": true,
	"update_event": true,
	"delete_event": true,
	"move_event":   true,
}

// IsWrite reports whether the named tool requires confirmation.
func IsWrite(name string) bool { return writeTools[name] }

// Defs returns the tool definitions advertised to Claude.
func Defs() []ToolDef {
	mk := func(s string) json.RawMessage { return json.RawMessage(s) }
	return []ToolDef{
		{
			Name:        "list_calendars",
			Description: "List every calendar the daemon has access to. Returns each calendar's internal id (use this in subsequent tool calls), its summary, owning account email, IANA time zone, color, primary flag, and Google calendar id.",
			InputSchema: mk(`{"type":"object","properties":{},"additionalProperties":false}`),
		},
		{
			Name:        "list_events",
			Description: "List events on one calendar within a time range. Returns event id, summary, start, end, location, description, attendees. Use the calendar's internal id from list_calendars.",
			InputSchema: mk(`{
				"type":"object",
				"properties":{
					"calendar_id":{"type":"integer","description":"internal calendar id from list_calendars"},
					"time_min":{"type":"string","description":"RFC3339 lower bound (inclusive)"},
					"time_max":{"type":"string","description":"RFC3339 upper bound (exclusive)"},
					"q":{"type":"string","description":"optional free-text query (matches event summary/description)"}
				},
				"required":["calendar_id","time_min","time_max"],
				"additionalProperties":false
			}`),
		},
		{
			Name:        "find_event",
			Description: "Search for events across every calendar by free-text query. Returns matching events along with their calendar_id.",
			InputSchema: mk(`{
				"type":"object",
				"properties":{
					"query":{"type":"string","description":"free-text query, matched against event summary"},
					"time_min":{"type":"string","description":"optional RFC3339 lower bound; defaults to 7 days ago"},
					"time_max":{"type":"string","description":"optional RFC3339 upper bound; defaults to 90 days from now"}
				},
				"required":["query"],
				"additionalProperties":false
			}`),
		},
		{
			Name:        "find_free_time",
			Description: "Find free windows of at least duration_minutes across the listed calendars within [time_min, time_max]. Pure read; safe to call freely.",
			InputSchema: mk(`{
				"type":"object",
				"properties":{
					"duration_minutes":{"type":"integer","minimum":5},
					"calendar_ids":{"type":"array","items":{"type":"integer"}},
					"time_min":{"type":"string"},
					"time_max":{"type":"string"}
				},
				"required":["duration_minutes","calendar_ids","time_min","time_max"],
				"additionalProperties":false
			}`),
		},
		{
			Name:        "create_event",
			Description: "Stage the creation of a new event. NOT executed until the user confirms in the UI.",
			InputSchema: mk(`{
				"type":"object",
				"properties":{
					"calendar_id":{"type":"integer"},
					"summary":{"type":"string"},
					"start":{"type":"string","description":"RFC3339"},
					"end":{"type":"string","description":"RFC3339"},
					"time_zone":{"type":"string","description":"IANA, optional"},
					"location":{"type":"string"},
					"description":{"type":"string"},
					"attendees":{"type":"array","items":{"type":"string"}}
				},
				"required":["calendar_id","summary","start","end"],
				"additionalProperties":false
			}`),
		},
		{
			Name:        "update_event",
			Description: "Stage an update to an existing event. Only the supplied fields are changed. NOT executed until the user confirms.",
			InputSchema: mk(`{
				"type":"object",
				"properties":{
					"calendar_id":{"type":"integer"},
					"event_id":{"type":"string"},
					"summary":{"type":"string"},
					"start":{"type":"string"},
					"end":{"type":"string"},
					"time_zone":{"type":"string"},
					"location":{"type":"string"},
					"description":{"type":"string"},
					"attendees":{"type":"array","items":{"type":"string"}}
				},
				"required":["calendar_id","event_id"],
				"additionalProperties":false
			}`),
		},
		{
			Name:        "delete_event",
			Description: "Stage the deletion of an event. NOT executed until the user confirms.",
			InputSchema: mk(`{
				"type":"object",
				"properties":{
					"calendar_id":{"type":"integer"},
					"event_id":{"type":"string"}
				},
				"required":["calendar_id","event_id"],
				"additionalProperties":false
			}`),
		},
		{
			Name:        "move_event",
			Description: "Stage a reschedule of an event (changes only start and end). NOT executed until the user confirms.",
			InputSchema: mk(`{
				"type":"object",
				"properties":{
					"calendar_id":{"type":"integer"},
					"event_id":{"type":"string"},
					"new_start":{"type":"string"},
					"new_end":{"type":"string"},
					"time_zone":{"type":"string"}
				},
				"required":["calendar_id","event_id","new_start","new_end"],
				"additionalProperties":false
			}`),
		},
	}
}

// Execute dispatches a tool call. Returns the textual content to send back to
// the model as a tool_result. If the tool is unknown, an error is returned.
func (t *Toolbox) Execute(ctx context.Context, name string, input json.RawMessage) (string, error) {
	switch name {
	case "list_calendars":
		return t.listCalendars(ctx)
	case "list_events":
		return t.listEvents(ctx, input)
	case "find_event":
		return t.findEvent(ctx, input)
	case "find_free_time":
		return t.findFreeTime(ctx, input)
	case "create_event":
		return t.createEvent(ctx, input)
	case "update_event":
		return t.updateEvent(ctx, input)
	case "delete_event":
		return t.deleteEvent(ctx, input)
	case "move_event":
		return t.moveEvent(ctx, input)
	default:
		return "", fmt.Errorf("unknown tool %q", name)
	}
}

// Describe returns a one-line human-readable summary of a staged tool call,
// rendered on the confirmation card so the user knows what they're approving.
func Describe(name string, input json.RawMessage) string {
	switch name {
	case "create_event":
		var p createEventInput
		_ = json.Unmarshal(input, &p)
		return fmt.Sprintf("Create %q on calendar #%d from %s to %s", p.Summary, p.CalendarID, p.Start, p.End)
	case "update_event":
		var p updateEventInput
		_ = json.Unmarshal(input, &p)
		return fmt.Sprintf("Update event %s on calendar #%d", p.EventID, p.CalendarID)
	case "delete_event":
		var p deleteEventInput
		_ = json.Unmarshal(input, &p)
		return fmt.Sprintf("Delete event %s on calendar #%d", p.EventID, p.CalendarID)
	case "move_event":
		var p moveEventInput
		_ = json.Unmarshal(input, &p)
		return fmt.Sprintf("Move event %s on calendar #%d to %s–%s", p.EventID, p.CalendarID, p.NewStart, p.NewEnd)
	}
	return name
}

// ---------------------------------------------------------------------------
// Read tools
// ---------------------------------------------------------------------------

type calendarOut struct {
	ID               int64  `json:"id"`
	Summary          string `json:"summary"`
	AccountEmail     string `json:"account_email"`
	TimeZone         string `json:"time_zone"`
	Color            string `json:"color,omitempty"`
	GoogleCalendarID string `json:"google_calendar_id"`
}

func (t *Toolbox) listCalendars(ctx context.Context) (string, error) {
	cals, err := t.calendars.ListAll(ctx)
	if err != nil {
		return "", err
	}
	accountEmail := map[int64]string{}
	if accts, err := t.accounts.List(ctx); err == nil {
		for _, a := range accts {
			accountEmail[a.ID] = a.Email
		}
	}
	out := make([]calendarOut, 0, len(cals))
	for _, c := range cals {
		out = append(out, calendarOut{
			ID:               c.ID,
			Summary:          c.Summary,
			AccountEmail:     accountEmail[c.AccountID],
			TimeZone:         c.TimeZone,
			Color:            c.Color,
			GoogleCalendarID: c.GoogleCalendarID,
		})
	}
	return marshalToolResult(out)
}

type listEventsInput struct {
	CalendarID int64  `json:"calendar_id"`
	TimeMin    string `json:"time_min"`
	TimeMax    string `json:"time_max"`
	Q          string `json:"q"`
}

type eventOut struct {
	ID          string   `json:"id"`
	CalendarID  int64    `json:"calendar_id"`
	Summary     string   `json:"summary"`
	Start       string   `json:"start"`
	End         string   `json:"end"`
	Location    string   `json:"location,omitempty"`
	Description string   `json:"description,omitempty"`
	Attendees   []string `json:"attendees,omitempty"`
	AllDay      bool     `json:"all_day,omitempty"`
}

func (t *Toolbox) listEvents(ctx context.Context, input json.RawMessage) (string, error) {
	var p listEventsInput
	if err := json.Unmarshal(input, &p); err != nil {
		return "", err
	}
	cal, err := t.calendars.Get(ctx, p.CalendarID)
	if err != nil {
		return "", err
	}
	cli, err := t.clientFor(ctx, cal.AccountID)
	if err != nil {
		return "", err
	}
	if _, err := time.Parse(time.RFC3339, p.TimeMin); err != nil {
		return "", fmt.Errorf("time_min must be RFC3339: %w", err)
	}
	if _, err := time.Parse(time.RFC3339, p.TimeMax); err != nil {
		return "", fmt.Errorf("time_max must be RFC3339: %w", err)
	}
	call := cli.Service().Events.List(cal.GoogleCalendarID).
		Context(ctx).SingleEvents(true).
		TimeMin(p.TimeMin).TimeMax(p.TimeMax).
		MaxResults(100).OrderBy("startTime")
	if p.Q != "" {
		call = call.Q(p.Q)
	}
	resp, err := call.Do()
	if err != nil {
		return "", err
	}
	out := make([]eventOut, 0, len(resp.Items))
	for _, ev := range resp.Items {
		out = append(out, toEventOut(ev, cal.ID))
	}
	return marshalToolResult(out)
}

type findEventInput struct {
	Query   string `json:"query"`
	TimeMin string `json:"time_min"`
	TimeMax string `json:"time_max"`
}

func (t *Toolbox) findEvent(ctx context.Context, input json.RawMessage) (string, error) {
	var p findEventInput
	if err := json.Unmarshal(input, &p); err != nil {
		return "", err
	}
	if p.Query == "" {
		return "", fmt.Errorf("query is required")
	}
	now := time.Now()
	if p.TimeMin == "" {
		p.TimeMin = now.AddDate(0, 0, -7).Format(time.RFC3339)
	}
	if p.TimeMax == "" {
		p.TimeMax = now.AddDate(0, 0, 90).Format(time.RFC3339)
	}
	cals, err := t.calendars.ListAll(ctx)
	if err != nil {
		return "", err
	}
	var hits []eventOut
	for _, cal := range cals {
		cli, err := t.clientFor(ctx, cal.AccountID)
		if err != nil {
			continue
		}
		resp, err := cli.Service().Events.List(cal.GoogleCalendarID).
			Context(ctx).SingleEvents(true).
			TimeMin(p.TimeMin).TimeMax(p.TimeMax).
			Q(p.Query).MaxResults(50).OrderBy("startTime").Do()
		if err != nil {
			continue
		}
		for _, ev := range resp.Items {
			hits = append(hits, toEventOut(ev, cal.ID))
		}
	}
	return marshalToolResult(hits)
}

type findFreeTimeInput struct {
	DurationMinutes int     `json:"duration_minutes"`
	CalendarIDs     []int64 `json:"calendar_ids"`
	TimeMin         string  `json:"time_min"`
	TimeMax         string  `json:"time_max"`
}

type freeWindowOut struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

func (t *Toolbox) findFreeTime(ctx context.Context, input json.RawMessage) (string, error) {
	var p findFreeTimeInput
	if err := json.Unmarshal(input, &p); err != nil {
		return "", err
	}
	if p.DurationMinutes < 5 {
		return "", fmt.Errorf("duration_minutes must be >= 5")
	}
	if len(p.CalendarIDs) == 0 {
		return "", fmt.Errorf("calendar_ids must not be empty")
	}
	tmin, err := time.Parse(time.RFC3339, p.TimeMin)
	if err != nil {
		return "", fmt.Errorf("time_min: %w", err)
	}
	tmax, err := time.Parse(time.RFC3339, p.TimeMax)
	if err != nil {
		return "", fmt.Errorf("time_max: %w", err)
	}
	if !tmax.After(tmin) {
		return "", fmt.Errorf("time_max must be after time_min")
	}

	// Group calendars by account so we can issue one freebusy call per account.
	type group struct {
		accountID    int64
		googleCalIDs []string
	}
	byAcct := map[int64]*group{}
	for _, id := range p.CalendarIDs {
		cal, err := t.calendars.Get(ctx, id)
		if err != nil {
			return "", err
		}
		g, ok := byAcct[cal.AccountID]
		if !ok {
			g = &group{accountID: cal.AccountID}
			byAcct[cal.AccountID] = g
		}
		g.googleCalIDs = append(g.googleCalIDs, cal.GoogleCalendarID)
	}

	var busy []timeWin
	for _, g := range byAcct {
		cli, err := t.clientFor(ctx, g.accountID)
		if err != nil {
			return "", err
		}
		fb, err := cli.FreeBusy(ctx, g.googleCalIDs, tmin, tmax, "UTC")
		if err != nil {
			return "", fmt.Errorf("freebusy: %w", err)
		}
		for _, periods := range fb {
			for _, pe := range periods {
				bs, err := time.Parse(time.RFC3339, pe.Start)
				if err != nil {
					continue
				}
				be, err := time.Parse(time.RFC3339, pe.End)
				if err != nil {
					continue
				}
				busy = append(busy, timeWin{bs, be})
			}
		}
	}
	busy = mergeBusy(busy)

	// Subtract busy from [tmin, tmax].
	free := []timeWin{{tmin, tmax}}
	for _, b := range busy {
		var next []timeWin
		for _, w := range free {
			if b.end.Before(w.start) || b.start.After(w.end) {
				next = append(next, w)
				continue
			}
			if w.start.Before(b.start) {
				next = append(next, timeWin{w.start, b.start})
			}
			if w.end.After(b.end) {
				next = append(next, timeWin{b.end, w.end})
			}
		}
		free = next
	}

	min := time.Duration(p.DurationMinutes) * time.Minute
	out := make([]freeWindowOut, 0, len(free))
	for _, w := range free {
		if w.end.Sub(w.start) >= min {
			out = append(out, freeWindowOut{
				Start: w.start.Format(time.RFC3339),
				End:   w.end.Format(time.RFC3339),
			})
		}
	}
	return marshalToolResult(out)
}

type timeWin struct{ start, end time.Time }

func mergeBusy(in []timeWin) []timeWin {
	if len(in) == 0 {
		return nil
	}
	sort.Slice(in, func(i, j int) bool { return in[i].start.Before(in[j].start) })
	out := []timeWin{in[0]}
	for _, w := range in[1:] {
		last := &out[len(out)-1]
		if !w.start.After(last.end) {
			if w.end.After(last.end) {
				last.end = w.end
			}
			continue
		}
		out = append(out, w)
	}
	return out
}

// ---------------------------------------------------------------------------
// Write tools
// ---------------------------------------------------------------------------

type createEventInput struct {
	CalendarID  int64    `json:"calendar_id"`
	Summary     string   `json:"summary"`
	Start       string   `json:"start"`
	End         string   `json:"end"`
	TimeZone    string   `json:"time_zone"`
	Location    string   `json:"location"`
	Description string   `json:"description"`
	Attendees   []string `json:"attendees"`
}

func (t *Toolbox) createEvent(ctx context.Context, input json.RawMessage) (string, error) {
	var p createEventInput
	if err := json.Unmarshal(input, &p); err != nil {
		return "", err
	}
	cal, err := t.calendars.Get(ctx, p.CalendarID)
	if err != nil {
		return "", err
	}
	cli, err := t.clientFor(ctx, cal.AccountID)
	if err != nil {
		return "", err
	}
	tz := strings.TrimSpace(p.TimeZone)
	if tz == "" {
		tz = cal.TimeZone
	}
	ev := &gcal.Event{
		Summary:     p.Summary,
		Location:    p.Location,
		Description: p.Description,
		Start:       &gcal.EventDateTime{DateTime: p.Start, TimeZone: tz},
		End:         &gcal.EventDateTime{DateTime: p.End, TimeZone: tz},
		ExtendedProperties: &gcal.EventExtendedProperties{
			Private: t.aiManagedProps(),
		},
	}
	for _, e := range p.Attendees {
		ev.Attendees = append(ev.Attendees, &gcal.EventAttendee{Email: e})
	}
	saved, err := cli.InsertEvent(ctx, cal.GoogleCalendarID, ev)
	if err != nil {
		return "", err
	}
	t.logAudit(ctx, "create", saved.Id, fmt.Sprintf("created %q on calendar #%d", p.Summary, p.CalendarID))
	return marshalToolResult(map[string]any{"event_id": saved.Id, "status": "created"})
}

type updateEventInput struct {
	CalendarID  int64    `json:"calendar_id"`
	EventID     string   `json:"event_id"`
	Summary     string   `json:"summary"`
	Start       string   `json:"start"`
	End         string   `json:"end"`
	TimeZone    string   `json:"time_zone"`
	Location    string   `json:"location"`
	Description string   `json:"description"`
	Attendees   []string `json:"attendees"`
}

func (t *Toolbox) updateEvent(ctx context.Context, input json.RawMessage) (string, error) {
	var p updateEventInput
	if err := json.Unmarshal(input, &p); err != nil {
		return "", err
	}
	cal, err := t.calendars.Get(ctx, p.CalendarID)
	if err != nil {
		return "", err
	}
	cli, err := t.clientFor(ctx, cal.AccountID)
	if err != nil {
		return "", err
	}
	existing, err := cli.GetEvent(ctx, cal.GoogleCalendarID, p.EventID)
	if err != nil {
		return "", err
	}
	if p.Summary != "" {
		existing.Summary = p.Summary
	}
	if p.Location != "" {
		existing.Location = p.Location
	}
	if p.Description != "" {
		existing.Description = p.Description
	}
	tz := strings.TrimSpace(p.TimeZone)
	if tz == "" && existing.Start != nil {
		tz = existing.Start.TimeZone
	}
	if tz == "" {
		tz = cal.TimeZone
	}
	if p.Start != "" {
		existing.Start = &gcal.EventDateTime{DateTime: p.Start, TimeZone: tz}
	}
	if p.End != "" {
		existing.End = &gcal.EventDateTime{DateTime: p.End, TimeZone: tz}
	}
	if len(p.Attendees) > 0 {
		existing.Attendees = nil
		for _, e := range p.Attendees {
			existing.Attendees = append(existing.Attendees, &gcal.EventAttendee{Email: e})
		}
	}
	if existing.ExtendedProperties == nil {
		existing.ExtendedProperties = &gcal.EventExtendedProperties{}
	}
	if existing.ExtendedProperties.Private == nil {
		existing.ExtendedProperties.Private = map[string]string{}
	}
	for k, v := range t.aiManagedProps() {
		existing.ExtendedProperties.Private[k] = v
	}
	saved, err := cli.UpdateEvent(ctx, cal.GoogleCalendarID, p.EventID, existing)
	if err != nil {
		return "", err
	}
	t.logAudit(ctx, "update", saved.Id, fmt.Sprintf("updated event %s on calendar #%d", saved.Id, p.CalendarID))
	return marshalToolResult(map[string]any{"event_id": saved.Id, "status": "updated"})
}

type deleteEventInput struct {
	CalendarID int64  `json:"calendar_id"`
	EventID    string `json:"event_id"`
}

func (t *Toolbox) deleteEvent(ctx context.Context, input json.RawMessage) (string, error) {
	var p deleteEventInput
	if err := json.Unmarshal(input, &p); err != nil {
		return "", err
	}
	cal, err := t.calendars.Get(ctx, p.CalendarID)
	if err != nil {
		return "", err
	}
	cli, err := t.clientFor(ctx, cal.AccountID)
	if err != nil {
		return "", err
	}
	if err := cli.DeleteEvent(ctx, cal.GoogleCalendarID, p.EventID); err != nil {
		return "", err
	}
	t.logAudit(ctx, "delete", p.EventID, fmt.Sprintf("deleted event %s on calendar #%d", p.EventID, p.CalendarID))
	return marshalToolResult(map[string]any{"event_id": p.EventID, "status": "deleted"})
}

type moveEventInput struct {
	CalendarID int64  `json:"calendar_id"`
	EventID    string `json:"event_id"`
	NewStart   string `json:"new_start"`
	NewEnd     string `json:"new_end"`
	TimeZone   string `json:"time_zone"`
}

func (t *Toolbox) moveEvent(ctx context.Context, input json.RawMessage) (string, error) {
	var p moveEventInput
	if err := json.Unmarshal(input, &p); err != nil {
		return "", err
	}
	cal, err := t.calendars.Get(ctx, p.CalendarID)
	if err != nil {
		return "", err
	}
	cli, err := t.clientFor(ctx, cal.AccountID)
	if err != nil {
		return "", err
	}
	existing, err := cli.GetEvent(ctx, cal.GoogleCalendarID, p.EventID)
	if err != nil {
		return "", err
	}
	tz := strings.TrimSpace(p.TimeZone)
	if tz == "" && existing.Start != nil {
		tz = existing.Start.TimeZone
	}
	if tz == "" {
		tz = cal.TimeZone
	}
	existing.Start = &gcal.EventDateTime{DateTime: p.NewStart, TimeZone: tz}
	existing.End = &gcal.EventDateTime{DateTime: p.NewEnd, TimeZone: tz}
	if existing.ExtendedProperties == nil {
		existing.ExtendedProperties = &gcal.EventExtendedProperties{}
	}
	if existing.ExtendedProperties.Private == nil {
		existing.ExtendedProperties.Private = map[string]string{}
	}
	for k, v := range t.aiManagedProps() {
		existing.ExtendedProperties.Private[k] = v
	}
	saved, err := cli.UpdateEvent(ctx, cal.GoogleCalendarID, p.EventID, existing)
	if err != nil {
		return "", err
	}
	t.logAudit(ctx, "move", saved.Id, fmt.Sprintf("moved event %s to %s–%s on calendar #%d", saved.Id, p.NewStart, p.NewEnd, p.CalendarID))
	return marshalToolResult(map[string]any{"event_id": saved.Id, "status": "moved"})
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func (t *Toolbox) aiManagedProps() map[string]string {
	return map[string]string{
		calendar.PropManaged: "1",
		PropAISession:        fmt.Sprintf("%d", t.conversationID),
	}
}

func (t *Toolbox) logAudit(ctx context.Context, action, eventID, msg string) {
	_ = t.audit.Write(ctx, db.AuditWrite{
		Kind:          "ai",
		TargetEventID: eventID,
		Action:        action,
		Message:       msg,
	})
}

func toEventOut(ev *gcal.Event, calID int64) eventOut {
	o := eventOut{
		ID:          ev.Id,
		CalendarID:  calID,
		Summary:     ev.Summary,
		Location:    ev.Location,
		Description: ev.Description,
	}
	if ev.Start != nil {
		if ev.Start.DateTime != "" {
			o.Start = ev.Start.DateTime
		} else {
			o.Start = ev.Start.Date
			o.AllDay = true
		}
	}
	if ev.End != nil {
		if ev.End.DateTime != "" {
			o.End = ev.End.DateTime
		} else {
			o.End = ev.End.Date
		}
	}
	for _, a := range ev.Attendees {
		o.Attendees = append(o.Attendees, a.Email)
	}
	return o
}

func marshalToolResult(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
