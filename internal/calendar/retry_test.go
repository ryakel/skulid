package calendar

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestBackoffDelayIsExponentialAndCapped(t *testing.T) {
	if got := backoffDelay(0); got != baseRetryDelay {
		t.Errorf("attempt 0 = %v, want %v", got, baseRetryDelay)
	}
	if got := backoffDelay(1); got != 2*baseRetryDelay {
		t.Errorf("attempt 1 = %v, want %v", got, 2*baseRetryDelay)
	}
	if got := backoffDelay(50); got != maxRetryDelay {
		t.Errorf("a huge attempt must clamp to %v, got %v", maxRetryDelay, got)
	}
	if got := backoffDelay(-1); got != baseRetryDelay {
		t.Errorf("negative attempt = %v, want %v", got, baseRetryDelay)
	}
}

// Jitter exists so several calendars throttled at once don't march back in
// lockstep, but it must never collapse to zero or exceed the base delay.
func TestWithJitterStaysInRange(t *testing.T) {
	d := 4 * time.Second
	for _, frac := range []float64{0, 0.25, 0.5, 0.999} {
		got := withJitter(d, frac)
		if got < d/2 || got > d {
			t.Errorf("withJitter(%v, %v) = %v, want within [%v, %v]", d, frac, got, d/2, d)
		}
	}
	if got := withJitter(0, 0.5); got != 0 {
		t.Errorf("zero delay should stay zero, got %v", got)
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		in      string
		want    time.Duration
		wantOK  bool
		comment string
	}{
		{"30", 30 * time.Second, true, "delay-seconds"},
		{"0", 0, true, "zero seconds"},
		{"  15 ", 15 * time.Second, true, "surrounding space"},
		{"-5", 0, true, "negative seconds clamp to zero, not a negative sleep"},
		{"", 0, false, "absent header"},
		{"soon", 0, false, "unparseable"},
		{"Mon, 27 Apr 2026 12:00:30 GMT", 30 * time.Second, true, "http-date in the future"},
		{"Mon, 27 Apr 2026 11:59:00 GMT", 0, true, "http-date in the past clamps to zero"},
	}
	for _, tc := range cases {
		t.Run(tc.comment, func(t *testing.T) {
			got, ok := parseRetryAfter(tc.in, now)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && got != tc.want {
				t.Errorf("delay = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestIsRateLimited(t *testing.T) {
	if !isRateLimited(http.StatusTooManyRequests, nil) {
		t.Error("429 is rate limiting")
	}
	if !isRateLimited(http.StatusForbidden, []byte(`{"error":{"errors":[{"reason":"rateLimitExceeded"}]}}`)) {
		t.Error("403 rateLimitExceeded is rate limiting")
	}
	if !isRateLimited(http.StatusForbidden, []byte(`{"reason":"userRateLimitExceeded"}`)) {
		t.Error("403 userRateLimitExceeded is rate limiting")
	}
	// A plain 403 is a permissions problem. Retrying it is pointless and
	// would turn an instant, legible failure into a slow one.
	if isRateLimited(http.StatusForbidden, []byte(`{"reason":"forbidden"}`)) {
		t.Error("a plain 403 must not be treated as rate limiting")
	}
	if isRateLimited(http.StatusNotFound, nil) {
		t.Error("404 is not rate limiting")
	}
}

// The idempotency rule is the safety-critical part: replaying a failed insert
// could duplicate an event on someone's real calendar.
func TestIsRetryableServerError(t *testing.T) {
	for _, m := range []string{http.MethodGet, http.MethodHead, http.MethodPut, http.MethodDelete} {
		if !isRetryableServerError(m, http.StatusBadGateway) {
			t.Errorf("%s is idempotent; 502 should be retryable", m)
		}
	}
	if isRetryableServerError(http.MethodPost, http.StatusBadGateway) {
		t.Error("POST must never be replayed on 5xx — it could duplicate an event")
	}
	if isRetryableServerError(http.MethodGet, http.StatusBadRequest) {
		t.Error("400 is not a server error")
	}
}

func TestReplayable(t *testing.T) {
	noBody, _ := http.NewRequest(http.MethodGet, "http://x/", nil)
	if !replayable(noBody) {
		t.Error("a bodyless request is replayable")
	}

	withGetBody, _ := http.NewRequest(http.MethodPost, "http://x/", strings.NewReader("{}"))
	if !replayable(withGetBody) {
		t.Error("NewRequest sets GetBody for a strings.Reader, so this is replayable")
	}

	// A body with no GetBody has already been drained by the first attempt.
	opaque, _ := http.NewRequest(http.MethodPost, "http://x/", io.NopCloser(strings.NewReader("{}")))
	opaque.GetBody = nil
	if replayable(opaque) {
		t.Error("a body without GetBody must not be replayed — attempt two would send nothing")
	}
}

// testTransport drives the retry loop without real sleeping.
func newTestTransport(base http.RoundTripper) *retryTransport {
	return &retryTransport{
		base:  base,
		sleep: func(time.Duration) {},
		rand:  func() float64 { return 0.5 },
	}
}

func TestRoundTripRetriesRateLimitThenSucceeds(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&calls, 1) < 3 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	resp, err := newTestTransport(http.DefaultTransport).RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("server saw %d calls, want 3", got)
	}
}

func TestRoundTripDoesNotRetryPostOnServerError(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL, strings.NewReader(`{"summary":"meeting"}`))
	resp, err := newTestTransport(http.DefaultTransport).RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("POST hit the server %d times; a 5xx must not be replayed", got)
	}
}

// A rate-limited POST is safe to replay — 429 means it was never processed —
// and the body must survive the replay intact.
func TestRoundTripReplaysPostBodyOnRateLimit(t *testing.T) {
	var calls int32
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		if atomic.AddInt32(&calls, 1) < 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL, strings.NewReader(`{"summary":"meeting"}`))
	resp, err := newTestTransport(http.DefaultTransport).RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if len(bodies) != 2 {
		t.Fatalf("want 2 attempts, got %d", len(bodies))
	}
	if bodies[0] != bodies[1] {
		t.Errorf("replayed body differs: %q then %q", bodies[0], bodies[1])
	}
}

func TestRoundTripGivesUpAfterMaxAttempts(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	resp, err := newTestTransport(http.DefaultTransport).RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if got := atomic.LoadInt32(&calls); got != maxRetryAttempts {
		t.Errorf("server saw %d calls, want %d — retries must be bounded", got, maxRetryAttempts)
	}
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("the final response should surface, got %d", resp.StatusCode)
	}
}

// A caller receiving a non-retried 403 must still be able to read its body:
// the transport peeks at it to classify, and has to put it back.
func TestRoundTripRestoresBodyOfNonRetried403(t *testing.T) {
	const payload = `{"error":{"errors":[{"reason":"forbidden"}]}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(payload))
	}))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	resp, err := newTestTransport(http.DefaultTransport).RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	got, _ := io.ReadAll(resp.Body)
	if string(got) != payload {
		t.Errorf("body came back as %q, want %q", got, payload)
	}
}

func TestRoundTripHonoursRetryAfter(t *testing.T) {
	var slept []time.Duration
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&calls, 1) < 2 {
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	rt := newTestTransport(http.DefaultTransport)
	rt.sleep = func(d time.Duration) { slept = append(slept, d) }

	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if len(slept) != 1 || slept[0] != 2*time.Second {
		t.Errorf("slept %v, want exactly [2s] from the Retry-After header", slept)
	}
}
