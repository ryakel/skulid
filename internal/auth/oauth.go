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
	stateCookie     = "skulid_oauth_state"
	intentCookie    = "skulid_oauth_intent"
	expectSubCookie = "skulid_oauth_expect_sub"
	IntentLogin     = "login"   // owner login (TOFU)
	IntentConnect   = "connect" // additional account connection
)

// FlowOptions describes a consent round-trip about to start.
type FlowOptions struct {
	Intent string
	// LoginHint pre-selects an account on Google's chooser. It is only a
	// hint -- the user can still pick a different one, which is why
	// ExpectSub exists.
	LoginHint string
	// ExpectSub, when set, is the Google subject ID the returning account
	// must match. Set it for a reconnect, where the point is to repair one
	// specific account; leave it empty when adding any new account.
	ExpectSub string
}

// FlowState is what a completed consent round-trip carries back.
type FlowState struct {
	Intent    string
	ExpectSub string
}

// SubMismatch reports whether a returning account is the wrong one.
//
// An empty expect means the flow placed no constraint -- "+ Connect Google
// account" must still be able to add any account -- so only a set-and-
// different subject is a mismatch.
func SubMismatch(expect, got string) bool {
	return expect != "" && expect != got
}

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

// StartFlow begins a consent round-trip, recording what the callback will
// need to validate it.
func (p *OAuthProvider) StartFlow(w http.ResponseWriter, secure bool, opt FlowOptions) string {
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
		Value:    opt.Intent,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   600,
	})
	// Only set for a reconnect. Cleared with the others in VerifyState, and
	// scoped exactly like them so it cannot outlive the flow.
	http.SetCookie(w, &http.Cookie{
		Name:     expectSubCookie,
		Value:    opt.ExpectSub,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   600,
	})

	// Always request refresh tokens, force consent so we get one even on re-auth.
	opts := []oauth2.AuthCodeOption{
		oauth2.AccessTypeOffline,
		oauth2.SetAuthURLParam("prompt", "consent"),
	}
	if opt.LoginHint != "" {
		opts = append(opts, oauth2.SetAuthURLParam("login_hint", opt.LoginHint))
	}
	return p.cfg.AuthCodeURL(state, opts...)
}

// VerifyState validates the OAuth state cookie and returns what the flow
// recorded when it started. It clears every flow cookie.
func (p *OAuthProvider) VerifyState(w http.ResponseWriter, r *http.Request) (FlowState, error) {
	stateCk, err := r.Cookie(stateCookie)
	if err != nil {
		return FlowState{}, fmt.Errorf("state cookie missing: %w", err)
	}
	got := r.URL.Query().Get("state")
	if got == "" || got != stateCk.Value {
		return FlowState{}, fmt.Errorf("state mismatch")
	}

	out := FlowState{Intent: IntentLogin}
	if ck, _ := r.Cookie(intentCookie); ck != nil && ck.Value != "" {
		out.Intent = ck.Value
	}
	if ck, _ := r.Cookie(expectSubCookie); ck != nil {
		out.ExpectSub = ck.Value
	}

	for _, name := range []string{stateCookie, intentCookie, expectSubCookie} {
		http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1})
	}
	return out, nil
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
