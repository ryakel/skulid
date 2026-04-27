// Package calendar wraps the Google Calendar v3 client with helpers for the
// extendedProperties keys used to mark managed events and detect loops.
package calendar

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"golang.org/x/oauth2"
	"google.golang.org/api/calendar/v3"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

const (
	PropManaged       = "skulidManaged"
	PropSourceEventID = "skulidSourceEventId"
	PropRuleID        = "skulidRuleId"
	PropSmartBlockID  = "skulidSmartBlockId"
	PropTaskID        = "skulidTaskId"
	PropHabitID       = "skulidHabitId"
	PropBufferType    = "skulidBufferType"       // "decompression" | "travel" (future)
	PropBufferFor     = "skulidBufferForEventId" // Google ID of the meeting we trail

	// Legacy keys from the pre-rename "calm-axolotl" era. Read-only — recognized
	// by IsManaged() so any old managed event written under the previous name
	// still gets the loop guard, but new writes only emit the new keys above.
	legacyPropManaged = "calmAxolotlManaged"
)

// BufferProps fills the extendedProperties.private map for a buffer event
// (e.g. Decompress). The owning meeting's Google event ID goes in
// PropBufferFor so we can find the buffer back by source.
func BufferProps(bufferType, sourceEventID string) map[string]string {
	return map[string]string{
		PropManaged:    "1",
		PropBufferType: bufferType,
		PropBufferFor:  sourceEventID,
	}
}

type Client struct {
	svc *calendar.Service
}

func New(ctx context.Context, ts oauth2.TokenSource) (*Client, error) {
	svc, err := calendar.NewService(ctx, option.WithTokenSource(ts))
	if err != nil {
		return nil, err
	}
	return &Client{svc: svc}, nil
}

func (c *Client) Service() *calendar.Service { return c.svc }

func (c *Client) ListCalendars(ctx context.Context) ([]*calendar.CalendarListEntry, error) {
	var out []*calendar.CalendarListEntry
	pageToken := ""
	for {
		call := c.svc.CalendarList.List().Context(ctx).MaxResults(100)
		if pageToken != "" {
			call = call.PageToken(pageToken)
		}
		resp, err := call.Do()
		if err != nil {
			return nil, err
		}
		out = append(out, resp.Items...)
		if resp.NextPageToken == "" {
			break
		}
		pageToken = resp.NextPageToken
	}
	return out, nil
}

// IncrementalSyncResult is what an incremental sync returns: the new sync
// token plus the list of events that changed since the last token.
type IncrementalSyncResult struct {
	Events       []*calendar.Event
	NextSyncToken string
}

// IncrementalSync uses the syncToken if present, otherwise performs a full
// time-bounded sync from `from` forward. When the server returns 410 Gone the
// caller should retry with an empty syncToken (full resync).
func (c *Client) IncrementalSync(ctx context.Context, calendarID, syncToken string, from time.Time) (*IncrementalSyncResult, error) {
	out := &IncrementalSyncResult{}
	pageToken := ""
	for {
		call := c.svc.Events.List(calendarID).Context(ctx).
			ShowDeleted(true).
			SingleEvents(true).
			MaxResults(250)
		if syncToken != "" {
			call = call.SyncToken(syncToken)
		} else {
			call = call.TimeMin(from.Format(time.RFC3339))
		}
		if pageToken != "" {
			call = call.PageToken(pageToken)
		}
		resp, err := call.Do()
		if err != nil {
			var gerr *googleapi.Error
			if errors.As(err, &gerr) && gerr.Code == 410 {
				return nil, ErrSyncTokenInvalid
			}
			return nil, err
		}
		out.Events = append(out.Events, resp.Items...)
		if resp.NextPageToken != "" {
			pageToken = resp.NextPageToken
			continue
		}
		out.NextSyncToken = resp.NextSyncToken
		return out, nil
	}
}

var ErrSyncTokenInvalid = errors.New("sync token invalid")

func (c *Client) GetEvent(ctx context.Context, calendarID, eventID string) (*calendar.Event, error) {
	return c.svc.Events.Get(calendarID, eventID).Context(ctx).Do()
}

func (c *Client) InsertEvent(ctx context.Context, calendarID string, ev *calendar.Event) (*calendar.Event, error) {
	return c.svc.Events.Insert(calendarID, ev).Context(ctx).Do()
}

func (c *Client) UpdateEvent(ctx context.Context, calendarID, eventID string, ev *calendar.Event) (*calendar.Event, error) {
	return c.svc.Events.Update(calendarID, eventID, ev).Context(ctx).Do()
}

func (c *Client) DeleteEvent(ctx context.Context, calendarID, eventID string) error {
	err := c.svc.Events.Delete(calendarID, eventID).Context(ctx).Do()
	if err != nil {
		var gerr *googleapi.Error
		if errors.As(err, &gerr) && (gerr.Code == 404 || gerr.Code == 410) {
			return nil
		}
		return err
	}
	return nil
}

func (c *Client) FreeBusy(ctx context.Context, calendarIDs []string, start, end time.Time, tz string) (map[string][]*calendar.TimePeriod, error) {
	req := &calendar.FreeBusyRequest{
		TimeMin:  start.Format(time.RFC3339),
		TimeMax:  end.Format(time.RFC3339),
		TimeZone: tz,
	}
	for _, id := range calendarIDs {
		req.Items = append(req.Items, &calendar.FreeBusyRequestItem{Id: id})
	}
	resp, err := c.svc.Freebusy.Query(req).Context(ctx).Do()
	if err != nil {
		return nil, err
	}
	out := make(map[string][]*calendar.TimePeriod, len(resp.Calendars))
	for id, cal := range resp.Calendars {
		out[id] = cal.Busy
	}
	return out, nil
}

// Watch registers a push notification channel on a calendar. The channelID and
// token are caller-supplied; the address is the public webhook URL.
func (c *Client) Watch(ctx context.Context, calendarID, channelID, address, token string, ttl time.Duration) (*calendar.Channel, error) {
	ch := &calendar.Channel{
		Id:      channelID,
		Type:    "web_hook",
		Address: address,
		Token:   token,
	}
	if ttl > 0 {
		ch.Params = map[string]string{"ttl": strconv.FormatInt(int64(ttl.Seconds()), 10)}
	}
	return c.svc.Events.Watch(calendarID, ch).Context(ctx).Do()
}

func (c *Client) StopChannel(ctx context.Context, channelID, resourceID string) error {
	err := c.svc.Channels.Stop(&calendar.Channel{Id: channelID, ResourceId: resourceID}).Context(ctx).Do()
	if err != nil {
		var gerr *googleapi.Error
		if errors.As(err, &gerr) && (gerr.Code == 404 || gerr.Code == 410) {
			return nil
		}
		return err
	}
	return nil
}

// ManagedProps fills in the extendedProperties.private map used as the
// secondary loop guard.
func ManagedProps(ruleID int64, sourceEventID string) map[string]string {
	return map[string]string{
		PropManaged:       "1",
		PropRuleID:        fmt.Sprintf("%d", ruleID),
		PropSourceEventID: sourceEventID,
	}
}

func SmartBlockProps(blockID int64) map[string]string {
	return map[string]string{
		PropManaged:      "1",
		PropSmartBlockID: fmt.Sprintf("%d", blockID),
	}
}

func TaskProps(taskID int64) map[string]string {
	return map[string]string{
		PropManaged: "1",
		PropTaskID:  fmt.Sprintf("%d", taskID),
	}
}

func HabitProps(habitID int64) map[string]string {
	return map[string]string{
		PropManaged: "1",
		PropHabitID: fmt.Sprintf("%d", habitID),
	}
}

// IsManaged reports whether an event was created by us. Recognizes both the
// current "skulid*" keys and the legacy "calmAxolotl*" keys so events written
// before the project rename still trip the loop guard.
func IsManaged(ev *calendar.Event) bool {
	if ev == nil || ev.ExtendedProperties == nil || ev.ExtendedProperties.Private == nil {
		return false
	}
	props := ev.ExtendedProperties.Private
	return props[PropManaged] == "1" || props[legacyPropManaged] == "1"
}
