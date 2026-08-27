package sync_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ryakel/skulid/internal/calendar"
	"github.com/ryakel/skulid/internal/calendar/calfake"
	"github.com/ryakel/skulid/internal/db"
	"github.com/ryakel/skulid/internal/db/dbtest"
	syncengine "github.com/ryakel/skulid/internal/sync"
)

// PlaceTask reconciles a task's calendar blocks rather than rebuilding them,
// so that the churn on a calendar other people can see is limited to the
// blocks that actually moved. That reconcile has three branches -- unchanged,
// too many, too few -- and none of them was exercised.

const taskCalGoogleID = "task-cal"

type schedulerFixture struct {
	Scheduler *syncengine.Scheduler
	Fake      *calfake.Client
	Pool      *pgxpool.Pool
	Tasks     *db.TaskRepo
	Chunks    *db.TaskChunkRepo
	CalID     int64
}

// newSchedulerFixture wires a real scheduler over a throwaway database with
// 09:00-17:00 working hours every day, in UTC, so the tests can reason about
// exact windows.
func newSchedulerFixture(t *testing.T) schedulerFixture {
	t.Helper()
	pool := dbtest.New(t)
	ctx := context.Background()

	accountID, calID := dbtest.SeedCalendar(t, pool, "owner@example.com", taskCalGoogleID)

	// Every day, so a test's window doesn't depend on which day it runs.
	hours := json.RawMessage(`{"time_zone":"UTC","days":{
		"mon":["09:00-17:00"],"tue":["09:00-17:00"],"wed":["09:00-17:00"],
		"thu":["09:00-17:00"],"fri":["09:00-17:00"],"sat":["09:00-17:00"],
		"sun":["09:00-17:00"]}}`)
	if err := db.NewAccountRepo(pool).UpdateHours(ctx, accountID, hours, nil, nil); err != nil {
		t.Fatalf("setting hours: %v", err)
	}

	fake := calfake.New()
	fake.Seed(taskCalGoogleID)

	tasks := db.NewTaskRepo(pool)
	chunks := db.NewTaskChunkRepo(pool)
	clientFor := func(context.Context, int64) (calendar.API, error) { return fake, nil }
	sch := syncengine.NewScheduler(tasks, chunks, db.NewHabitRepo(pool),
		db.NewHabitOccurrenceRepo(pool), db.NewAccountRepo(pool), db.NewCalendarRepo(pool),
		db.NewManagedWindowRepo(pool), db.NewSettingRepo(pool), db.NewAuditRepo(pool),
		clientFor, slog.New(slog.NewTextHandler(io.Discard, nil)))

	return schedulerFixture{Scheduler: sch, Fake: fake, Pool: pool, Tasks: tasks, Chunks: chunks, CalID: calID}
}

func (f schedulerFixture) newTask(t *testing.T, title string, minutes int, due *time.Time) int64 {
	t.Helper()
	id, err := f.Tasks.Create(context.Background(), &db.Task{
		Title: title, DurationMinutes: minutes, DueAt: due, TargetCalendarID: f.CalID,
	})
	if err != nil {
		t.Fatalf("creating task: %v", err)
	}
	return id
}

func TestPlaceTaskWritesOneBlockThenLeavesItAlone(t *testing.T) {
	f := newSchedulerFixture(t)
	ctx := context.Background()
	id := f.newTask(t, "Write the report", 60, nil)

	if err := f.Scheduler.PlaceTask(ctx, id); err != nil {
		t.Fatalf("first placement: %v", err)
	}
	if got := len(f.Fake.CallsOf("insert")); got != 1 {
		t.Fatalf("want one block inserted, got %d", got)
	}
	chunks, _ := f.Chunks.ListByTask(ctx, id)
	if len(chunks) != 1 {
		t.Fatalf("want one chunk row, got %d", len(chunks))
	}
	// A single-block task keeps its plain title, so it looks as it always did.
	if got := f.Fake.CallsOf("insert")[0].Event.Summary; got != "Write the report" {
		t.Errorf("summary = %q, want the unadorned title", got)
	}
	task, _ := f.Tasks.Get(ctx, id)
	if task.Status != db.TaskScheduled || task.ScheduleNote != "" {
		t.Errorf("want scheduled with no note, got %q / %q", task.Status, task.ScheduleNote)
	}
	if task.ScheduledStartsAt == nil || !task.ScheduledStartsAt.Equal(chunks[0].StartsAt) {
		t.Errorf("summary columns don't match the first chunk")
	}

	// Re-placing an unchanged plan must be a no-op, not a rewrite. This is
	// what keeps the 6-hour maintenance tick from shuffling the calendar.
	f.Fake.Reset()
	if err := f.Scheduler.PlaceTask(ctx, id); err != nil {
		t.Fatalf("second placement: %v", err)
	}
	if writes := f.Fake.Calls(); len(writes) != 0 {
		t.Fatalf("an unchanged plan must write nothing, got %+v", writes)
	}
}

func TestPlaceTaskSplitsALongTaskAcrossBlocks(t *testing.T) {
	f := newSchedulerFixture(t)
	ctx := context.Background()

	// 12 hours of work cannot fit in any single 8-hour day, so it has to
	// split -- the whole point of SKUL-11.
	id := f.newTask(t, "Write the report", 12*60, nil)
	if err := f.Scheduler.PlaceTask(ctx, id); err != nil {
		t.Fatalf("placement: %v", err)
	}

	chunks, _ := f.Chunks.ListByTask(ctx, id)
	if len(chunks) < 2 {
		t.Fatalf("a 12h task should split, got %d chunk(s)", len(chunks))
	}
	var total time.Duration
	for i, c := range chunks {
		if c.Seq != i {
			t.Errorf("chunk %d has seq %d; seq must stay a dense prefix", i, c.Seq)
		}
		total += c.EndsAt.Sub(c.StartsAt)
	}
	if total != 12*time.Hour {
		t.Errorf("chunks total %v, want 12h", total)
	}
	// Split blocks are numbered so they read as one task, not duplicates.
	for _, call := range f.Fake.CallsOf("insert") {
		if call.Event.Summary == "Write the report" {
			t.Errorf("a split block should be numbered, got the plain title")
		}
	}
	task, _ := f.Tasks.Get(ctx, id)
	if task.Status != db.TaskScheduled {
		t.Errorf("status = %q, want scheduled", task.Status)
	}
}

func TestPlaceTaskShrinkingTheTaskDeletesTheSurplusBlocks(t *testing.T) {
	f := newSchedulerFixture(t)
	ctx := context.Background()

	id := f.newTask(t, "Write the report", 12*60, nil)
	if err := f.Scheduler.PlaceTask(ctx, id); err != nil {
		t.Fatalf("placement: %v", err)
	}
	before, _ := f.Chunks.ListByTask(ctx, id)
	if len(before) < 2 {
		t.Fatalf("need a split task to shrink, got %d chunk(s)", len(before))
	}

	// Shrink it to something that fits in one window.
	task, _ := f.Tasks.Get(ctx, id)
	task.DurationMinutes = 60
	if err := f.Tasks.Update(ctx, task); err != nil {
		t.Fatalf("shrinking: %v", err)
	}

	f.Fake.Reset()
	if err := f.Scheduler.PlaceTask(ctx, id); err != nil {
		t.Fatalf("re-placement: %v", err)
	}

	after, _ := f.Chunks.ListByTask(ctx, id)
	if len(after) != 1 {
		t.Fatalf("want one chunk after shrinking, got %d", len(after))
	}
	// The surplus blocks must be deleted on Google too, not just in the DB.
	if got, want := len(f.Fake.CallsOf("delete")), len(before)-1; got != want {
		t.Errorf("deleted %d blocks, want %d", got, want)
	}
	if got := len(f.Fake.Events(taskCalGoogleID)); got != 1 {
		t.Errorf("calendar should hold one block, got %d", got)
	}
	// The kept block is reused rather than deleted and recreated.
	if got := len(f.Fake.CallsOf("insert")); got != 0 {
		t.Errorf("shrinking should reuse the first block, got %d inserts", got)
	}
}

func TestPlaceTaskGrowingTheTaskInsertsOnlyTheShortfall(t *testing.T) {
	f := newSchedulerFixture(t)
	ctx := context.Background()

	id := f.newTask(t, "Write the report", 60, nil)
	if err := f.Scheduler.PlaceTask(ctx, id); err != nil {
		t.Fatalf("placement: %v", err)
	}

	task, _ := f.Tasks.Get(ctx, id)
	task.DurationMinutes = 12 * 60
	if err := f.Tasks.Update(ctx, task); err != nil {
		t.Fatalf("growing: %v", err)
	}

	f.Fake.Reset()
	if err := f.Scheduler.PlaceTask(ctx, id); err != nil {
		t.Fatalf("re-placement: %v", err)
	}

	after, _ := f.Chunks.ListByTask(ctx, id)
	if len(after) < 2 {
		t.Fatalf("want a split task after growing, got %d chunk(s)", len(after))
	}
	// The existing block is moved, not deleted and recreated: only the
	// shortfall is inserted.
	if got, want := len(f.Fake.CallsOf("insert")), len(after)-1; got != want {
		t.Errorf("inserted %d blocks, want %d (the shortfall only)", got, want)
	}
	if got := len(f.Fake.CallsOf("delete")); got != 0 {
		t.Errorf("growing should delete nothing, got %d deletes", got)
	}
}

func TestPlaceTaskReportsWhyItCannotFit(t *testing.T) {
	f := newSchedulerFixture(t)
	ctx := context.Background()

	// Due tomorrow, but needing far more hours than remain before then.
	due := time.Now().Add(24 * time.Hour).UTC().Truncate(time.Second)
	id := f.newTask(t, "Impossible", 40*60, &due)
	if err := f.Scheduler.PlaceTask(ctx, id); err != nil {
		t.Fatalf("placement: %v", err)
	}

	// Placement is all-or-nothing: booking part of it and calling the task
	// scheduled would be a lie the user only finds out about at the deadline.
	if writes := f.Fake.Calls(); len(writes) != 0 {
		t.Errorf("a task that cannot fit must book nothing, got %+v", writes)
	}
	if chunks, _ := f.Chunks.ListByTask(ctx, id); len(chunks) != 0 {
		t.Errorf("want no chunks, got %d", len(chunks))
	}

	task, _ := f.Tasks.Get(ctx, id)
	if task.Status != db.TaskPending {
		t.Errorf("status = %q, want pending", task.Status)
	}
	if task.ScheduleNote == "" {
		t.Error("a task that cannot be placed must say why; the note is empty")
	}
}

func TestPlaceTaskClearsBlocksWhenTheTaskStopsFitting(t *testing.T) {
	f := newSchedulerFixture(t)
	ctx := context.Background()

	id := f.newTask(t, "Write the report", 60, nil)
	if err := f.Scheduler.PlaceTask(ctx, id); err != nil {
		t.Fatalf("placement: %v", err)
	}
	if got := len(f.Fake.Events(taskCalGoogleID)); got != 1 {
		t.Fatalf("want a block to clear, got %d", got)
	}

	// Give it a deadline already in the past for the working hours available.
	task, _ := f.Tasks.Get(ctx, id)
	task.DurationMinutes = 40 * 60
	due := time.Now().Add(24 * time.Hour).UTC().Truncate(time.Second)
	task.DueAt = &due
	if err := f.Tasks.Update(ctx, task); err != nil {
		t.Fatalf("updating: %v", err)
	}

	f.Fake.Reset()
	if err := f.Scheduler.PlaceTask(ctx, id); err != nil {
		t.Fatalf("re-placement: %v", err)
	}

	if got := len(f.Fake.CallsOf("delete")); got != 1 {
		t.Errorf("want the stale block deleted, got %d deletes", got)
	}
	if got := len(f.Fake.Events(taskCalGoogleID)); got != 0 {
		t.Errorf("calendar should be empty, got %d events", got)
	}
	if chunks, _ := f.Chunks.ListByTask(ctx, id); len(chunks) != 0 {
		t.Errorf("chunk rows outlived the blocks: %+v", chunks)
	}
	task, _ = f.Tasks.Get(ctx, id)
	if task.Status != db.TaskPending || task.ScheduleNote == "" {
		t.Errorf("want pending with a reason, got %q / %q", task.Status, task.ScheduleNote)
	}
}

// A task must not treat its own blocks as busy, or a scheduled task could
// never stay where it is.
func TestPlaceTaskDoesNotEvictItself(t *testing.T) {
	f := newSchedulerFixture(t)
	ctx := context.Background()

	id := f.newTask(t, "Write the report", 4*60, nil)
	if err := f.Scheduler.PlaceTask(ctx, id); err != nil {
		t.Fatalf("placement: %v", err)
	}
	first, _ := f.Chunks.ListByTask(ctx, id)
	if len(first) != 1 {
		t.Fatalf("want one block, got %d", len(first))
	}

	for i := 0; i < 3; i++ {
		if err := f.Scheduler.PlaceTask(ctx, id); err != nil {
			t.Fatalf("re-placement %d: %v", i, err)
		}
	}
	again, _ := f.Chunks.ListByTask(ctx, id)
	if len(again) != 1 || !again[0].StartsAt.Equal(first[0].StartsAt) {
		t.Errorf("the block moved under repeated placement: %v -> %v", first[0].StartsAt, again[0].StartsAt)
	}
}
