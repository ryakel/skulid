package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	calendarapi "google.golang.org/api/calendar/v3"
)

const (
	stateCookie    = "skulid_oauth_state"
	intentCookie   = "skulid_oauth_intent"
	IntentLogin    = "login"    // owner login (TOFU)
	IntentConnect  = "connect"  // additional account connection
)

type OAuthProvider struct {
	cfg *oauth2.Config
}

func NewOAuthProvider(clientID, clientSecret, redirectURL string) *OAuthProvider {
	return &OAuthProvider{
		cfg: &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  redirectURL,
			Endpoint:     google.Endpoint,
			Scopes: []string{
				"openid",
				"email",
				"profile",
				calendarapi.CalendarScope,
			},
		},
	}
}

func (p *OAuthProvider) Config() *oauth2.Config { return p.cfg }

func (p *OAuthProvider) StartFlow(w http.ResponseWriter, intent string, secure bool) string {
	state := randomState()
	http.SetCookie(w, &http.Cookie{
		Name:     stateCookie,
		Value:    state,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   600,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     intentCookie,
		Value:    intent,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   600,
	})
	// Always request refresh tokens, force consent so we get one even on re-auth.
	return p.cfg.AuthCodeURL(state,
		oauth2.AccessTypeOffline,
		oauth2.SetAuthURLParam("prompt", "consent"),
	)
}

// VerifyState reads the OAuth state cookie and intent. It clears both cookies.
func (p *OAuthProvider) VerifyState(w http.ResponseWriter, r *http.Request) (string, error) {
	stateCk, err := r.Cookie(stateCookie)
	if err != nil {
		return "", fmt.Errorf("state cookie missing: %w", err)
	}
	intentCk, _ := r.Cookie(intentCookie)
	got := r.URL.Query().Get("state")
	if got == "" || got != stateCk.Value {
		return "", fmt.Errorf("state mismatch")
	}
	intent := IntentLogin
	if intentCk != nil && intentCk.Value != "" {
		intent = intentCk.Value
	}
	// Clear cookies.
	http.SetCookie(w, &http.Cookie{Name: stateCookie, Value: "", Path: "/", MaxAge: -1})
	http.SetCookie(w, &http.Cookie{Name: intentCookie, Value: "", Path: "/", MaxAge: -1})
	return intent, nil
}

func (p *OAuthProvider) Exchange(ctx context.Context, code string) (*oauth2.Token, error) {
	return p.cfg.Exchange(ctx, code)
}

// UserInfo represents the fields we extract from the ID token / userinfo endpoint.
type UserInfo struct {
	Sub   string `json:"sub"`
	Email string `json:"email"`
	Name  string `json:"name"`
}

// FetchUserInfo calls the OpenID Connect userinfo endpoint with the access token.
func (p *OAuthProvider) FetchUserInfo(ctx context.Context, tok *oauth2.Token) (*UserInfo, error) {
	client := p.cfg.Client(ctx, tok)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://openidconnect.googleapis.com/v1/userinfo", nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("userinfo: status %d", resp.StatusCode)
	}
	var u UserInfo
	if err := json.NewDecoder(resp.Body).Decode(&u); err != nil {
		return nil, err
	}
	if u.Sub == "" || u.Email == "" {
		return nil, fmt.Errorf("userinfo missing sub or email")
	}
	return &u, nil
}

func randomState() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
