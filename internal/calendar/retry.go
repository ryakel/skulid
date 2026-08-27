package calendar

import (
	"bytes"
	"io"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Google's Calendar API is quota-limited and skulid's workload is bursty in
// exactly the way quotas punish: backfill walks history in one pass, the
// planner issues one Events.list per calendar per page load, and smart-block
// and decompression recompute fire on a 15s debounce -- all multiplied by
// connected account.
//
// Nothing retried any of it. The generated client routes every call through
// gensupport.SendRequest, which is the non-retrying path, so a 429 propagated
// straight up into a background goroutine whose only handling is a log line.
//
// This lives in the HTTP transport rather than in Client's methods because
// four call sites reach the raw service through Client.Service() -- the
// planner, decompression, and two AI tools. A wrapper-level retry would have
// missed every one of them, including the burstiest path there is.
const (
	maxRetryAttempts = 4
	baseRetryDelay   = 500 * time.Millisecond
	maxRetryDelay    = 8 * time.Second

	// Cap on how much of an error body is read to classify it. Error payloads
	// are small; this only exists so a malformed response cannot be used to
	// exhaust memory.
	maxErrorBodyBytes = 64 * 1024
)

// backoffDelay is the un-jittered delay before the given zero-based retry
// attempt: exponential from baseRetryDelay, capped at maxRetryDelay.
func backoffDelay(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	d := baseRetryDelay << attempt
	if d > maxRetryDelay || d <= 0 { // <= 0 catches shift overflow
		return maxRetryDelay
	}
	return d
}

// withJitter spreads retries so several calendars rate-limited at the same
// moment do not march back in lockstep. frac is in [0,1); the result stays
// within [d/2, d].
func withJitter(d time.Duration, frac float64) time.Duration {
	if d <= 0 {
		return 0
	}
	half := d / 2
	return half + time.Duration(float64(half)*frac)
}

// parseRetryAfter reads a Retry-After header in either supported form:
// delay-seconds, or an HTTP-date. A date in the past yields zero, not a
// negative delay.
func parseRetryAfter(v string, now time.Time) (time.Duration, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, false
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs < 0 {
			return 0, true
		}
		return time.Duration(secs) * time.Second, true
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := t.Sub(now); d > 0 {
			return d, true
		}
		return 0, true
	}
	return 0, false
}

// isRateLimited reports whether a response means "slow down". 429 is the
// modern signal; Calendar still emits 403 with a rateLimitExceeded reason in
// the body, which is why the body matters here and nowhere else.
func isRateLimited(status int, body []byte) bool {
	if status == http.StatusTooManyRequests {
		return true
	}
	if status != http.StatusForbidden {
		return false
	}
	s := string(body)
	return strings.Contains(s, "rateLimitExceeded") ||
		strings.Contains(s, "userRateLimitExceeded") ||
		strings.Contains(s, "quotaExceeded")
}

// isRetryableServerError decides whether a 5xx may be retried for this method.
//
// A 429 means the request was never processed, so replaying it is safe for
// anything. A 5xx is ambiguous -- the write may well have landed before the
// error -- so it is only replayed for methods that are idempotent by
// definition. Retrying Events.insert after a 502 would risk a duplicate event
// on someone's calendar, which is worse than the failed sync it is trying to
// avoid.
func isRetryableServerError(method string, status int) bool {
	switch status {
	case http.StatusInternalServerError, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout:
	default:
		return false
	}
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPut, http.MethodDelete:
		return true
	default:
		return false
	}
}

// replayable reports whether a request can be sent more than once. A body
// without GetBody has already been consumed by the first attempt, and
// replaying it would send an empty payload.
func replayable(req *http.Request) bool {
	return req.Body == nil || req.Body == http.NoBody || req.GetBody != nil
}

// retryTransport retries rate-limited and (where safe) failed requests.
type retryTransport struct {
	base  http.RoundTripper
	sleep func(time.Duration) // injectable for tests
	rand  func() float64      // injectable for tests
}

func newRetryTransport(base http.RoundTripper) *retryTransport {
	return &retryTransport{
		base:  base,
		sleep: time.Sleep,
		rand:  rand.Float64,
	}
}

func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !replayable(req) {
		return t.base.RoundTrip(req)
	}

	for attempt := 0; ; attempt++ {
		attemptReq := req.Clone(req.Context())
		if attempt > 0 && req.GetBody != nil {
			body, err := req.GetBody()
			if err != nil {
				return nil, err
			}
			attemptReq.Body = body
		}

		resp, err := t.base.RoundTrip(attemptReq)
		if err != nil {
			// Transport-level failures are deliberately not retried: a
			// connection that died mid-write may still have delivered the
			// request, and this layer cannot tell.
			return nil, err
		}

		if attempt >= maxRetryAttempts-1 {
			return resp, nil
		}

		delay, retry := t.retryDelay(attemptReq.Method, resp, attempt)
		if !retry {
			return resp, nil
		}

		// The response is being discarded, so drain enough to let the
		// connection be reused and then close it.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxErrorBodyBytes))
		_ = resp.Body.Close()

		select {
		case <-req.Context().Done():
			return nil, req.Context().Err()
		default:
		}
		t.sleep(delay)
	}
}

// retryDelay classifies a response and returns how long to wait before
// replaying it. It only buffers the body for a 403, where the reason string
// is the deciding factor -- successful responses are never read here.
func (t *retryTransport) retryDelay(method string, resp *http.Response, attempt int) (time.Duration, bool) {
	var body []byte
	if resp.StatusCode == http.StatusForbidden {
		body = peekBody(resp)
	}

	switch {
	case isRateLimited(resp.StatusCode, body):
	case isRetryableServerError(method, resp.StatusCode):
	default:
		return 0, false
	}

	if d, ok := parseRetryAfter(resp.Header.Get("Retry-After"), time.Now()); ok {
		if d > maxRetryDelay {
			d = maxRetryDelay
		}
		return d, true
	}
	return withJitter(backoffDelay(attempt), t.rand()), true
}

// peekBody reads a bounded prefix of the body and puts it back, so a caller
// that ends up receiving this response still sees an intact one.
func peekBody(resp *http.Response) []byte {
	if resp.Body == nil {
		return nil
	}
	buf, err := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
	if err != nil {
		return nil
	}
	_ = resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(buf))
	return buf
}
