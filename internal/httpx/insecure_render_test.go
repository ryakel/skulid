package httpx

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/ryakel/skulid/internal/config"
	"github.com/ryakel/skulid/internal/db"
)

// This banner is the only in-app signal that stored tokens cannot be trusted.
// It renders on the login page too, because that is the first thing anyone
// sees and they may never reach an authenticated page.
func TestRenderInsecureConfigBanner(t *testing.T) {
	r, err := NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}

	findings := []config.InsecureFinding{
		{Var: "ENCRYPTION_KEY", Reason: "decodes to all zero bytes"},
		{Var: "SESSION_SECRET", Reason: "is the placeholder published in docker-compose.yml"},
	}

	pages := map[string]map[string]any{
		"accounts": {
			"Title":              "Accounts",
			"Features":           map[string]bool{"Assistant": false},
			"Version":            "test",
			"Accounts":           []db.Account{{ID: 1, Email: "me@example.com", CreatedAt: time.Now()}},
			"CalendarsByAccount": map[int64][]db.Calendar{},
			"InsecureFindings":   findings,
		},
		"login": {
			"Claimed":          false,
			"Version":          "test",
			"InsecureFindings": findings,
		},
	}

	for name, data := range pages {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := r.Render(&buf, name, data); err != nil {
				t.Fatalf("Render: %v", err)
			}
			out := buf.String()
			for _, want := range []string{
				"INSECURE CONFIGURATION",
				"ENCRYPTION_KEY",
				"decodes to all zero bytes",
				"SESSION_SECRET",
			} {
				if !strings.Contains(out, want) {
					t.Errorf("page missing %q", want)
				}
			}
		})
	}
}

// A correctly configured instance must show none of it.
func TestRenderNoInsecureBannerWhenConfigIsSound(t *testing.T) {
	r, err := NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}

	pages := map[string]map[string]any{
		"accounts": {
			"Title":              "Accounts",
			"Features":           map[string]bool{"Assistant": false},
			"Version":            "test",
			"Accounts":           []db.Account{},
			"CalendarsByAccount": map[int64][]db.Calendar{},
		},
		"login": {"Claimed": false, "Version": "test"},
	}

	for name, data := range pages {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := r.Render(&buf, name, data); err != nil {
				t.Fatalf("Render: %v", err)
			}
			if strings.Contains(buf.String(), "INSECURE CONFIGURATION") {
				t.Error("healthy config should not render the insecure banner")
			}
		})
	}
}
