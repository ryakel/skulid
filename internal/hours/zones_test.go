package hours

import (
	"testing"
	"time"
)

// Every zone we offer has to be one the scheduler can actually load, or the
// picker hands people a value that fails at expansion time — exactly the
// failure the picker exists to prevent.
func TestEveryOfferedZoneLoads(t *testing.T) {
	for _, g := range ZoneGroupsWith("") {
		for _, z := range g.Zones {
			if _, err := time.LoadLocation(z.Name); err != nil {
				t.Errorf("zone %q does not load: %v", z.Name, err)
			}
		}
	}
}

func TestZoneGroupsPreserveAnUnknownCurrentValue(t *testing.T) {
	const exotic = "Antarctica/Troll"
	if Known(exotic) {
		t.Fatalf("%s is in the curated list; pick another for this test", exotic)
	}
	found := false
	for _, g := range ZoneGroupsWith(exotic) {
		for _, z := range g.Zones {
			if z.Name == exotic {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("a stored zone outside the curated list must still be offered, or saving the form would silently drop it")
	}
}

func TestZoneGroupsDoNotDuplicateAKnownCurrentValue(t *testing.T) {
	count := 0
	for _, g := range ZoneGroupsWith("America/Chicago") {
		for _, z := range g.Zones {
			if z.Name == "America/Chicago" {
				count++
			}
		}
	}
	if count != 1 {
		t.Fatalf("America/Chicago appears %d times, want 1", count)
	}
}

func TestZoneLabels(t *testing.T) {
	cases := map[string]string{
		"UTC":                            "UTC",
		"America/Chicago":                "Chicago",
		"America/Argentina/Buenos_Aires": "Buenos Aires",
		"Asia/Ho_Chi_Minh":               "Ho Chi Minh",
	}
	for in, want := range cases {
		if got := labelFor(in); got != want {
			t.Errorf("labelFor(%q) = %q, want %q", in, got, want)
		}
	}
}
