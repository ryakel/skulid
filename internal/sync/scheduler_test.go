package sync

import (
	"testing"
	"time"
)

func TestChunkTitle(t *testing.T) {
	// A single-block task keeps its plain title, so an ordinary task looks
	// exactly as it always did.
	if got := chunkTitle("Write the report", 0, 1); got != "Write the report" {
		t.Errorf("single block: got %q", got)
	}
	if got := chunkTitle("Write the report", 0, 3); got != "Write the report (1/3)" {
		t.Errorf("first of three: got %q", got)
	}
	if got := chunkTitle("Write the report", 2, 3); got != "Write the report (3/3)" {
		t.Errorf("last of three: got %q", got)
	}
	// total 0 shouldn't be reachable, but must not produce "(1/0)".
	if got := chunkTitle("x", 0, 0); got != "x" {
		t.Errorf("zero total: got %q", got)
	}
}

func TestRoundedDuration(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{30 * time.Minute, "30m"},
		{time.Hour, "1h"},
		{90 * time.Minute, "1h30m"},
		{8 * time.Hour, "8h"},
		{0, "0m"},
		// Seconds round to the nearest minute rather than leaking into the note.
		{time.Hour + 30*time.Second, "1h1m"},
	}
	for _, c := range cases {
		if got := roundedDuration(c.in); got != c.want {
			t.Errorf("roundedDuration(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNoFitNote(t *testing.T) {
	loc := time.UTC
	due := time.Date(2026, 5, 1, 17, 0, 0, 0, loc)

	// Nothing at all fits, with a deadline: name the deadline.
	got := noFitNote(2*time.Hour, 0, &due, loc)
	if want := "No free time before Fri May 1, 5:00 PM."; got != want {
		t.Errorf("got %q want %q", got, want)
	}

	// Nothing fits and no deadline: name the horizon instead.
	got = noFitNote(2*time.Hour, 0, nil, loc)
	if want := "No free time in your working hours over the next two weeks."; got != want {
		t.Errorf("got %q want %q", got, want)
	}

	// Some of it fits: say how much, so the user can act on it.
	got = noFitNote(8*time.Hour, 6*time.Hour, &due, loc)
	want := "Only 6h of the 8h needed would fit. Free up time, shorten the task, or move the due date."
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}
