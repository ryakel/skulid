// Package calfake is an in-memory calendar.API for tests.
//
// It is a fake rather than a mock: it keeps real events in a map and answers
// reads from them, so a test asserts on the state Google would have ended up
// in rather than on a sequence of expected calls. That reads better for an
// engine whose whole contract is "insert, update and delete these events".
package calfake

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"

	gcal "google.golang.org/api/calendar/v3"

	"github.com/ryakel/skulid/internal/calendar"
)

// Call is one write the engine made, in order. Reads are not recorded --
// asserting on them pins the implementation rather than the behaviour.
type Call struct {
	Op         string // "insert" | "update" | "delete"
	CalendarID string
	EventID    string
	Event      *gcal.Event
}

// Client is an in-memory calendar.API.
//
// Safe for concurrent use: the planner fans out across calendars, so a fake
// that raced would fail under -race for reasons that have nothing to do with
// the code under test.
type Client struct {
	mu     sync.Mutex
	nextID int
	// events is calendarID -> eventID -> event.
	events map[string]map[string]*gcal.Event
	calls  []Call

	// Err, when set for an operation name, makes every such call fail. Use it
	// to prove an engine's error handling without a real API.
	Err map[string]error
}

func New() *Client {
	return &Client{events: map[string]map[string]*gcal.Event{}}
}

// Seed puts an event on a calendar without recording a call, standing in for
// what was already there before the engine ran. Called with no events it just
// registers the calendar, so reads against it answer empty rather than
// "not found".
func (c *Client) Seed(calendarID string, evs ...*gcal.Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.events[calendarID] == nil {
		c.events[calendarID] = map[string]*gcal.Event{}
	}
	for _, ev := range evs {
		if ev.Id == "" {
			ev.Id = c.mintID()
		}
		c.putLocked(calendarID, ev)
	}
}

// Events returns everything currently on a calendar, ordered by id so a test
// can compare without sorting.
func (c *Client) Events(calendarID string) []*gcal.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]*gcal.Event, 0, len(c.events[calendarID]))
	for _, ev := range c.events[calendarID] {
		out = append(out, ev)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Id < out[j].Id })
	return out
}

// Calls returns the writes made so far, in order.
func (c *Client) Calls() []Call {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Call(nil), c.calls...)
}

// CallsOf returns just the writes of one kind, which is usually what a test
// actually wants to count.
func (c *Client) CallsOf(op string) []Call {
	var out []Call
	for _, call := range c.Calls() {
		if call.Op == op {
			out = append(out, call)
		}
	}
	return out
}

// Reset clears the recorded calls but keeps the events, for a test that runs
// the engine twice and only cares about the second pass.
func (c *Client) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = nil
}

func (c *Client) mintID() string {
	c.nextID++
	return "fake-" + strconv.Itoa(c.nextID)
}

func (c *Client) putLocked(calendarID string, ev *gcal.Event) {
	if c.events[calendarID] == nil {
		c.events[calendarID] = map[string]*gcal.Event{}
	}
	c.events[calendarID][ev.Id] = ev
}

func (c *Client) fail(op string) error {
	if c.Err == nil {
		return nil
	}
	return c.Err[op]
}

func (c *Client) ListCalendars(context.Context) ([]*gcal.CalendarListEntry, error) {
	if err := c.fail("list_calendars"); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]*gcal.CalendarListEntry, 0, len(c.events))
	for id := range c.events {
		out = append(out, &gcal.CalendarListEntry{Id: id, Summary: id})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Id < out[j].Id })
	return out, nil
}

func (c *Client) ListEvents(_ context.Context, calendarID string, opt calendar.ListEventsOptions) ([]*gcal.Event, error) {
	if err := c.fail("list_events"); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []*gcal.Event
	for _, ev := range c.events[calendarID] {
		if !withinWindow(ev, opt.TimeMin, opt.TimeMax) {
			continue
		}
		out = append(out, ev)
	}
	sort.Slice(out, func(i, j int) bool { return startOf(out[i]).Before(startOf(out[j])) })
	if opt.MaxResults > 0 && int64(len(out)) > opt.MaxResults {
		out = out[:opt.MaxResults]
	}
	return out, nil
}

// IncrementalSync ignores sync tokens and returns everything on the calendar:
// the engine's job is to decide what each event means, not to page.
func (c *Client) IncrementalSync(_ context.Context, calendarID, _ string, from time.Time) (*calendar.IncrementalSyncResult, error) {
	if err := c.fail("incremental_sync"); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out := &calendar.IncrementalSyncResult{NextSyncToken: "fake-token"}
	for _, ev := range c.events[calendarID] {
		// A cancelled event has no usable start, and the real API returns it
		// regardless when ShowDeleted is on, so never filter one out here.
		if ev.Status != "cancelled" && !withinWindow(ev, from, time.Time{}) {
			continue
		}
		out.Events = append(out.Events, ev)
	}
	sort.Slice(out.Events, func(i, j int) bool { return out.Events[i].Id < out.Events[j].Id })
	return out, nil
}

func (c *Client) GetEvent(_ context.Context, calendarID, eventID string) (*gcal.Event, error) {
	if err := c.fail("get_event"); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	ev, ok := c.events[calendarID][eventID]
	if !ok {
		return nil, fmt.Errorf("event %q not found on %q", eventID, calendarID)
	}
	return ev, nil
}

func (c *Client) InsertEvent(_ context.Context, calendarID string, ev *gcal.Event) (*gcal.Event, error) {
	if err := c.fail("insert"); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	saved := cloneEvent(ev)
	saved.Id = c.mintID()
	if saved.Etag == "" {
		saved.Etag = `"etag-` + saved.Id + `"`
	}
	c.putLocked(calendarID, saved)
	c.calls = append(c.calls, Call{Op: "insert", CalendarID: calendarID, EventID: saved.Id, Event: cloneEvent(saved)})
	return saved, nil
}

func (c *Client) UpdateEvent(_ context.Context, calendarID, eventID string, ev *gcal.Event) (*gcal.Event, error) {
	if err := c.fail("update"); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.events[calendarID][eventID]; !ok {
		return nil, fmt.Errorf("event %q not found on %q", eventID, calendarID)
	}
	saved := cloneEvent(ev)
	saved.Id = eventID
	saved.Etag = `"etag-` + eventID + `-` + strconv.Itoa(len(c.calls)) + `"`
	c.putLocked(calendarID, saved)
	c.calls = append(c.calls, Call{Op: "update", CalendarID: calendarID, EventID: eventID, Event: cloneEvent(saved)})
	return saved, nil
}

// DeleteEvent mirrors the real client: deleting something already gone is not
// an error, because Google answers 404/410 and the wrapper swallows it.
func (c *Client) DeleteEvent(_ context.Context, calendarID, eventID string) error {
	if err := c.fail("delete"); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.events[calendarID], eventID)
	c.calls = append(c.calls, Call{Op: "delete", CalendarID: calendarID, EventID: eventID})
	return nil
}

// FreeBusy derives busy periods from the seeded events, so a test sets up one
// calendar and both the sync and the scheduling paths see the same world.
// Events marked transparent are free, matching Google.
func (c *Client) FreeBusy(_ context.Context, calendarIDs []string, start, end time.Time, _ string) (map[string][]*gcal.TimePeriod, error) {
	if err := c.fail("freebusy"); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string][]*gcal.TimePeriod, len(calendarIDs))
	for _, id := range calendarIDs {
		var periods []*gcal.TimePeriod
		for _, ev := range c.events[id] {
			if ev.Status == "cancelled" || ev.Transparency == "transparent" {
				continue
			}
			if !withinWindow(ev, start, end) {
				continue
			}
			if ev.Start == nil || ev.End == nil {
				continue
			}
			periods = append(periods, &gcal.TimePeriod{Start: ev.Start.DateTime, End: ev.End.DateTime})
		}
		sort.Slice(periods, func(i, j int) bool { return periods[i].Start < periods[j].Start })
		out[id] = periods
	}
	return out, nil
}

func (c *Client) Watch(_ context.Context, _, channelID, _, _ string, _ time.Duration) (*gcal.Channel, error) {
	if err := c.fail("watch"); err != nil {
		return nil, err
	}
	return &gcal.Channel{Id: channelID, ResourceId: "fake-resource-" + channelID}, nil
}

func (c *Client) StopChannel(context.Context, string, string) error {
	return c.fail("stop_channel")
}

func startOf(ev *gcal.Event) time.Time {
	if ev == nil || ev.Start == nil {
		return time.Time{}
	}
	if ev.Start.DateTime != "" {
		t, err := time.Parse(time.RFC3339, ev.Start.DateTime)
		if err == nil {
			return t
		}
	}
	if ev.Start.Date != "" {
		t, err := time.Parse("2006-01-02", ev.Start.Date)
		if err == nil {
			return t
		}
	}
	return time.Time{}
}

func endOf(ev *gcal.Event) time.Time {
	if ev == nil || ev.End == nil {
		return startOf(ev)
	}
	if ev.End.DateTime != "" {
		t, err := time.Parse(time.RFC3339, ev.End.DateTime)
		if err == nil {
			return t
		}
	}
	if ev.End.Date != "" {
		t, err := time.Parse("2006-01-02", ev.End.Date)
		if err == nil {
			return t
		}
	}
	return startOf(ev)
}

// withinWindow is half-open on both ends, matching how Google treats
// timeMin/timeMax. A zero bound means unbounded on that side.
func withinWindow(ev *gcal.Event, min, max time.Time) bool {
	s, e := startOf(ev), endOf(ev)
	if s.IsZero() && e.IsZero() {
		return true
	}
	if !min.IsZero() && !e.After(min) {
		return false
	}
	if !max.IsZero() && !s.Before(max) {
		return false
	}
	return true
}

// cloneEvent copies deeply enough that a test mutating what it passed in, or
// what it read back, cannot reach into the fake's stored state.
func cloneEvent(in *gcal.Event) *gcal.Event {
	if in == nil {
		return nil
	}
	out := *in
	if in.Start != nil {
		s := *in.Start
		out.Start = &s
	}
	if in.End != nil {
		e := *in.End
		out.End = &e
	}
	if in.ExtendedProperties != nil {
		ep := *in.ExtendedProperties
		if in.ExtendedProperties.Private != nil {
			ep.Private = make(map[string]string, len(in.ExtendedProperties.Private))
			for k, v := range in.ExtendedProperties.Private {
				ep.Private[k] = v
			}
		}
		out.ExtendedProperties = &ep
	}
	if in.Attendees != nil {
		out.Attendees = make([]*gcal.EventAttendee, len(in.Attendees))
		for i, a := range in.Attendees {
			if a == nil {
				continue
			}
			c := *a
			out.Attendees[i] = &c
		}
	}
	return &out
}

var _ calendar.API = (*Client)(nil)
