package config

import (
	"bytes"
	"fmt"
	"strings"
)

// These are the placeholder values docker-compose.yml substitutes when the
// corresponding variable is unset. They exist so the stack boots for a smoke
// test without a .env, which means a missing or misnamed .env produces a
// running instance rather than an error. Recognising them by content is the
// only way to tell those two situations apart.
const (
	placeholderSessionSecret = "dev-only-do-not-use-in-production-please-rotate"
	placeholderClientValue   = "dev"
)

// minSessionSecretLen matches what the docs tell you to generate. Below this
// the HMAC signing key is weaker than the cookie it protects.
const minSessionSecretLen = 32

// InsecureFinding is one reason this configuration must not face the internet.
type InsecureFinding struct {
	Var    string
	Reason string
}

func (f InsecureFinding) String() string { return f.Var + ": " + f.Reason }

// detectInsecure reports every way a loaded config is unsafe to run in
// production. Pure: it reads no environment and touches no I/O, so the
// judgement is exhaustively testable.
//
// The check is by *content*, not by whether a variable was set, because
// compose always sets them. "Present" has never been the same as "safe".
func detectInsecure(c *Config) []InsecureFinding {
	var out []InsecureFinding

	secret := string(c.SessionSecret)
	switch {
	case secret == placeholderSessionSecret:
		out = append(out, InsecureFinding{
			Var:    "SESSION_SECRET",
			Reason: "is the placeholder published in docker-compose.yml — anyone can forge an owner session cookie",
		})
	case len(secret) < minSessionSecretLen:
		out = append(out, InsecureFinding{
			Var:    "SESSION_SECRET",
			Reason: fmt.Sprintf("is %d bytes; %d or more is required to sign session cookies safely", len(secret), minSessionSecretLen),
		})
	}

	if len(c.EncryptionKey) > 0 && bytes.Equal(c.EncryptionKey, make([]byte, len(c.EncryptionKey))) {
		out = append(out, InsecureFinding{
			Var:    "ENCRYPTION_KEY",
			Reason: "decodes to all zero bytes — the placeholder from docker-compose.yml. Every stored Google refresh token is sealed with a key published in this repository",
		})
	}

	if c.GoogleClientID == placeholderClientValue {
		out = append(out, InsecureFinding{Var: "GOOGLE_CLIENT_ID", Reason: "is the placeholder \"dev\"; no real OAuth client is configured"})
	}
	if c.GoogleClientSecret == placeholderClientValue {
		out = append(out, InsecureFinding{Var: "GOOGLE_CLIENT_SECRET", Reason: "is the placeholder \"dev\"; no real OAuth client is configured"})
	}

	// EXTERNAL_URL drives both the OAuth redirect Google is given and the
	// Secure flag on the session cookie, so plain http quietly downgrades
	// the cookie on a host that has to be publicly reachable.
	if c.ExternalURL != "" && !strings.HasPrefix(c.ExternalURL, "https://") {
		out = append(out, InsecureFinding{
			Var:    "EXTERNAL_URL",
			Reason: "is not https:// — session cookies lose their Secure flag and Google will reject the OAuth redirect",
		})
	}

	return out
}

// insecureError renders findings into the message the daemon dies with.
func insecureError(findings []InsecureFinding) error {
	var b strings.Builder
	b.WriteString("refusing to start: this configuration is not safe to run.\n")
	for _, f := range findings {
		b.WriteString("  - " + f.String() + "\n")
	}
	b.WriteString("\nThis usually means .env is missing, misnamed, or not being read, so docker-compose\n")
	b.WriteString("substituted its placeholder defaults. Fix the values, or set\n")
	b.WriteString(EnvAllowInsecure + "=1 to run anyway (local smoke testing only).")
	return fmt.Errorf("%s", b.String())
}
