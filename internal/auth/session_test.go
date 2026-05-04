package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newSM(t *testing.T) *SessionManager {
	t.Helper()
	return NewSessionManager([]byte("test-secret-please-make-it-long-enough"), false)
}

// requestWithCookies copies every Set-Cookie header from a recorder onto a fresh
// request so SessionManager.Read can find it.
func requestWithCookies(t *testing.T, rec *httptest.ResponseRecorder) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	for _, c := range rec.Result().Cookies() {
		r.AddCookie(c)
	}
	return r
}

func TestSessionRoundTrip(t *testing.T) {
	sm := newSM(t)
	rec := httptest.NewRecorder()
	sm.Issue(rec, Session{GoogleSub: "abc", Email: "owner@example.com"})

	got, err := sm.Read(requestWithCookies(t, rec))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.GoogleSub != "abc" || got.Email != "owner@example.com" {
		t.Fatalf("unexpected payload: %+v", got)
	}
	if got.IssuedAt.IsZero() {
		t.Fatal("expected IssuedAt to be set by Issue")
	}
}

func TestSessionRejectsTamperedSignature(t *testing.T) {
	sm := newSM(t)
	rec := httptest.NewRecorder()
	sm.Issue(rec, Session{GoogleSub: "abc", Email: "owner@example.com"})

	cookies := rec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("expected Set-Cookie")
	}
	parts := strings.SplitN(cookies[0].Value, ".", 2)
	if len(parts) != 2 {
		t.Fatal("expected payload.sig cookie format")
	}
	// Replace the signature with garbage of the same length.
	tampered := parts[0] + "." + strings.Repeat("A", len(parts[1]))

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: cookies[0].Name, Value: tampered})
	if _, err := sm.Read(r); err == nil {
		t.Fatal("expected Read to fail for tampered signature")
	}
}

func TestSessionRejectsExpired(t *testing.T) {
	sm := newSM(t)
	rec := httptest.NewRecorder()
	// 31 days ago — older than sessionMaxAge (30d).
	sm.Issue(rec, Session{
		GoogleSub: "abc",
		Email:     "owner@example.com",
		IssuedAt:  time.Now().Add(-31 * 24 * time.Hour),
	})
	if _, err := sm.Read(requestWithCookies(t, rec)); err == nil {
		t.Fatal("expected Read to reject expired session")
	}
}

func TestSessionMissingCookie(t *testing.T) {
	sm := newSM(t)
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	if _, err := sm.Read(r); err == nil {
		t.Fatal("expected Read with no cookie to fail")
	}
}

func TestSessionMalformedCookie(t *testing.T) {
	sm := newSM(t)
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: "no-dot"})
	if _, err := sm.Read(r); err == nil {
		t.Fatal("expected Read to fail on malformed cookie")
	}
}

func TestSessionClearWipesCookie(t *testing.T) {
	sm := newSM(t)
	rec := httptest.NewRecorder()
	sm.Clear(rec)
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected 1 Set-Cookie, got %d", len(cookies))
	}
	if cookies[0].MaxAge != -1 {
		t.Fatalf("expected MaxAge=-1 on Clear, got %d", cookies[0].MaxAge)
	}
}
