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
