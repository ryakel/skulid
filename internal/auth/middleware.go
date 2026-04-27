package auth

import (
	"context"
	"net/http"
)

type ctxKey string

const sessionCtxKey ctxKey = "session"

// RequireOwner returns a middleware that ensures the request carries a session
// matching the recorded owner. Unauthenticated requests redirect to /login;
// mismatched owners get a 403.
func RequireOwner(sessions *SessionManager, tofu *TOFU) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sess, err := sessions.Read(r)
			if err != nil {
				http.Redirect(w, r, "/login", http.StatusFound)
				return
			}
			ok, err := tofu.VerifyOwner(r.Context(), sess.GoogleSub)
			if err != nil {
				http.Error(w, "owner check failed", http.StatusInternalServerError)
				return
			}
			if !ok {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			ctx := context.WithValue(r.Context(), sessionCtxKey, sess)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func SessionFromContext(ctx context.Context) (*Session, bool) {
	s, ok := ctx.Value(sessionCtxKey).(*Session)
	return s, ok
}
