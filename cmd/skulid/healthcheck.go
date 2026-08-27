package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"
)

// The runtime image is distroless: no shell, no curl, no wget. The only
// executable in it is this binary, so the container healthcheck invokes
// skulid itself with a flag and lets it probe its own HTTP server. That
// avoids shipping a second binary or fattening the base image just to be
// able to ask "are you serving?".
const healthcheckTimeout = 3 * time.Second

// isHealthcheckInvocation reports whether the process was started to probe a
// running daemon rather than to be one.
func isHealthcheckInvocation(args []string) bool {
	for _, a := range args {
		if a == "-healthcheck" || a == "--healthcheck" {
			return true
		}
	}
	return false
}

// healthProbeURL turns a listen address into the URL to probe. The probe runs
// inside the container, so it always dials loopback regardless of which
// interface the server bound to -- ":8567" and "0.0.0.0:8567" are both
// reachable at 127.0.0.1, and a bare or malformed value falls back to the
// same default the daemon uses.
func healthProbeURL(listenAddr string) string {
	port := "8567"
	if _, p, err := net.SplitHostPort(listenAddr); err == nil && p != "" {
		port = p
	}
	return "http://127.0.0.1:" + port + "/healthz"
}

// runHealthcheck probes the local daemon. A non-nil return means unhealthy.
//
// This is deliberately a liveness check, not a readiness one: /healthz reports
// that the HTTP server is accepting and serving requests, and nothing more.
// Wiring Postgres into it would mean a database blip restart-loops the app
// during an outage it did not cause, which makes the outage worse rather than
// shorter.
func runHealthcheck() error {
	ctx, cancel := context.WithTimeout(context.Background(), healthcheckTimeout)
	defer cancel()

	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = ":8567"
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthProbeURL(addr), nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("/healthz returned %d", resp.StatusCode)
	}
	return nil
}
