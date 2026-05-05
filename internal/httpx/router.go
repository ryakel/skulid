package httpx

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/ryakel/skulid/internal/ai"
	"github.com/ryakel/skulid/internal/auth"
	"github.com/ryakel/skulid/internal/config"
	"github.com/ryakel/skulid/internal/crypto"
	"github.com/ryakel/skulid/internal/db"
	syncengine "github.com/ryakel/skulid/internal/sync"
	"github.com/ryakel/skulid/internal/webhook"
	"github.com/ryakel/skulid/internal/worker"
)

type Server struct {
	Cfg            *config.Config
	// Version is the build tag injected at compile time
	// (`-ldflags "-X main.appVersion=..."`); rendered in the page footer
	// so a user can tell which container build they're looking at.
	Version        string
	Sealer         *crypto.Sealer
	Sessions       *auth.SessionManager
	OAuth          *auth.OAuthProvider
	TOFU           *auth.TOFU
	Settings       *db.SettingRepo
	Accounts       *db.AccountRepo
	Calendars      *db.CalendarRepo
	Tokens         *db.SyncTokenRepo
	Rules          *db.SyncRuleRepo
	Blocks         *db.SmartBlockRepo
	Managed        *db.ManagedBlockRepo
	Links          *db.EventLinkRepo
	Audit          *db.AuditRepo
	Categories     *db.CategoryRepo
	Tasks          *db.TaskRepo
	Habits         *db.HabitRepo
	Occurrences    *db.HabitOccurrenceRepo
	Scheduler      *syncengine.Scheduler
	Engine         *syncengine.Engine
	ClientFor      syncengine.ClientFor
	Worker         *worker.Manager
	Renderer       *Renderer
	WebhookHandler *webhook.Handler
	// AI assistant — nil-safe; routes are only registered when Agent != nil.
	AIConversations *db.AIConversationRepo
	AIMessages      *db.AIMessageRepo
	AIPending       *db.AIPendingActionRepo
	Agent           *ai.Agent
	Log             *slog.Logger
}

func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(loggingMiddleware(s.Log))
	r.Use(middleware.Timeout(60 * time.Second))

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// Public routes (auth flow + webhook).
	r.Get("/login", s.handleLoginPage)
	r.Post("/login/start", s.handleLoginStart)
	r.Get("/auth/google/callback", s.handleAuthCallback)
	r.Post("/logout", s.handleLogout)
	r.Method(http.MethodPost, "/api/webhooks/google", s.WebhookHandler)

	// Dev-only: bypass real OAuth so the UI can be exercised against a
	// synthetic owner. Only registered when SKULID_DEV_AUTH_BYPASS is set.
	if s.Cfg.DevAuthBypass {
		r.Get("/dev/login", s.handleDevLogin)
	}

	// Owner-protected routes.
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireOwner(s.Sessions, s.TOFU))

		r.Get("/", s.handleDashboard)
		r.Get("/planner", s.handlePlannerPage)
		r.Get("/priorities", s.handlePrioritiesPage)

		r.Get("/accounts", s.handleAccountsPage)
		r.Post("/accounts/connect", s.handleAccountConnect)
		r.Post("/accounts/{id}/refresh", s.handleAccountRefresh)
		r.Post("/accounts/{id}/delete", s.handleAccountDelete)
		r.Get("/calendars/{id}", s.handleCalendarSettings)
		r.Post("/calendars/{id}", s.handleCalendarSettingsSave)
		r.Post("/calendars/{id}/enabled", s.handleCalendarToggleEnabled)

		r.Get("/rules", s.handleRulesPage)
		r.Get("/rules/new", s.handleRuleEditPage)
		r.Get("/rules/{id}", s.handleRuleEditPage)
		r.Post("/rules", s.handleRuleSave)
		r.Post("/rules/{id}", s.handleRuleSave)
		r.Post("/rules/{id}/delete", s.handleRuleDelete)
		r.Post("/rules/{id}/sync", s.handleRuleSyncNow)
		r.Post("/rules/{id}/backfill", s.handleRuleBackfill)

		r.Get("/blocks", s.handleBlocksPage)
		r.Get("/blocks/new", s.handleBlockEditPage)
		r.Get("/blocks/{id}", s.handleBlockEditPage)
		r.Post("/blocks", s.handleBlockSave)
		r.Post("/blocks/{id}", s.handleBlockSave)
		r.Post("/blocks/{id}/delete", s.handleBlockDelete)
		r.Post("/blocks/{id}/recompute", s.handleBlockRecompute)

		r.Get("/tasks", s.handleTasksPage)
		r.Get("/tasks/new", s.handleTaskEditPage)
		r.Get("/tasks/{id}", s.handleTaskEditPage)
		r.Post("/tasks", s.handleTaskSave)
		r.Post("/tasks/{id}", s.handleTaskSave)
		r.Post("/tasks/{id}/delete", s.handleTaskDelete)
		r.Post("/tasks/{id}/place", s.handleTaskPlace)
		r.Post("/tasks/{id}/complete", s.handleTaskComplete)

		r.Get("/habits", s.handleHabitsPage)
		r.Get("/habits/new", s.handleHabitEditPage)
		r.Get("/habits/{id}", s.handleHabitEditPage)
		r.Post("/habits", s.handleHabitSave)
		r.Post("/habits/{id}", s.handleHabitSave)
		r.Post("/habits/{id}/delete", s.handleHabitDelete)
		r.Post("/habits/{id}/place", s.handleHabitPlace)

		r.Get("/audit", s.handleAuditPage)

		r.Get("/settings", s.handleSettingsPage)
		r.Post("/settings/rewatch", s.handleRewatchAll)
		r.Get("/settings/categories", s.handleCategoriesPage)
		r.Post("/settings/categories", s.handleCategoriesSave)
		r.Get("/settings/hours", s.handleHoursPage)
		r.Post("/settings/hours/{id}", s.handleHoursSave)
		r.Get("/settings/buffers", s.handleBuffersPage)
		r.Post("/settings/buffers", s.handleBuffersSave)
		r.Post("/settings/buffers/recompute", s.handleBuffersRecompute)

		if s.Agent != nil {
			r.Get("/assistant", s.handleAssistantList)
			r.Post("/assistant", s.handleAssistantNew)
			r.Get("/assistant/{id}", s.handleAssistantChat)
			r.Post("/assistant/{id}/message", s.handleAssistantSend)
			r.Post("/assistant/{id}/actions/{aid}/apply", s.handleAssistantApply)
			r.Post("/assistant/{id}/actions/{aid}/reject", s.handleAssistantReject)
			r.Post("/assistant/{id}/delete", s.handleAssistantDelete)
		}
	})

	r.Get("/static/*", http.StripPrefix("/static/", staticHandler()).ServeHTTP)
	return r
}

func loggingMiddleware(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			// Skip noisy paths.
			if strings.HasPrefix(r.URL.Path, "/static/") || r.URL.Path == "/healthz" {
				return
			}
			log.Info("http",
				"method", r.Method,
				"path", r.URL.Path,
				"status", ww.Status(),
				"bytes", ww.BytesWritten(),
				"dur", time.Since(start).String(),
			)
		})
	}
}

// requestContext returns a context with a deadline aligned to the request.
func requestContext(r *http.Request, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), d)
}
