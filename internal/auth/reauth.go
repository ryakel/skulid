package auth

import (
	"errors"
	"net/http"

	"golang.org/x/oauth2"
)

// AuthFailure describes why an account can no longer mint access tokens.
// A zero value means the failure was transient and the account should be
// left alone.
type AuthFailure struct {
	Permanent bool
	// Reason is a short, user-facing explanation. Empty when not permanent.
	Reason string
}

// permanentGrantErrors are the OAuth 2.0 error codes Google returns when a
// refresh token will never work again. Retrying these is pointless; the only
// cure is sending the user back through the consent screen.
var permanentGrantErrors = map[string]string{
	"invalid_grant":       "Google revoked this account's access. This happens when consent is withdrawn, the password changed, the token sat unused for six months, or the OAuth app is still in Testing publishing status (where Google expires every refresh token after 7 days).",
	"invalid_client":      "Google rejected the OAuth client credentials. Check GOOGLE_CLIENT_ID and GOOGLE_CLIENT_SECRET.",
	"unauthorized_client": "This OAuth client is not authorized to refresh tokens for this account.",
	"access_denied":       "Access was denied for this account.",
}

// ClassifyRefreshError decides whether a token-refresh failure is permanent
// (the account needs a fresh consent round-trip) or transient (network blip,
// Google 5xx, rate limit) and should simply be retried on the next tick.
//
// It is pure: no I/O, no clock, no context. That is what makes it testable.
func ClassifyRefreshError(err error) AuthFailure {
	if err == nil {
		return AuthFailure{}
	}

	var re *oauth2.RetrieveError
	if !errors.As(err, &re) {
		// Not an OAuth protocol error at all -- DNS, TLS, timeout. Transient.
		return AuthFailure{}
	}

	if reason, ok := permanentGrantErrors[re.ErrorCode]; ok {
		return AuthFailure{Permanent: true, Reason: reason}
	}

	// Google does not always populate ErrorCode. Fall back to the status:
	// 400/401 on the token endpoint means the grant itself was rejected,
	// while 429 and 5xx are load-shedding and worth retrying.
	if re.Response != nil {
		switch code := re.Response.StatusCode; {
		case code == http.StatusBadRequest, code == http.StatusUnauthorized:
			return AuthFailure{
				Permanent: true,
				Reason:    "Google rejected this account's refresh token. Reconnect the account to grant consent again.",
			}
		}
	}

	return AuthFailure{}
}
