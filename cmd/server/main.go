package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/EternisAI/takes-replies-basket-test/internal/host"
	"github.com/EternisAI/takes-replies-basket-test/internal/httpapi"
	"github.com/EternisAI/takes-replies-basket-test/internal/llm"
	"github.com/EternisAI/takes-replies-basket-test/internal/takesreply"
	"github.com/EternisAI/takes-replies-basket-test/internal/takesresearch"
	pgmigrate "github.com/EternisAI/takes-replies-basket-test/storage/pg"
	replydb "github.com/EternisAI/takes-replies-basket-test/storage/pg/generated/takesreply"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	defaultReplyModel    = "anthropic/claude-sonnet-5.5"
	defaultEvidenceModel = "google/gemini-3.8-flash"
	defaultBasketModel   = "google/gemini-3.8-flash"
)

func main() {
	if err := run(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}

func run() error {
	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		return errors.New("DATABASE_URL is required")
	}
	token := strings.TrimSpace(os.Getenv("AUTH_BEARER_TOKEN"))
	if token == "" {
		return errors.New("AUTH_BEARER_TOKEN is required")
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := pgmigrate.Migrate(ctx, databaseURL); err != nil {
		return fmt.Errorf("standalone database migration failed: %w", err)
	}
	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	poolConfig.MaxConns = 12
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return fmt.Errorf("connect to standalone database: %w", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("check standalone database connection: %w", err)
	}

	apiKey := strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY"))
	completer := host.NewOpenRouterCompleter(apiKey)
	usage := host.NewUsageLedger(pool, logger)
	service, err := takesreply.NewService(pool, replydb.New(pool), completer, host.Admission{Usage: usage}, takesreply.Config{
		ConstructionMode: takesresearch.ExpressionMode,
		ReplyModel:       envOr("REPLY_MODEL", defaultReplyModel),
		EvidenceModel:    envOr("EVIDENCE_MODEL", defaultEvidenceModel),
	}, logger)
	if err != nil {
		return fmt.Errorf("configure reply service: %w", err)
	}

	dexes := splitCSV(envOr("HYPERLIQUID_DEXES", "xyz"))
	catalog := host.NewHyperliquidCatalogReader(&http.Client{Timeout: 10 * time.Second}, dexes)
	identity := takesresearch.NewSECIdentityReader(&http.Client{Timeout: 10 * time.Second})
	basketModel := envOr("BASKET_MODEL", defaultBasketModel)
	builder := takesresearch.NewExpressionBuilder(catalog, takesresearch.NewOpenRouterExpressionPlanner(completer, basketModel), identity)
	worker := takesreply.NewBuildWorker(pool, catalog, builder, usage, nil, logger)
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		worker.Run(ctx, 2*time.Second)
	}()

	owner := envOr("AUTH_USER_ID", "test-owner")
	handler, err := httpapi.New(service, httpapi.Config{
		BearerToken: token, OwnerUserID: owner, ModelConfigured: apiKey != "", DatabasePing: pool.Ping,
	})
	if err != nil {
		return fmt.Errorf("configure HTTP API: %w", err)
	}
	port := envOr("PORT", "8080")
	server := &http.Server{Addr: ":" + port, Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	serverErr := make(chan error, 1)
	go func() { serverErr <- server.ListenAndServe() }()
	logger.Info("standalone Takes reply service started", "port", port, "owner", owner, "modelConfigured", apiKey != "", "marketDexes", dexes)
	var serveFailure error
	select {
	case <-ctx.Done():
	case err := <-serverErr:
		if !errors.Is(err, http.ErrServerClosed) {
			serveFailure = fmt.Errorf("HTTP server failed: %w", err)
		}
	}
	stop()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	if err := server.Shutdown(shutdownCtx); err != nil {
		cancel()
		return fmt.Errorf("shut down HTTP server: %w", err)
	}
	cancel()
	select {
	case <-workerDone:
	case <-time.After(15 * time.Second):
		return errors.New("build worker did not stop before the shutdown deadline")
	}
	if serveFailure != nil {
		return serveFailure
	}
	return nil
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func splitCSV(value string) []string {
	var values []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			values = append(values, item)
		}
	}
	return values
}

var _ llm.Completer = host.NewOpenRouterCompleter("")
