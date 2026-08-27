package sync_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	gcal "google.golang.org/api/calendar/v3"

	"github.com/ryakel/skulid/internal/calendar"
	"github.com/ryakel/skulid/internal/calendar/calfake"
	"github.com/ryakel/skulid/internal/db"
	"github.com/ryakel/skulid/internal/db/dbtest"
	syncengine "github.com/ryakel/skulid/internal/sync"
)

// rule_engine.go is flagged in CLAUDE.md and wiki/Sync-Rules.md as the
// trickiest file in the codebase, and every one of its invariants is the kind
// that looks obviously correct until it isn't. These drive it with real rows
// and real SQL, faking only Google.

const (
	srcGoogleID = "source-cal"
	tgtGoogleID = "target-cal"
)

type engineFixture struct {
	Engine *syncengine.Engine
	Fake   *calfake.Client
	Pool   *pgxpool.Pool
	Links  *db.EventLinkRepo
	Audit  *db.AuditRepo
	RuleID int64
	SrcID  int64
	TgtID  int64
}

// newEngineFixture wires a real engine over a throwaway database and one fake
// calendar backing both sides, so a mirror written to the target is visible
// when the reverse pass reads it back.
func newEngineFixture(t *testing.T, rule *db.SyncRule) engineFixture {
	t.Helper()
	pool := dbtest.New(t)
	ctx := context.Background()

	_, srcID := dbtest.SeedCalendar(t, pool, "owner@example.com", srcGoogleID)
	_, tgtID := dbtest.SeedCalendar(t, pool, "owner@example.com", tgtGoogleID)

	fake := calfake.New()
	// Both calendars must exist in the fake even when empty, so freebusy and
	// list calls answer rather than 404.
	fake.Seed(srcGoogleID)
	fake.Seed(tgtGoogleID)

	rules := db.NewSyncRuleRepo(pool)
	rule.SourceCalendarID = srcID
	rule.TargetCalendarID = tgtID
	ruleID, err := rules.Create(ctx, rule)
	if err != nil {
		t.Fatalf("creating rule: %v", err)
	}

	links := db.NewEventLinkRepo(pool)
	audit := db.NewAuditRepo(pool)
	clientFor := func(context.Context, int64) (calendar.API, error) { return fake, nil }
	engine := syncengine.NewEngine(rules, db.NewAccountRepo(pool), db.NewCalendarRepo(pool),
		links, audit, clientFor, slog.New(slog.NewTextHandler(io.Discard, nil)))

	return engineFixture{
		Engine: engine, Fake: fake, Pool: pool, Links: links, Audit: audit,
		RuleID: ruleID, SrcID: srcID, TgtID: tgtID,
	}
}

func meetingAt(id, title string, start time.Time) *gcal.Event {
	return &gcal.Event{
		Id:      id,
		Summary: title,
		Etag:    `"etag-` + id + `"`,
		Start:   &gcal.EventDateTime{DateTime: start.Format(time.RFC3339)},
		End:     &gcal.EventDateTime{DateTime: start.Add(time.Hour).Format(time.RFC3339)},
	}
}

func oneWayRule() *db.SyncRule {
	return &db.SyncRule{Name: "one way", Direction: "one_way", PrimarySide: "source", Enabled: true}
}

func bidiRule() *db.SyncRule {
	return &db.SyncRule{Name: "bidi", Direction: "bidirectional", PrimarySide: "source", Enabled: true}
}

func auditActions(t *testing.T, audit *db.AuditRepo) []string {
	t.Helper()
	entries, err := audit.Recent(context.Background(), 50)
	if err != nil {
		t.Fatalf("reading audit: %v", err)
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Action)
	}
	return out
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// The loop guard. Without it two bidirectional rules ping-pong indefinitely,
// so this is the single most important thing the engine does.
func TestEngineNeverForwardsAManagedEvent(t *testing.T) {
	f := newEngineFixture(t, oneWayRule())
	ctx := context.Background()
	start := time.Now().Add(time.Hour).UTC().Truncate(time.Second)

	for name, props := range map[string]map[string]string{
		"skulid rule mirror":   {"skulidManaged": "1", "skulidRuleId": "9"},
		"skulid smart block":   {"skulidManaged": "1", "skulidSmartBlockId": "4"},
		"skulid travel buffer": {"skulidManaged": "1", "skulidBufferType": "travel"},
		// Pre-rename events must still trip the guard, or an instance
		// upgraded from calmAxolotl starts looping on its own history.
		"legacy calmAxolotl": {"calmAxolotlManaged": "1"},
	} {
		t.Run(name, func(t *testing.T) {
			ev := meetingAt("managed-"+name, "Managed", start)
			ev.ExtendedProperties = &gcal.EventExtendedProperties{Private: props}
			if err := f.Engine.ProcessChange(ctx, f.SrcID, ev); err != nil {
				t.Fatalf("ProcessChange: %v", err)
			}
		})
	}

	if writes := f.Fake.Calls(); len(writes) != 0 {
		t.Fatalf("a managed event must never be forwarded, got %d writes: %+v", len(writes), writes)
	}
	if evs := f.Fake.Events(tgtGoogleID); len(evs) != 0 {
		t.Errorf("target calendar should be empty, got %d events", len(evs))
	}
}

func TestEngineMirrorsThenUpdatesInPlace(t *testing.T) {
	f := newEngineFixture(t, oneWayRule())
	ctx := context.Background()
	start := time.Now().Add(time.Hour).UTC().Truncate(time.Second)

	ev := meetingAt("src-1", "Standup", start)
	f.Fake.Seed(srcGoogleID, ev)
	if err := f.Engine.ProcessChange(ctx, f.SrcID, ev); err != nil {
		t.Fatalf("first pass: %v", err)
	}

	inserts := f.Fake.CallsOf("insert")
	if len(inserts) != 1 {
		t.Fatalf("want one mirror inserted, got %d", len(inserts))
	}
	mirror := inserts[0].Event
	// Every mirror must carry the loop-guard properties, or the next inbound
	// webhook forwards it straight back.
	if mirror.ExtendedProperties == nil || mirror.ExtendedProperties.Private["skulidManaged"] != "1" {
		t.Errorf("mirror is missing skulidManaged: %+v", mirror.ExtendedProperties)
	}
	if mirror.ExtendedProperties.Private["skulidSourceEventId"] != "src-1" {
		t.Errorf("mirror does not name its source: %+v", mirror.ExtendedProperties.Private)
	}

	link, err := f.Links.Get(ctx, f.RuleID, "src-1")
	if err != nil || link == nil {
		t.Fatalf("no event_link recorded: %v (%v)", err, link)
	}

	// A second pass with a changed source updates the same mirror rather than
	// inserting a duplicate.
	f.Fake.Reset()
	ev.Summary = "Standup (moved)"
	ev.Etag = `"etag-src-1-v2"`
	if err := f.Engine.ProcessChange(ctx, f.SrcID, ev); err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if got := len(f.Fake.CallsOf("insert")); got != 0 {
		t.Errorf("second pass inserted %d duplicate mirrors", got)
	}
	if got := len(f.Fake.CallsOf("update")); got != 1 {
		t.Errorf("want one in-place update, got %d", got)
	}
	if evs := f.Fake.Events(tgtGoogleID); len(evs) != 1 {
		t.Errorf("target should hold exactly one mirror, got %d", len(evs))
	}
}

// Etag dedup on bidirectional rules. Without it, an outbound mirror update
// arrives back as an inbound webhook and the two calendars update each other
// forever.
func TestEngineSkipsUnchangedEtagOnBidirectionalRule(t *testing.T) {
	f := newEngineFixture(t, bidiRule())
	ctx := context.Background()
	start := time.Now().Add(time.Hour).UTC().Truncate(time.Second)

	ev := meetingAt("src-1", "Standup", start)
	f.Fake.Seed(srcGoogleID, ev)
	if err := f.Engine.ProcessChange(ctx, f.SrcID, ev); err != nil {
		t.Fatalf("first pass: %v", err)
	}
	if got := len(f.Fake.CallsOf("insert")); got != 1 {
		t.Fatalf("want one mirror, got %d", got)
	}

	// Same event, same etag, delivered again -- as a webhook echo would.
	f.Fake.Reset()
	if err := f.Engine.ProcessChange(ctx, f.SrcID, ev); err != nil {
		t.Fatalf("echo pass: %v", err)
	}
	if writes := f.Fake.Calls(); len(writes) != 0 {
		t.Fatalf("an unchanged etag must write nothing, got %+v", writes)
	}

	// A genuinely changed etag still gets through, or the dedup would freeze
	// the mirror permanently.
	f.Fake.Reset()
	ev.Etag = `"etag-src-1-v2"`
	ev.Summary = "Standup (moved)"
	if err := f.Engine.ProcessChange(ctx, f.SrcID, ev); err != nil {
		t.Fatalf("changed pass: %v", err)
	}
	if got := len(f.Fake.CallsOf("update")); got != 1 {
		t.Errorf("a changed etag should update the mirror, got %d updates", got)
	}
}

// A one-way rule has no etag dedup: the target is never a source, so there is
// no loop to break, and skipping would drop real edits.
func TestEngineDoesNotDedupeEtagOnOneWayRule(t *testing.T) {
	f := newEngineFixture(t, oneWayRule())
	ctx := context.Background()
	start := time.Now().Add(time.Hour).UTC().Truncate(time.Second)

	ev := meetingAt("src-1", "Standup", start)
	f.Fake.Seed(srcGoogleID, ev)
	if err := f.Engine.ProcessChange(ctx, f.SrcID, ev); err != nil {
		t.Fatalf("first pass: %v", err)
	}
	f.Fake.Reset()
	if err := f.Engine.ProcessChange(ctx, f.SrcID, ev); err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if got := len(f.Fake.CallsOf("update")); got != 1 {
		t.Errorf("one-way rules re-apply rather than dedupe; got %d updates", got)
	}
}

// Cancellation must delete the mirror even when the filter no longer matches.
// The deletion path deliberately runs before the filter check -- get that
// order wrong and a cancelled meeting outside the filter leaves its mirror
// on the calendar forever.
func TestEngineCancelledSourceDeletesMirrorDespiteFilter(t *testing.T) {
	rule := oneWayRule()
	rule.Filter = json.RawMessage(`{"title_regex":"^Standup"}`)
	f := newEngineFixture(t, rule)
	ctx := context.Background()
	start := time.Now().Add(time.Hour).UTC().Truncate(time.Second)

	ev := meetingAt("src-1", "Standup", start)
	f.Fake.Seed(srcGoogleID, ev)
	if err := f.Engine.ProcessChange(ctx, f.SrcID, ev); err != nil {
		t.Fatalf("first pass: %v", err)
	}
	if got := len(f.Fake.Events(tgtGoogleID)); got != 1 {
		t.Fatalf("want a mirror to delete, got %d target events", got)
	}

	// Cancelled, and renamed so the filter would reject it.
	f.Fake.Reset()
	cancelled := meetingAt("src-1", "Something else entirely", start)
	cancelled.Status = "cancelled"
	if err := f.Engine.ProcessChange(ctx, f.SrcID, cancelled); err != nil {
		t.Fatalf("cancel pass: %v", err)
	}

	if got := len(f.Fake.CallsOf("delete")); got != 1 {
		t.Fatalf("want the mirror deleted, got %d deletes", got)
	}
	if got := len(f.Fake.Events(tgtGoogleID)); got != 0 {
		t.Errorf("target should be empty after cancellation, got %d", got)
	}
	if link, _ := f.Links.Get(ctx, f.RuleID, "src-1"); link != nil {
		t.Errorf("event_link outlived the mirror: %+v", link)
	}
	if acts := auditActions(t, f.Audit); !contains(acts, "delete") {
		t.Errorf("cancellation should audit as delete, got %v", acts)
	}
}

// A filter that stopped matching drops the mirror, audited as filter_drop.
// This is what the Re-run backfill button relies on to clean up after a
// narrowed filter, so it also has to leave no event_link behind.
func TestEngineFilterDropRemovesTheMirror(t *testing.T) {
	rule := oneWayRule()
	rule.Filter = json.RawMessage(`{"title_regex":"^Standup"}`)
	f := newEngineFixture(t, rule)
	ctx := context.Background()
	start := time.Now().Add(time.Hour).UTC().Truncate(time.Second)

	ev := meetingAt("src-1", "Standup", start)
	f.Fake.Seed(srcGoogleID, ev)
	if err := f.Engine.ProcessChange(ctx, f.SrcID, ev); err != nil {
		t.Fatalf("first pass: %v", err)
	}
	if got := len(f.Fake.Events(tgtGoogleID)); got != 1 {
		t.Fatalf("want a mirror, got %d", got)
	}

	f.Fake.Reset()
	renamed := meetingAt("src-1", "Retro", start)
	renamed.Etag = `"etag-src-1-v2"`
	if err := f.Engine.ProcessChange(ctx, f.SrcID, renamed); err != nil {
		t.Fatalf("rename pass: %v", err)
	}

	if got := len(f.Fake.Events(tgtGoogleID)); got != 0 {
		t.Errorf("a mirror that stopped matching should be deleted, got %d", got)
	}
	if link, _ := f.Links.Get(ctx, f.RuleID, "src-1"); link != nil {
		t.Errorf("event_link outlived the dropped mirror: %+v", link)
	}
	if acts := auditActions(t, f.Audit); !contains(acts, "filter_drop") {
		t.Errorf("want a filter_drop audit entry, got %v", acts)
	}

	// An unmatched event that was never mirrored writes nothing at all.
	f.Fake.Reset()
	never := meetingAt("src-2", "Retro", start)
	if err := f.Engine.ProcessChange(ctx, f.SrcID, never); err != nil {
		t.Fatalf("unmatched pass: %v", err)
	}
	if writes := f.Fake.Calls(); len(writes) != 0 {
		t.Errorf("an event that never matched should write nothing, got %+v", writes)
	}
}

// Forward and reverse passes of a bidirectional rule must not collide on
// event_link's unique index. The reverse pass stores under "rev:"+id.
func TestEngineForwardAndReversePassesDoNotCollide(t *testing.T) {
	f := newEngineFixture(t, bidiRule())
	ctx := context.Background()
	start := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	id := f.RuleID

	fwd := meetingAt("shared-1", "Forward", start)
	f.Fake.Seed(srcGoogleID, fwd)
	if err := f.Engine.ProcessChange(ctx, f.SrcID, fwd); err != nil {
		t.Fatalf("forward pass: %v", err)
	}

	// The same event id arriving from the *target* side is the reverse pass.
	rev := meetingAt("shared-1", "Reverse", start.Add(2*time.Hour))
	rev.Etag = `"etag-shared-1-rev"`
	f.Fake.Seed(tgtGoogleID, rev)
	if err := f.Engine.ProcessChange(ctx, f.TgtID, rev); err != nil {
		t.Fatalf("reverse pass: %v", err)
	}

	forward, err := f.Links.Get(ctx, id, "shared-1")
	if err != nil || forward == nil {
		t.Fatalf("forward link missing: %v (%v)", err, forward)
	}
	reverse, err := f.Links.Get(ctx, id, "rev:shared-1")
	if err != nil || reverse == nil {
		t.Fatalf("reverse link missing -- the passes collided: %v (%v)", err, reverse)
	}
	if forward.ID == reverse.ID {
		t.Fatal("forward and reverse share one event_link row")
	}

	all, err := f.Links.ListByRule(ctx, id)
	if err != nil || len(all) != 2 {
		t.Fatalf("want 2 links for the rule, got %d (%v)", len(all), err)
	}
}

// A rule whose source or target calendar is disabled is dormant: the mirror
// stays as it is on Google rather than being torn down, so re-enabling
// resumes rather than rebuilds.
func TestEngineIsDormantWhenACalendarIsDisabled(t *testing.T) {
	f := newEngineFixture(t, oneWayRule())
	ctx := context.Background()
	start := time.Now().Add(time.Hour).UTC().Truncate(time.Second)

	if err := db.NewCalendarRepo(f.Pool).SetEnabled(ctx, f.TgtID, false); err != nil {
		t.Fatalf("disabling target: %v", err)
	}

	ev := meetingAt("src-1", "Standup", start)
	f.Fake.Seed(srcGoogleID, ev)
	if err := f.Engine.ProcessChange(ctx, f.SrcID, ev); err != nil {
		t.Fatalf("ProcessChange: %v", err)
	}
	if writes := f.Fake.Calls(); len(writes) != 0 {
		t.Fatalf("a dormant rule must write nothing, got %+v", writes)
	}
}
