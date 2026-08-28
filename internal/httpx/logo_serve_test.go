package httpx

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The logo is referenced by layout.html's favicon link and by app.css, both of
// which point at /static/logo.svg. Prove the embedded FS actually serves it:
// a missing file is a 404 in the browser, not a build error.
func TestLogoIsServedFromEmbeddedStatic(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/logo.svg", nil)
	staticHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /static/logo.svg = %d, want 200", rec.Code)
	}
	body, _ := io.ReadAll(rec.Body)
	if !strings.Contains(string(body), "<svg") {
		t.Errorf("served body is not an SVG: %.80q", body)
	}
	if !strings.Contains(string(body), "#3b82f6") {
		t.Errorf("accent module missing from served logo")
	}
}
