package httpx

import (
	"reflect"
	"testing"
)

func TestParseInt64(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"", 0},
		{"  ", 0},
		{"42", 42},
		{" 42 ", 42},
		{"-7", -7},
		{"not a number", 0},
	}
	for _, tc := range cases {
		if got := parseInt64(tc.in); got != tc.want {
			t.Errorf("parseInt64(%q): got %d want %d", tc.in, got, tc.want)
		}
	}
}

func TestStrOr(t *testing.T) {
	if got := strOr("", "fallback"); got != "fallback" {
		t.Errorf("empty input should return fallback, got %q", got)
	}
	if got := strOr("   ", "fallback"); got != "fallback" {
		t.Errorf("whitespace-only input should return fallback, got %q", got)
	}
	if got := strOr("set", "fallback"); got != "set" {
		t.Errorf("set input should return itself, got %q", got)
	}
}

func TestSplitCSV(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", []string{}},
		{"a", []string{"a"}},
		{"a,b,c", []string{"a", "b", "c"}},
		{" a , b ,  c", []string{"a", "b", "c"}},
		{",,a,,b,,", []string{"a", "b"}},
	}
	for _, tc := range cases {
		got := splitCSV(tc.in)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("splitCSV(%q): got %v want %v", tc.in, got, tc.want)
		}
	}
}

// TestRendererParsesAllTemplates is a smoke test: it catches template syntax
// errors in any of the embedded HTML files at test time instead of at first
// page load. The render path itself is not exercised — that would require
// fully constructed page data for every page.
func TestRendererParsesAllTemplates(t *testing.T) {
	r, err := NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}
	expected := []string{
		"dashboard", "login", "accounts", "rules", "rule_edit",
		"blocks", "block_edit", "audit", "settings", "categories",
		"hours", "buffers", "tasks", "task_edit", "habits", "habit_edit",
		"planner", "priorities", "assistant_list", "assistant_chat",
	}
	for _, name := range expected {
		if _, ok := r.pages[name]; !ok {
			t.Errorf("renderer is missing template %q", name)
		}
	}
}
