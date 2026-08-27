package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsHealthcheckInvocation(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want bool
	}{
		{"no args — run as a daemon", nil, false},
		{"single dash", []string{"-healthcheck"}, true},
		{"double dash", []string{"--healthcheck"}, true},
		{"among other args", []string{"-v", "--healthcheck"}, true},
		{"unrelated flag", []string{"-version"}, false},
		{"substring must not match", []string{"-healthchecker"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isHealthcheckInvocation(tc.args); got != tc.want {
				t.Errorf("isHealthcheckInvocation(%v) = %v, want %v", tc.args, got, tc.want)
			}
		})
	}
}

// The probe runs inside the container, so every form of listen address has to
// resolve to loopback — binding 0.0.0.0 must not make it try to dial 0.0.0.0.
func TestHealthProbeURL(t *testing.T) {
	cases := map[string]string{
		":8567":          "http://127.0.0.1:8567/healthz",
		"0.0.0.0:8567":   "http://127.0.0.1:8567/healthz",
		"127.0.0.1:9000": "http://127.0.0.1:9000/healthz",
		"[::]:8080":      "http://127.0.0.1:8080/healthz",
		"":               "http://127.0.0.1:8567/healthz",
		"nonsense":       "http://127.0.0.1:8567/healthz",
		"host-no-port:":  "http://127.0.0.1:8567/healthz",
	}
	for addr, want := range cases {
		if got := healthProbeURL(addr); got != want {
			t.Errorf("healthProbeURL(%q) = %q, want %q", addr, got, want)
		}
	}
}

// runHealthcheck's contract is "non-nil means unhealthy". Exercise the status
// handling against a real server so the exit code the container sees is the
// one the endpoint actually justifies.
func TestHealthcheckStatusHandling(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		wantErr bool
	}{
		{"serving", http.StatusOK, false},
		{"still starting", http.StatusServiceUnavailable, true},
		{"wedged behind an error", http.StatusInternalServerError, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/healthz" {
					t.Errorf("probed %q, want /healthz", r.URL.Path)
				}
				w.WriteHeader(tc.status)
			}))
			defer srv.Close()

			t.Setenv("LISTEN_ADDR", srv.Listener.Addr().String())
			err := runHealthcheck()
			if tc.wantErr && err == nil {
				t.Errorf("status %d should report unhealthy", tc.status)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("status %d should report healthy, got %v", tc.status, err)
			}
		})
	}
}

// Nothing listening is the case the healthcheck exists for.
func TestHealthcheckFailsWhenNothingIsListening(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	addr := srv.Listener.Addr().String()
	srv.Close() // free the port so the probe has nothing to reach

	t.Setenv("LISTEN_ADDR", addr)
	if err := runHealthcheck(); err == nil {
		t.Error("want an error when the daemon is not listening")
	}
}
