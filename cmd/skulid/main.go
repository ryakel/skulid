// Command skulid is the entrypoint for the skulid daemon: a self-hosted,
// single-user Google Calendar sync server.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ryakel/skulid/internal/ai"
	"github.com/ryakel/skulid/internal/auth"
	"github.com/ryakel/skulid/internal/calendar"
	"github.com/ryakel/skulid/internal/config"
	"github.com/ryakel/skulid/internal/crypto"
	"github.com/ryakel/skulid/internal/db"
	"github.com/ryakel/skulid/internal/httpx"
	syncengine "github.com/ryakel/skulid/internal/sync"
	"github.com/ryakel/skulid/internal/webhook"
	"github.com/ryakel/skulid/internal/worker"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)

	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log.Info("config loaded", "external_url", cfg.ExternalURL, "listen", cfg.ListenAddr)

	migrateCtx, cancelMigrate := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelMigrate()
	if err := db.Migrate(migrateCtx, cfg.DatabaseURL); err != nil {
		return err
	}
	log.Info("migrations applied")

	connectCtx, cancelConnect := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelConnect()
	pool, err := db.Connect(connectCtx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	sealer, err := crypto.NewSealer(cfg.EncryptionKey)
	if err != nil {
		return err
	}

	settings := db.NewSettingRepo(pool)
	accounts := db.NewAccountRepo(pool)
	calendars := db.NewCalendarRepo(pool)
	tokens := db.NewSyncTokenRepo(pool)
	rules := db.NewSyncRuleRepo(pool)
	blocks := db.NewSmartBlockRepo(pool)
	managed := db.NewManagedBlockRepo(pool)
	links := db.NewEventLinkRepo(pool)
	audit := db.NewAuditRepo(pool)
	aiConversations := db.NewAIConversationRepo(pool)
	aiMessages := db.NewAIMessageRepo(pool)
	aiPending := db.NewAIPendingActionRepo(pool)

	// Record the configured external URL so it's visible in /settings.
	_ = settings.Set(context.Background(), db.SettingExternalURL, cfg.ExternalURL)

	oauth := auth.NewOAuthProvider(cfg.GoogleClientID, cfg.GoogleClientSecret, cfg.RedirectURL())
	tofu := auth.NewTOFU(settings)
	secure := isHTTPS(cfg.ExternalURL)
	sessions := auth.NewSessionManager(cfg.SessionSecret, secure)

	clientFor := func(ctx context.Context, accountID int64) (*calendar.Client, error) {
		ts := auth.NewAccountTokenSource(oauth, sealer, accounts, accountID)
		return calendar.New(ctx, ts)
	}

	engine := syncengine.NewEngine(rules, calendars, links, audit, clientFor, log)
	smartEngine := syncengine.NewSmartBlockEngine(blocks, managed, calendars, audit, clientFor, log)

	mgr := worker.NewManager(pool, accounts, calendars, tokens, rules, blocks, links, audit,
		clientFor, engine, smartEngine, cfg.ExternalURL, log)
	mgr.SetAIConversationCleanup(aiConversations, 30*24*time.Hour)

	var agent *ai.Agent
	if cfg.AnthropicAPIKey != "" {
		agent = ai.NewAgent(
			ai.NewClient(cfg.AnthropicAPIKey, cfg.AnthropicModel),
			aiConversations, aiMessages, aiPending,
			accounts, calendars, audit, clientFor, log,
		)
		log.Info("ai assistant enabled", "model", cfg.AnthropicModel)
	} else {
		log.Info("ai assistant disabled (set ANTHROPIC_API_KEY to enable)")
	}

	rootCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := mgr.Start(rootCtx); err != nil {
		return err
	}
	defer mgr.Stop()

	renderer, err := httpx.NewRenderer()
	if err != nil {
		return err
	}

	hookHandler := webhook.NewHandler(tokens, mgr, log)

	srv := &httpx.Server{
		Cfg:            cfg,
		Sealer:         sealer,
		Sessions:       sessions,
		OAuth:          oauth,
		TOFU:           tofu,
		Settings:       settings,
		Accounts:       accounts,
		Calendars:      calendars,
		Tokens:         tokens,
		Rules:          rules,
		Blocks:         blocks,
		Managed:        managed,
		Links:          links,
		Audit:          audit,
		Engine:          engine,
		ClientFor:       clientFor,
		Worker:          mgr,
		Renderer:        renderer,
		WebhookHandler:  hookHandler,
		AIConversations: aiConversations,
		AIMessages:      aiMessages,
		AIPending:       aiPending,
		Agent:           agent,
		Log:             log,
	}

	httpSrv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           srv.Router(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("http server listening", "addr", cfg.ListenAddr)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case <-rootCtx.Done():
		log.Info("shutdown signal received")
	case err := <-errCh:
		log.Error("http server failed", "err", err)
	}

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelShutdown()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		log.Error("http shutdown", "err", err)
	}
	return nil
}

func isHTTPS(url string) bool {
	return len(url) >= 8 && url[:8] == "https://"
}
