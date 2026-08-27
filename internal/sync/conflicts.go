package sync

import (
	"time"

	gcal "google.golang.org/api/calendar/v3"

	"github.com/ryakel/skulid/internal/db"
	"github.com/ryakel/skulid/internal/hours"
)

// enabledCalendars drops calendars the owner has switched off. A disabled
// calendar syncs nothing and holds no watch, so treating it as a source of
// busy time would block scheduling on events nobody is maintaining.
func enabledCalendars(in []db.Calendar) []db.Calendar {
	out := make([]db.Calendar, 0, len(in))
	for _, c := range in {
		if c.Enabled {
			out = append(out, c)
		}
	}
	return out
}

// groupByAccount buckets calendars by owning account, so freebusy can be
// fetched in one call per account rather than one per calendar.
func groupByAccount(in []db.Calendar) map[int64][]db.Calendar {
	out := make(map[int64][]db.Calendar)
	for _, c := range in {
		out[c.AccountID] = append(out[c.AccountID], c)
	}
	return out
}

// periodsToWindows converts Google's freebusy periods into windows, skipping
// any it cannot parse rather than failing the whole placement. A period we
// cannot read is one busy block we might schedule over, which is why the
// caller must not treat this as a complete failure signal on its own.
func periodsToWindows(periods []*gcal.TimePeriod) []hours.Window {
	out := make([]hours.Window, 0, len(periods))
	for _, p := range periods {
		start, err := time.Parse(time.RFC3339, p.Start)
		if err != nil {
			continue
		}
		end, err := time.Parse(time.RFC3339, p.End)
		if err != nil {
			continue
		}
		out = append(out, hours.Window{Start: start, End: end})
	}
	return out
}

// subtractManaged removes skulid's own scheduled windows from a busy set.
//
// Freebusy returns opaque periods with no extendedProperties, so a smart
// block is indistinguishable from a real meeting at that layer. Every window
// skulid wrote is recorded locally, though, so they can be removed here
// instead -- at the cost of one database query rather than a per-calendar
// Events.list.
//
// The known limit: Google merges adjacent and overlapping busy periods before
// returning them. If a real meeting was created on top of a skulid block
// after that block was placed, removing the block's window also removes the
// overlap, and that slice stops reading as busy. Narrow, and strictly better
// than the alternative of every managed block blocking all placement.
func subtractManaged(busy, managed []hours.Window) []hours.Window {
	if len(managed) == 0 {
		return busy
	}
	return hours.SubtractBusy(busy, managed)
}
