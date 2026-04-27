// Package webhook handles Google push notifications. The handler verifies the
// per-channel token and enqueues an incremental sync for the matching calendar.
package webhook

import (
	"crypto/subtle"
	"log/slog"
	"net/http"

	"github.com/ryakel/skulid/internal/db"
	"github.com/ryakel/skulid/internal/worker"
)

type Handler struct {
	tokens *db.SyncTokenRepo
	mgr    *worker.Manager
	log    *slog.Logger
}

func NewHandler(tokens *db.SyncTokenRepo, mgr *worker.Manager, log *slog.Logger) *Handler {
	return &Handler{tokens: tokens, mgr: mgr, log: log}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	channelID := r.Header.Get("X-Goog-Channel-Id")
	resourceID := r.Header.Get("X-Goog-Resource-Id")
	resourceState := r.Header.Get("X-Goog-Resource-State")
	channelToken := r.Header.Get("X-Goog-Channel-Token")
	if channelID == "" {
		http.Error(w, "missing channel id", http.StatusBadRequest)
		return
	}

	tok, err := h.tokens.FindByChannelID(r.Context(), channelID)
	if err != nil {
		h.log.Error("webhook lookup failed", "err", err)
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	if tok == nil {
		// Unknown channel: ack so Google stops retrying.
		w.WriteHeader(http.StatusOK)
		return
	}
	if tok.WatchTokenSecret != "" && subtle.ConstantTimeCompare([]byte(tok.WatchTokenSecret), []byte(channelToken)) != 1 {
		http.Error(w, "bad token", http.StatusUnauthorized)
		return
	}
	if resourceID != "" && tok.WatchResourceID != "" && tok.WatchResourceID != resourceID {
		// Resource id mismatch — record but accept.
		h.log.Warn("webhook resource id mismatch", "stored", tok.WatchResourceID, "got", resourceID)
	}
	// "sync" is the initial confirmation — no event to process.
	if resourceState != "sync" {
		h.mgr.EnqueueCalendar(tok.AccountID, tok.CalendarID)
	}
	w.WriteHeader(http.StatusOK)
}
