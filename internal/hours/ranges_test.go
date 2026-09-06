package hours

import (
	"testing"
	"time"
)

func TestNormalizeRange(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"09:00-17:00", "09:00-17:00", true},
		{"  09:00-17:00  ", "09:00-17:00", true},
		{"9:00-17:00", "09:00-17:00", true},
		{"9:5-17:5", "09:05-17:05", true},
		{"09:00 - 17:00", "09:00-17:00", true},
		{"09:00–17:00", "09:00-17:00", true}, // en-dash, the paste hazard
		{"09:00—17:00", "09:00-17:00", true}, // em-dash
		{"00:00-23:59", "00:00-23:59", true},

		{"", "", false},
		{"09:00", "", false},
		{"9-5", "", false},
		{"17:00-09:00", "", false},  // inverted
		{"09:00-09:00", "", false},  // empty window
		{"24:00-25:00", "", false},  // hour out of range
		{"09:60-17:00", "", false},  // minute out of range
		{"09:00-17:00x", "", false}, // trailing garbage
		{"09:00-17:00-18:00", "", false},
		{"nine-five", "", false},
	}
	for _, c := range cases {
		got, ok := NormalizeRange(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("NormalizeRange(%q) = (%q, %v), want (%q, %v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestNormalizeRangesNamesTheOffendingEntry(t *testing.T) {
	got, bad := NormalizeRanges(" 9:00-12:00 , 13:00-17:00 ")
	if bad != "" {
		t.Fatalf("unexpected rejection of %q", bad)
	}
	if len(got) != 2 || got[0] != "09:00-12:00" || got[1] != "13:00-17:00" {
		t.Fatalf("got %v", got)
	}

	if _, bad := NormalizeRanges("09:00-12:00,lunch,13:00-17:00"); bad != "lunch" {
		t.Fatalf("want the bad entry named, got %q", bad)
	}

	// Empty segments are skipped rather than rejected: a trailing comma is a
	// typo, not a reason to refuse the whole row.
	if got, bad := NormalizeRanges("09:00-17:00,"); bad != "" || len(got) != 1 {
		t.Fatalf("got %v, bad %q", got, bad)
	}
}

// ParseRange and NormalizeRange must accept exactly the same set: a range the
// setup form takes has to be one the engine can expand.
func TestParseRangeAgreesWithNormalizeRange(t *testing.T) {
	day := time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)
	inputs := []string{
		"09:00-17:00", "9:00-17:00", "09:00 - 17:00", "09:00–17:00",
		"", "09:00", "17:00-09:00", "24:00-25:00", "09:00-17:00x", "9-5",
	}
	for _, in := range inputs {
		_, wantOK := NormalizeRange(in)
		_, _, gotOK := ParseRange(in, day, time.UTC)
		if gotOK != wantOK {
			t.Errorf("ParseRange(%q) ok=%v, NormalizeRange ok=%v", in, gotOK, wantOK)
		}
	}
}

func TestParseRangeBuildsTheWindowInLoc(t *testing.T) {
	loc, err := time.LoadLocation("America/Chicago")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	day := time.Date(2026, 3, 2, 0, 0, 0, 0, loc)
	start, end, ok := ParseRange("9:30-17:00", day, loc)
	if !ok {
		t.Fatal("want ok")
	}
	if start.Hour() != 9 || start.Minute() != 30 || end.Hour() != 17 {
		t.Fatalf("got %v-%v", start, end)
	}
	if start.Location() != loc {
		t.Fatalf("window built in %v, want %v", start.Location(), loc)
	}
}
