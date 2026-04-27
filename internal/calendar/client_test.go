package calendar

import (
	"testing"

	gcal "google.golang.org/api/calendar/v3"
)

func TestIsManaged(t *testing.T) {
	cases := []struct {
		name string
		ev   *gcal.Event
		want bool
	}{
		{"nil event", nil, false},
		{"no extended props", &gcal.Event{}, false},
		{"empty private map", &gcal.Event{
			ExtendedProperties: &gcal.EventExtendedProperties{Private: map[string]string{}},
		}, false},
		{"managed=0", &gcal.Event{
			ExtendedProperties: &gcal.EventExtendedProperties{
				Private: map[string]string{PropManaged: "0"},
			},
		}, false},
		{"managed=1", &gcal.Event{
			ExtendedProperties: &gcal.EventExtendedProperties{
				Private: map[string]string{PropManaged: "1"},
			},
		}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsManaged(tc.ev); got != tc.want {
				t.Fatalf("IsManaged: got %v want %v", got, tc.want)
			}
		})
	}
}

func TestManagedProps(t *testing.T) {
	props := ManagedProps(42, "abc-event-id")
	if props[PropManaged] != "1" {
		t.Errorf("PropManaged: got %q want %q", props[PropManaged], "1")
	}
	if props[PropRuleID] != "42" {
		t.Errorf("PropRuleID: got %q want %q", props[PropRuleID], "42")
	}
	if props[PropSourceEventID] != "abc-event-id" {
		t.Errorf("PropSourceEventID: got %q want %q", props[PropSourceEventID], "abc-event-id")
	}
	// And the round trip: an event built from these props is reported as managed.
	ev := &gcal.Event{
		ExtendedProperties: &gcal.EventExtendedProperties{Private: props},
	}
	if !IsManaged(ev) {
		t.Fatal("expected event built from ManagedProps to be IsManaged")
	}
}

func TestSmartBlockProps(t *testing.T) {
	props := SmartBlockProps(99)
	if props[PropManaged] != "1" {
		t.Errorf("PropManaged: got %q", props[PropManaged])
	}
	if props[PropSmartBlockID] != "99" {
		t.Errorf("PropSmartBlockID: got %q", props[PropSmartBlockID])
	}
	if _, ok := props[PropRuleID]; ok {
		t.Error("smart block props should not carry PropRuleID")
	}
}
