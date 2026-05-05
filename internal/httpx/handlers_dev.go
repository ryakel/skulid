package httpx

import (
	"net/http"

	"github.com/ryakel/skulid/internal/auth"
)

// devSub is the synthetic google_sub used by the dev auth bypass. Stable so a
// single owner row in the setting table works across restarts.
const devSub = "skulid-dev-bypass"

// handleDevLogin is registered only when SKULID_DEV_AUTH_BYPASS is true.
// It claims TOFU as DevUserEmail (idempotent — re-using a previous claim is
// fine) and issues a real session cookie, after which every owner-protected
// route works exactly like prod.
func (s *Server) handleDevLogin(w http.ResponseWriter, r *http.Request) {
	if !s.Cfg.DevAuthBypass {
		http.NotFound(w, r)
		return
	}
	email := s.Cfg.DevUserEmail
	if email == "" {
		email = "dev@local"
	}
	if err := s.TOFU.Claim(r.Context(), devSub, email); err != nil {
		s.Log.Error("dev login: tofu claim failed", "err", err)
		http.Error(w, "tofu claim failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.Sessions.Issue(w, auth.Session{GoogleSub: devSub, Email: email})
	s.Log.Warn("dev auth bypass — session issued", "email", email, "ip", r.RemoteAddr)
	http.Redirect(w, r, "/", http.StatusFound)
}
