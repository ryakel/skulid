package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	sessionCookie = "skulid_session"
	sessionMaxAge = 30 * 24 * time.Hour
)

type SessionManager struct {
	secret []byte
	secure bool
}

func NewSessionManager(secret []byte, secure bool) *SessionManager {
	return &SessionManager{secret: secret, secure: secure}
}

type Session struct {
	GoogleSub string
	Email     string
	IssuedAt  time.Time
}

func (s *SessionManager) Issue(w http.ResponseWriter, sess Session) {
	if sess.IssuedAt.IsZero() {
		sess.IssuedAt = time.Now()
	}
	payload := strings.Join([]string{
		sess.GoogleSub,
		sess.Email,
		strconv.FormatInt(sess.IssuedAt.Unix(), 10),
	}, "|")
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(payload))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	value := base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + sig
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode,
		Expires:  sess.IssuedAt.Add(sessionMaxAge),
	})
}

func (s *SessionManager) Clear(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

func (s *SessionManager) Read(r *http.Request) (*Session, error) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return nil, err
	}
	parts := strings.SplitN(c.Value, ".", 2)
	if len(parts) != 2 {
		return nil, errors.New("malformed session cookie")
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, err
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	mac := hmac.New(sha256.New, s.secret)
	mac.Write(payloadBytes)
	expected := mac.Sum(nil)
	if !hmac.Equal(sig, expected) {
		return nil, errors.New("session signature mismatch")
	}
	fields := strings.SplitN(string(payloadBytes), "|", 3)
	if len(fields) != 3 {
		return nil, errors.New("malformed session payload")
	}
	issued, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid issued at: %w", err)
	}
	issuedAt := time.Unix(issued, 0)
	if time.Since(issuedAt) > sessionMaxAge {
		return nil, errors.New("session expired")
	}
	return &Session{
		GoogleSub: fields[0],
		Email:     fields[1],
		IssuedAt:  issuedAt,
	}, nil
}
