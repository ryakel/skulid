package db

import "testing"

func TestBufferSettingsPaddingMinutesPicksMax(t *testing.T) {
	cases := []struct {
		b    BufferSettings
		want int
	}{
		{BufferSettings{}, 0},
		{BufferSettings{TaskHabitBreakMinutes: 15}, 15},
		{BufferSettings{DecompressionMinutes: 30}, 30},
		{BufferSettings{TaskHabitBreakMinutes: 15, DecompressionMinutes: 30}, 30},
		{BufferSettings{TaskHabitBreakMinutes: 30, DecompressionMinutes: 15}, 30},
		// Travel minutes intentionally don't influence padding in v1.
		{BufferSettings{TravelMinutes: 60}, 0},
	}
	for _, tc := range cases {
		if got := tc.b.PaddingMinutes(); got != tc.want {
			t.Errorf("%+v: got %d want %d", tc.b, got, tc.want)
		}
	}
}

func TestAtoiOrFallback(t *testing.T) {
	if got := atoiOr("42", 7); got != 42 {
		t.Errorf("got %d want 42", got)
	}
	if got := atoiOr("not a number", 7); got != 7 {
		t.Errorf("got %d want fallback 7", got)
	}
	if got := atoiOr("  ", 7); got != 7 {
		t.Errorf("got %d want fallback 7 for whitespace", got)
	}
}

func TestClampNonNeg(t *testing.T) {
	if got := clampNonNeg(-5); got != 0 {
		t.Errorf("got %d want 0", got)
	}
	if got := clampNonNeg(0); got != 0 {
		t.Errorf("got %d want 0", got)
	}
	if got := clampNonNeg(15); got != 15 {
		t.Errorf("got %d want 15", got)
	}
}
