package db

import (
	"encoding/json"
	"time"
)

type Account struct {
	ID                    int64
	GoogleSub             string
	Email                 string
	RefreshTokenSealed    string
	AccessTokenSealed     string
	AccessTokenExpiresAt  *time.Time
	PrimaryCalendarID     string
	CreatedAt             time.Time
	// Per-account hour windows (Working / Personal / Meeting). All nullable JSON;
	// readers should fall back: personal -> working -> default; meeting -> working.
	WorkingHours          json.RawMessage
	PersonalHours         json.RawMessage
	MeetingHours          json.RawMessage
}

type Calendar struct {
	ID               int64
	AccountID        int64
	GoogleCalendarID string
	Summary          string
	TimeZone         string
	Color            string
	LastSyncedAt     *time.Time
}

type SyncToken struct {
	ID                int64
	AccountID         int64
	CalendarID        int64
	SyncToken         string
	WatchChannelID    string
	WatchResourceID   string
	WatchTokenSecret  string
	WatchExpiresAt    *time.Time
	LastPolledAt      *time.Time
}

type SyncRule struct {
	ID                 int64
	Name               string
	SourceCalendarID   int64
	TargetCalendarID   int64
	Direction          string
	PrimarySide        string
	Filter             json.RawMessage
	Transform          json.RawMessage
	BackfillDays       int
	BackfillDone       bool
	Enabled            bool
	CreatedAt          time.Time
	// Optional category to pin the mirror event to. NULL means use the
	// auto-categorizer at planner-render time.
	CategoryID         *int64
	// Visibility preset. One of: personal_commitment | busy_for_all |
	// details_for_you_busy_others | details_for_you_and_access. Drives the
	// engine's transform; the legacy Transform JSON is honored only when
	// VisibilityMode is empty (it never is for new rules).
	VisibilityMode     string
	// One of: skip | only_busy | sync_all.
	AllDayMode         string
	// When true, only mirror events whose start time lies within the source
	// account's Working hours.
	WorkingHoursOnly   bool
}

type SmartBlock struct {
	ID                int64
	Name              string
	TargetCalendarID  int64
	SourceCalendarIDs []int64
	WorkingHours      json.RawMessage
	HorizonDays       int
	MinBlockMinutes   int
	MergeGapMinutes   int
	TitleTemplate     string
	Enabled           bool
	CreatedAt         time.Time
}

type EventLink struct {
	ID               int64
	RuleID           int64
	SourceAccountID  int64
	SourceCalendarID int64
	SourceEventID    string
	TargetAccountID  int64
	TargetCalendarID int64
	TargetEventID    string
	SourceEtag       string
	TargetEtag       string
	LastSyncedAt     time.Time
}

type ManagedBlock struct {
	ID               int64
	SmartBlockID     int64
	TargetAccountID  int64
	TargetCalendarID int64
	TargetEventID    string
	StartsAt         time.Time
	EndsAt           time.Time
	LastSyncedAt     time.Time
}

// Task priorities (matches Reclaim's Kanban columns).
const (
	PriorityCritical = "critical"
	PriorityHigh     = "high"
	PriorityMedium   = "medium"
	PriorityLow      = "low"
)

// Task statuses.
const (
	TaskPending   = "pending"
	TaskScheduled = "scheduled"
	TaskCompleted = "completed"
	TaskCancelled = "cancelled"
)

type Task struct {
	ID                 int64
	Title              string
	Notes              string
	Priority           string
	DurationMinutes    int
	DueAt              *time.Time
	Status             string
	TargetCalendarID   int64
	CategoryID         *int64
	ScheduledEventID   string
	ScheduledStartsAt  *time.Time
	ScheduledEndsAt    *time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type AuditEntry struct {
	ID            int64
	TS            time.Time
	Kind          string
	RuleID        *int64
	SmartBlockID  *int64
	SourceEventID string
	TargetEventID string
	Action        string
	Message       string
}
