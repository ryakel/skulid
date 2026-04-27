package ai

import (
	"encoding/json"
	"testing"
	"time"
)

func TestIsWriteCoversTheRightSet(t *testing.T) {
	writes := []string{"create_event", "update_event", "delete_event", "move_event"}
	reads := []string{"list_calendars", "list_events", "find_event", "find_free_time"}

	for _, name := range writes {
		if !IsWrite(name) {
			t.Errorf("expected %q to be a write tool", name)
		}
	}
	for _, name := range reads {
		if IsWrite(name) {
			t.Errorf("expected %q to NOT be a write tool", name)
		}
	}
	if IsWrite("nonexistent_tool") {
		t.Error("unknown tools should not register as writes")
	}
}

func TestDefsAreValidJSONAndCoverAllTools(t *testing.T) {
	defs := Defs()
	if len(defs) < 8 {
		t.Fatalf("expected at least 8 tool defs, got %d", len(defs))
	}
	want := map[string]bool{
		"list_calendars": false, "list_events": false, "find_event": false,
		"find_free_time": false, "create_event": false, "update_event": false,
		"delete_event": false, "move_event": false,
	}
	for _, d := range defs {
		if d.Name == "" {
			t.Error("tool def has empty name")
		}
		if d.Description == "" {
			t.Errorf("tool def %q has empty description", d.Name)
		}
		var schema map[string]any
		if err := json.Unmarshal(d.InputSchema, &schema); err != nil {
			t.Errorf("tool def %q has invalid input_schema JSON: %v", d.Name, err)
		}
		if schema["type"] != "object" {
			t.Errorf("tool def %q input_schema must have type=object", d.Name)
		}
		want[d.Name] = true
	}
	for name, found := range want {
		if !found {
			t.Errorf("expected tool %q to be in Defs()", name)
		}
	}
}

func TestDescribeProducesNonEmptyOutputForKnownTools(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"create_event", `{"calendar_id":1,"summary":"Test","start":"2026-04-27T10:00:00Z","end":"2026-04-27T11:00:00Z"}`},
		{"update_event", `{"calendar_id":1,"event_id":"abc"}`},
		{"delete_event", `{"calendar_id":1,"event_id":"abc"}`},
		{"move_event", `{"calendar_id":1,"event_id":"abc","new_start":"2026-04-27T11:00:00Z","new_end":"2026-04-27T12:00:00Z"}`},
	}
	for _, tc := range cases {
		got := Describe(tc.name, json.RawMessage(tc.input))
		if got == "" || got == tc.name {
			t.Errorf("Describe(%q): expected human-readable output, got %q", tc.name, got)
		}
	}
}

func TestDescribeFallsBackToToolNameForUnknown(t *testing.T) {
	if got := Describe("custom_thing", json.RawMessage(`{}`)); got != "custom_thing" {
		t.Errorf("expected fallback to tool name, got %q", got)
	}
}

func TestMergeBusyCoalescesOverlap(t *testing.T) {
	w := func(s, e string) timeWin {
		ts, _ := time.Parse(time.RFC3339, s)
		te, _ := time.Parse(time.RFC3339, e)
		return timeWin{ts, te}
	}
	in := []timeWin{
		w("2026-04-27T09:00:00Z", "2026-04-27T10:00:00Z"),
		w("2026-04-27T09:30:00Z", "2026-04-27T11:00:00Z"),
		w("2026-04-27T13:00:00Z", "2026-04-27T14:00:00Z"),
	}
	got := mergeBusy(in)
	if len(got) != 2 {
		t.Fatalf("expected 2 merged windows, got %d: %+v", len(got), got)
	}
	if !got[0].start.Equal(in[0].start) || !got[0].end.Equal(in[1].end) {
		t.Errorf("first merged window wrong: %+v", got[0])
	}
}

func TestMergeBusyEmpty(t *testing.T) {
	if mergeBusy(nil) != nil {
		t.Fatal("expected nil for empty input")
	}
}

func TestSummarizeShortReturnsAsIs(t *testing.T) {
	if got := summarize("hi"); got != "hi" {
		t.Errorf("expected 'hi', got %q", got)
	}
}

func TestSummarizeTruncates(t *testing.T) {
	long := "this is a very long input that should be truncated because it exceeds the maximum title length permitted"
	got := summarize(long)
	if len([]rune(got)) > 70 {
		t.Errorf("expected truncation, got len=%d: %q", len([]rune(got)), got)
	}
	if got[len(got)-3:] != "…" {
		t.Errorf("expected ellipsis at end, got %q", got)
	}
}

func TestSummarizeEmpty(t *testing.T) {
	if got := summarize("   "); got != "(empty)" {
		t.Errorf("expected '(empty)', got %q", got)
	}
}

func TestSystemPromptIncludesCurrentTime(t *testing.T) {
	now := time.Date(2026, 4, 27, 10, 0, 0, 0, time.UTC)
	got := SystemPrompt(now)
	if !contains(got, "2026-04-27T10:00:00Z") {
		t.Errorf("expected current time in prompt, got: %s", got)
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
