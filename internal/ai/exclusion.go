package ai

import (
	"context"
	"errors"
	"fmt"

	"github.com/ryakel/skulid/internal/calendar"
	"github.com/ryakel/skulid/internal/db"
	syncengine "github.com/ryakel/skulid/internal/sync"
)

// ErrAIExcluded is returned when a tool tries to reach an account the owner
// has placed off-limits to the assistant.
var ErrAIExcluded = errors.New("account is excluded from the AI assistant")

// exclusionChecker is the slice of AccountRepo the guard needs. Narrowing it
// to an interface keeps the guard testable without a live Postgres.
type exclusionChecker interface {
	IsAIExcluded(ctx context.Context, id int64) (bool, error)
}

// guardClientFor wraps a ClientFor so no account marked ai_excluded can be
// reached through the toolbox.
//
// The wrap happens once, in NewToolbox, rather than at each call site: there
// are a dozen places a tool asks for a client, and a guard that has to be
// remembered at each of them is a guard that will eventually be forgotten.
// Everything the assistant does to a calendar goes through a Google client,
// so this is the choke point that matters. A failed check denies access --
// an account we cannot verify is one we must not hand out.
func guardClientFor(accounts exclusionChecker, inner syncengine.ClientFor) syncengine.ClientFor {
	return func(ctx context.Context, accountID int64) (*calendar.Client, error) {
		excluded, err := accounts.IsAIExcluded(ctx, accountID)
		if err != nil {
			return nil, fmt.Errorf("checking AI exclusion for account %d: %w", accountID, err)
		}
		if excluded {
			return nil, fmt.Errorf("account %d: %w", accountID, ErrAIExcluded)
		}
		return inner(ctx, accountID)
	}
}

// filterExcludedCalendars drops calendars belonging to excluded accounts.
func filterExcludedCalendars(cals []db.Calendar, excluded map[int64]bool) []db.Calendar {
	if len(excluded) == 0 {
		return cals
	}
	out := make([]db.Calendar, 0, len(cals))
	for _, c := range cals {
		if !excluded[c.AccountID] {
			out = append(out, c)
		}
	}
	return out
}

// visibleCalendars is every calendar the assistant is allowed to know about.
// Excluded accounts are filtered before the model ever sees them, so it can
// neither name them nor reason about them.
func (t *Toolbox) visibleCalendars(ctx context.Context) ([]db.Calendar, error) {
	cals, err := t.calendars.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	excluded, err := t.accounts.AIExcludedIDs(ctx)
	if err != nil {
		return nil, err
	}
	return filterExcludedCalendars(cals, excluded), nil
}
