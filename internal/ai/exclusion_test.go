package ai

import (
	"context"
	"errors"
	"testing"

	"github.com/ryakel/skulid/internal/calendar"
	"github.com/ryakel/skulid/internal/db"
)

type fakeChecker struct {
	excluded map[int64]bool
	err      error
	calls    int
}

func (f *fakeChecker) IsAIExcluded(_ context.Context, id int64) (bool, error) {
	f.calls++
	if f.err != nil {
		return true, f.err
	}
	return f.excluded[id], nil
}

// The guard is the only thing standing between an employer's calendar and
// Anthropic's API, so verify it denies rather than merely filters.
func TestGuardClientForBlocksExcludedAccount(t *testing.T) {
	checker := &fakeChecker{excluded: map[int64]bool{7: true}}
	reached := false
	inner := func(context.Context, int64) (*calendar.Client, error) {
		reached = true
		return nil, nil
	}

	guarded := guardClientFor(checker, inner)
	if _, err := guarded(context.Background(), 7); !errors.Is(err, ErrAIExcluded) {
		t.Fatalf("want ErrAIExcluded, got %v", err)
	}
	if reached {
		t.Fatal("inner ClientFor was called for an excluded account")
	}
}

func TestGuardClientForAllowsNormalAccount(t *testing.T) {
	checker := &fakeChecker{excluded: map[int64]bool{7: true}}
	reached := false
	inner := func(context.Context, int64) (*calendar.Client, error) {
		reached = true
		return nil, nil
	}

	if _, err := guardClientFor(checker, inner)(context.Background(), 3); err != nil {
		t.Fatalf("unexpected error for a permitted account: %v", err)
	}
	if !reached {
		t.Fatal("inner ClientFor was not called for a permitted account")
	}
}

// A lookup failure must deny, not fall open.
func TestGuardClientForDeniesOnCheckFailure(t *testing.T) {
	checker := &fakeChecker{err: errors.New("db down")}
	reached := false
	inner := func(context.Context, int64) (*calendar.Client, error) {
		reached = true
		return nil, nil
	}

	if _, err := guardClientFor(checker, inner)(context.Background(), 3); err == nil {
		t.Fatal("want an error when the exclusion check fails, got nil")
	}
	if reached {
		t.Fatal("inner ClientFor was called despite a failed exclusion check")
	}
}

func TestFilterExcludedCalendars(t *testing.T) {
	cals := []db.Calendar{
		{ID: 1, AccountID: 1, Summary: "personal"},
		{ID: 2, AccountID: 7, Summary: "work"},
		{ID: 3, AccountID: 7, Summary: "work team"},
		{ID: 4, AccountID: 2, Summary: "family"},
	}

	got := filterExcludedCalendars(cals, map[int64]bool{7: true})
	if len(got) != 2 {
		t.Fatalf("want 2 visible calendars, got %d", len(got))
	}
	for _, c := range got {
		if c.AccountID == 7 {
			t.Errorf("calendar %q from an excluded account survived the filter", c.Summary)
		}
	}

	// No exclusions must not drop anything.
	if all := filterExcludedCalendars(cals, nil); len(all) != len(cals) {
		t.Errorf("want all %d calendars with no exclusions, got %d", len(cals), len(all))
	}
}

// filterExcludedCalendars must not alias or clobber the caller's slice.
func TestFilterExcludedCalendarsDoesNotMutateInput(t *testing.T) {
	cals := []db.Calendar{
		{ID: 1, AccountID: 7, Summary: "work"},
		{ID: 2, AccountID: 1, Summary: "personal"},
	}
	_ = filterExcludedCalendars(cals, map[int64]bool{7: true})

	if cals[0].Summary != "work" || cals[1].Summary != "personal" {
		t.Fatalf("input slice was mutated: %+v", cals)
	}
}
