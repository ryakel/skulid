package httpx

import (
	"testing"
	"time"
)

// ev is a tiny helper that builds a plannerEvent at a given start/end on a
// fixed reference date — keeps the table tests below readable.
func ev(t *testing.T, startHHMM, endHHMM string) plannerEvent {
	t.Helper()
	loc := time.UTC
	const ref = "2026-05-04"
	start, err := time.ParseInLocation("2006-01-02 15:04", ref+" "+startHHMM, loc)
	if err != nil {
		t.Fatalf("parse start %q: %v", startHHMM, err)
	}
	end, err := time.ParseInLocation("2006-01-02 15:04", ref+" "+endHHMM, loc)
	if err != nil {
		t.Fatalf("parse end %q: %v", endHHMM, err)
	}
	return plannerEvent{Start: start, End: end}
}

func TestAssignEventLanesEmpty(t *testing.T) {
	assignEventLanes(nil) // doesn't panic
	assignEventLanes([]plannerEvent{})
}

func TestAssignEventLanesSoloEventsGetFullWidth(t *testing.T) {
	in := []plannerEvent{
		ev(t, "09:00", "10:00"),
		ev(t, "11:00", "12:00"),
		ev(t, "14:00", "15:00"),
	}
	assignEventLanes(in)
	for i, e := range in {
		if e.Lane != 0 || e.Lanes != 1 {
			t.Errorf("event %d: got Lane=%d Lanes=%d, want Lane=0 Lanes=1", i, e.Lane, e.Lanes)
		}
	}
}

func TestAssignEventLanesTwoOverlap(t *testing.T) {
	in := []plannerEvent{
		ev(t, "09:00", "10:00"),
		ev(t, "09:30", "10:30"),
	}
	assignEventLanes(in)
	if in[0].Lane != 0 || in[1].Lane != 1 {
		t.Errorf("lanes: got %d / %d, want 0 / 1", in[0].Lane, in[1].Lane)
	}
	if in[0].Lanes != 2 || in[1].Lanes != 2 {
		t.Errorf("Lanes count: got %d / %d, want both 2", in[0].Lanes, in[1].Lanes)
	}
}

func TestAssignEventLanesGreedyReusesLane(t *testing.T) {
	// Three events: A and B overlap; C starts after both end, so it can reuse
	// lane 0. Cluster of A/B is one cluster; C is a separate solo cluster.
	in := []plannerEvent{
		ev(t, "09:00", "10:00"),
		ev(t, "09:30", "10:30"),
		ev(t, "11:00", "12:00"),
	}
	assignEventLanes(in)
	if in[0].Lane != 0 || in[1].Lane != 1 {
		t.Errorf("first cluster lanes: got %d / %d, want 0 / 1", in[0].Lane, in[1].Lane)
	}
	if in[0].Lanes != 2 || in[1].Lanes != 2 {
		t.Errorf("first cluster Lanes count wrong")
	}
	if in[2].Lane != 0 || in[2].Lanes != 1 {
		t.Errorf("solo event after gap should be Lane=0 Lanes=1, got Lane=%d Lanes=%d", in[2].Lane, in[2].Lanes)
	}
}

func TestAssignEventLanesTransitiveCluster(t *testing.T) {
	// A overlaps B, B overlaps C, but A doesn't directly overlap C. They form
	// one cluster (overlap is transitive for cluster purposes). The greedy
	// allocator finds that lane 0 frees up at 10:00, so C reuses it — max
	// concurrent in the cluster is 2, not 3, and Lanes should be 2.
	in := []plannerEvent{
		ev(t, "09:00", "10:00"),
		ev(t, "09:30", "11:00"),
		ev(t, "10:30", "12:00"),
	}
	assignEventLanes(in)
	for i, e := range in {
		if e.Lanes != 2 {
			t.Errorf("event %d: Lanes=%d, want 2 (max concurrent in cluster)", i, e.Lanes)
		}
	}
	// First two need different lanes; third reclaims lane 0.
	if in[0].Lane == in[1].Lane {
		t.Errorf("overlapping events shouldn't share a lane: %d / %d", in[0].Lane, in[1].Lane)
	}
	if in[2].Lane != 0 {
		t.Errorf("C should reuse lane 0 after A frees it; got %d", in[2].Lane)
	}
}

func TestAssignEventLanesAdjacentNotOverlapping(t *testing.T) {
	// Back-to-back events (9-10, 10-11) don't overlap — second can reuse the
	// first's lane.
	in := []plannerEvent{
		ev(t, "09:00", "10:00"),
		ev(t, "10:00", "11:00"),
	}
	assignEventLanes(in)
	for i, e := range in {
		if e.Lane != 0 || e.Lanes != 1 {
			t.Errorf("event %d: got Lane=%d Lanes=%d, want both 0/1", i, e.Lane, e.Lanes)
		}
	}
}
