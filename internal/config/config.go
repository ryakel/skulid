package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
)

type Config struct {
	GoogleClientID     string
	GoogleClientSecret string
	ExternalURL        string
	SessionSecret      []byte
	EncryptionKey      []byte
	DatabaseURL        string
	ListenAddr         string
	AnthropicAPIKey    string
	AnthropicModel     string
	// DevAuthBypass enables the GET /dev/login route which claims TOFU as
	// DevUserEmail and issues a session — lets you click around the UI
	// without doing a real Google OAuth round-trip. Loud startup warning
	// and a banner on every page when on. Never set in production.
	DevAuthBypass bool
	DevUserEmail  string
}

func Load() (*Config, error) {
	c := &Config{
		GoogleClientID:     os.Getenv("GOOGLE_CLIENT_ID"),
		GoogleClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),
		ExternalURL:        strings.TrimRight(os.Getenv("EXTERNAL_URL"), "/"),
		DatabaseURL:        os.Getenv("DATABASE_URL"),
		ListenAddr:         envOr("LISTEN_ADDR", ":8567"),
		AnthropicAPIKey:    os.Getenv("ANTHROPIC_API_KEY"),
		AnthropicModel:     envOr("ANTHROPIC_MODEL", "claude-opus-4-7"),
		DevAuthBypass:      isTruthy(os.Getenv("SKULID_DEV_AUTH_BYPASS")),
		DevUserEmail:       envOr("SKULID_DEV_USER_EMAIL", "dev@local"),
	}

	sessionSecret := os.Getenv("SESSION_SECRET")
	if sessionSecret == "" {
		return nil, errors.New("SESSION_SECRET is required")
	}
	c.SessionSecret = []byte(sessionSecret)

	encKeyB64 := os.Getenv("ENCRYPTION_KEY")
	if encKeyB64 == "" {
		return nil, errors.New("ENCRYPTION_KEY is required (base64-encoded 32 bytes)")
	}
	key, err := base64.StdEncoding.DecodeString(encKeyB64)
	if err != nil {
		return nil, fmt.Errorf("decoding ENCRYPTION_KEY: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("ENCRYPTION_KEY must decode to 32 bytes, got %d", len(key))
	}
	c.EncryptionKey = key

	missing := []string{}
	if c.GoogleClientID == "" {
		missing = append(missing, "GOOGLE_CLIENT_ID")
	}
	if c.GoogleClientSecret == "" {
		missing = append(missing, "GOOGLE_CLIENT_SECRET")
	}
	if c.ExternalURL == "" {
		missing = append(missing, "EXTERNAL_URL")
	}
	if c.DatabaseURL == "" {
		missing = append(missing, "DATABASE_URL")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required env: %s", strings.Join(missing, ", "))
	}

	return c, nil
}

func (c *Config) RedirectURL() string {
	return c.ExternalURL + "/auth/google/callback"
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// isTruthy treats the standard "yes-ish" strings as true so the dev flag
// works whether the operator types 1, true, yes, on, etc.
func isTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on", "y":
		return true
	}
	return false
}
