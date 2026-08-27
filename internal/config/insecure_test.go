package config

import (
	"strings"
	"testing"
)

// safeConfig is a configuration with nothing wrong with it. Each test mutates
// exactly one field, so a finding can only come from that field.
func safeConfig() *Config {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1) // anything but all-zero
	}
	return &Config{
		GoogleClientID:     "1234567890-abcdef.apps.googleusercontent.com",
		GoogleClientSecret: "GOCSPX-realish-secret-value",
		ExternalURL:        "https://skulid.example.com",
		SessionSecret:      []byte(strings.Repeat("s", 48)),
		EncryptionKey:      key,
		DatabaseURL:        "postgres://skulid:pw@db:5432/skulid",
	}
}

func TestDetectInsecureAcceptsAGoodConfig(t *testing.T) {
	if f := detectInsecure(safeConfig()); len(f) != 0 {
		t.Fatalf("a valid config produced findings: %v", f)
	}
}

func TestDetectInsecureCatchesEachPlaceholder(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Config)
		wantVar string
	}{
		{
			"compose placeholder session secret",
			func(c *Config) { c.SessionSecret = []byte(placeholderSessionSecret) },
			"SESSION_SECRET",
		},
		{
			"short session secret",
			func(c *Config) { c.SessionSecret = []byte("tooshort") },
			"SESSION_SECRET",
		},
		{
			"all-zero encryption key",
			func(c *Config) { c.EncryptionKey = make([]byte, 32) },
			"ENCRYPTION_KEY",
		},
		{
			"placeholder client id",
			func(c *Config) { c.GoogleClientID = "dev" },
			"GOOGLE_CLIENT_ID",
		},
		{
			"placeholder client secret",
			func(c *Config) { c.GoogleClientSecret = "dev" },
			"GOOGLE_CLIENT_SECRET",
		},
		{
			"plain http external url",
			func(c *Config) { c.ExternalURL = "http://localhost:8567" },
			"EXTERNAL_URL",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := safeConfig()
			tc.mutate(c)
			findings := detectInsecure(c)
			if len(findings) == 0 {
				t.Fatalf("no findings for %s", tc.name)
			}
			var found bool
			for _, f := range findings {
				if f.Var == tc.wantVar {
					found = true
					if f.Reason == "" {
						t.Error("finding carries no reason for the operator")
					}
				}
			}
			if !found {
				t.Errorf("want a finding for %s, got %v", tc.wantVar, findings)
			}
		})
	}
}

// The exact default docker-compose.yml substitutes when no .env is present.
// This is the case that must never boot silently.
func TestDetectInsecureCatchesTheFullComposeDefault(t *testing.T) {
	c := &Config{
		GoogleClientID:     "dev",
		GoogleClientSecret: "dev",
		ExternalURL:        "http://localhost:8567",
		SessionSecret:      []byte(placeholderSessionSecret),
		EncryptionKey:      make([]byte, 32),
		DatabaseURL:        "postgres://skulid:changeme@db:5432/skulid",
	}

	findings := detectInsecure(c)
	if len(findings) < 5 {
		t.Fatalf("want a finding for every placeholder, got %d: %v", len(findings), findings)
	}

	for _, want := range []string{"SESSION_SECRET", "ENCRYPTION_KEY", "GOOGLE_CLIENT_ID", "GOOGLE_CLIENT_SECRET", "EXTERNAL_URL"} {
		var found bool
		for _, f := range findings {
			if f.Var == want {
				found = true
			}
		}
		if !found {
			t.Errorf("missing finding for %s", want)
		}
	}
}

// A 32-byte secret is the documented minimum and must be accepted; 31 must not.
func TestSessionSecretLengthBoundary(t *testing.T) {
	c := safeConfig()
	c.SessionSecret = []byte(strings.Repeat("s", minSessionSecretLen))
	if f := detectInsecure(c); len(f) != 0 {
		t.Errorf("a %d-byte secret should be accepted, got %v", minSessionSecretLen, f)
	}

	c.SessionSecret = []byte(strings.Repeat("s", minSessionSecretLen-1))
	if f := detectInsecure(c); len(f) == 0 {
		t.Errorf("a %d-byte secret should be rejected", minSessionSecretLen-1)
	}
}

// The failure message has to tell the operator what to actually do.
func TestInsecureErrorNamesTheCauseAndTheEscapeHatch(t *testing.T) {
	msg := insecureError([]InsecureFinding{
		{Var: "ENCRYPTION_KEY", Reason: "decodes to all zero bytes"},
	}).Error()

	for _, want := range []string{"ENCRYPTION_KEY", "decodes to all zero bytes", ".env", EnvAllowInsecure} {
		if !strings.Contains(msg, want) {
			t.Errorf("startup error missing %q:\n%s", want, msg)
		}
	}
}
