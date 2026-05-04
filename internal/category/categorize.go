// Package category contains the pure event-classification logic. Given a
// Google Calendar event plus a small bag of context (the user's own email
// domains, an optional per-calendar default), Classify returns the slug of
// the best-matching built-in category.
//
// This file is intentionally I/O-free — Classify takes no context.Context
// and never touches the network or DB. That's why it's exhaustively tested.
package category

import (
	"strings"

	gcal "google.golang.org/api/calendar/v3"

	"github.com/ryakel/skulid/internal/db"
)

// Context carries the inputs the heuristics need beyond the event itself.
type Context struct {
	// OwnerDomains is the lowercased set of email domains considered "internal"
	// for this instance — typically derived from the connected accounts. An
	// attendee whose email domain is in this set is internal; anyone else is
	// external. Empty set means we can't classify internal/external and
	// 3+ attendee meetings collapse to Team (conservative: don't mark as
	// External just because we don't know).
	OwnerDomains map[string]bool
	// CalendarDefaultSlug is the per-calendar override slug (e.g. "personal"
	// for a Family calendar). Used as a low-priority fallback before "other".
	CalendarDefaultSlug string
}

// Title-keyword shortcuts. Order doesn't matter; first hit wins per category.
var keywordMap = []struct {
	slug    string
	matches []string
}{
	{db.CategoryFocus, []string{"focus", "deep work", "deep-work", "writing block", "heads down"}},
	{db.CategoryTravel, []string{"lunch", "break", "decompress", "decompression", "commute", "travel", "drive"}},
}

// Classify returns the best-fit category slug for the event. It never returns
// an empty string — when nothing matches, it falls back to "other".
func Classify(ev *gcal.Event, ctx Context) string {
	if ev == nil {
		return db.CategoryOther
	}

	// Cancelled events keep no category — caller should typically skip these.
	if ev.Status == "cancelled" {
		return db.CategoryOther
	}

	// Transparent (free) events lean to "free" unless the user has pinned the
	// calendar to something specific.
	if strings.EqualFold(ev.Transparency, "transparent") {
		if ctx.CalendarDefaultSlug != "" {
			return ctx.CalendarDefaultSlug
		}
		return db.CategoryFree
	}

	// Title-keyword pass — strongest signal short of a per-rule pin. Personal
	// keywords are checked here so "Lunch" on a work calendar still reads as
	// a break, not as a meeting.
	if slug := titleKeyword(ev.Summary); slug != "" {
		return slug
	}

	// All-day events that don't match a keyword usually represent personal/
	// life-admin items (holidays, OOO, birthdays).
	if isAllDay(ev) {
		if ctx.CalendarDefaultSlug != "" {
			return ctx.CalendarDefaultSlug
		}
		return db.CategoryPersonal
	}

	// Attendee-count-based heuristic for timed events.
	switch n := countRealAttendees(ev); {
	case n <= 1:
		// Solo events on the calendar — Focus by default unless the calendar
		// pins something more specific (e.g. a Personal calendar).
		if ctx.CalendarDefaultSlug != "" {
			return ctx.CalendarDefaultSlug
		}
		return db.CategoryFocus
	case n == 2:
		return db.CategoryOneOnOne
	default: // n >= 3
		if hasExternalAttendee(ev, ctx.OwnerDomains) {
			return db.CategoryExternal
		}
		return db.CategoryTeam
	}
}

// titleKeyword scans the event summary for any keyword in keywordMap and
// returns the matching slug, case-insensitively. Whole-word matches are not
// required — a substring is enough since meeting titles are noisy.
func titleKeyword(summary string) string {
	if summary == "" {
		return ""
	}
	lower := strings.ToLower(summary)
	for _, m := range keywordMap {
		for _, kw := range m.matches {
			if strings.Contains(lower, kw) {
				return m.slug
			}
		}
	}
	return ""
}

func isAllDay(ev *gcal.Event) bool {
	return ev.Start != nil && ev.Start.DateTime == "" && ev.Start.Date != ""
}

// countRealAttendees counts non-resource attendees. Google's Calendar API
// surfaces meeting rooms and equipment as attendees with Resource=true; those
// don't change the social shape of the meeting and shouldn't bump 1:1s into
// "Team".
func countRealAttendees(ev *gcal.Event) int {
	n := 0
	for _, a := range ev.Attendees {
		if a == nil || a.Resource {
			continue
		}
		n++
	}
	return n
}

// hasExternalAttendee reports whether any non-resource attendee belongs to a
// domain outside OwnerDomains. With an empty OwnerDomains the function
// reports false (we can't classify, so don't escalate to External).
func hasExternalAttendee(ev *gcal.Event, ownerDomains map[string]bool) bool {
	if len(ownerDomains) == 0 {
		return false
	}
	for _, a := range ev.Attendees {
		if a == nil || a.Resource || a.Email == "" {
			continue
		}
		dom := domainOf(a.Email)
		if dom == "" {
			continue
		}
		if !ownerDomains[dom] {
			return true
		}
	}
	return false
}

// domainOf returns the lowercased part after '@' in an email, or "" if absent.
func domainOf(email string) string {
	at := strings.LastIndex(email, "@")
	if at < 0 || at == len(email)-1 {
		return ""
	}
	return strings.ToLower(email[at+1:])
}

// DomainsFromEmails extracts the lowercased domain part of each email and
// returns them as a set. Helper for building Context.OwnerDomains from the
// list of connected accounts.
func DomainsFromEmails(emails []string) map[string]bool {
	out := map[string]bool{}
	for _, e := range emails {
		if d := domainOf(e); d != "" {
			out[d] = true
		}
	}
	return out
}
