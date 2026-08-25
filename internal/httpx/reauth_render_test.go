package httpx

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/ryakel/skulid/internal/db"
)

// The reauth banner and the Accounts rows are the only place the owner learns
// that sync has stopped. A typo in either template turns into a 500 on every
// page, so render them for real rather than trusting a compile.
func TestRenderAccountNeedingReauth(t *testing.T) {
	r, err := NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}

	detected := time.Date(2026, 8, 18, 9, 30, 0, 0, time.UTC)
	broken := db.Account{
		ID:               7,
		Email:            "ryan@example.com",
		CreatedAt:        time.Date(2026, 8, 11, 9, 0, 0, 0, time.UTC),
		NeedsReauth:      true,
		ReauthReason:     "Google revoked this account's access.",
		ReauthDetectedAt: &detected,
	}

	data := map[string]any{
		"Title":              "Accounts",
		"Features":           map[string]bool{"Assistant": false},
		"Version":            "test",
		"Accounts":           []db.Account{broken},
		"CalendarsByAccount": map[int64][]db.Calendar{},
		"ReauthAccounts":     []db.Account{broken},
	}

	var buf bytes.Buffer
	if err := r.Render(&buf, "accounts", data); err != nil {
		t.Fatalf("Render: %v", err)
	}

	out := buf.String()
	for _, want := range []string{
		"Sync is stopped.",
		"ryan@example.com",
		"needs reconnect",
		"/accounts/7/reconnect",
		"Google revoked this account&#39;s access.",
		"2026-08-18",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered page missing %q", want)
		}
	}
}

// A healthy account must not show any of the alarm furniture.
func TestRenderHealthyAccountHasNoReauthNoise(t *testing.T) {
	r, err := NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}

	healthy := db.Account{ID: 3, Email: "ok@example.com", CreatedAt: time.Now()}
	data := map[string]any{
		"Title":              "Accounts",
		"Features":           map[string]bool{"Assistant": false},
		"Version":            "test",
		"Accounts":           []db.Account{healthy},
		"CalendarsByAccount": map[int64][]db.Calendar{},
	}

	var buf bytes.Buffer
	if err := r.Render(&buf, "accounts", data); err != nil {
		t.Fatalf("Render: %v", err)
	}

	out := buf.String()
	for _, unwanted := range []string{"Sync is stopped.", "needs reconnect", "/accounts/3/reconnect"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("healthy account page should not contain %q", unwanted)
		}
	}
}
