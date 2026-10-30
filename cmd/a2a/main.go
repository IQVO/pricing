// Command a2a is the composition root for the pricing A2A agent: it wires env
// config to the Postgres read side, the read side to two query use cases, and
// those to the inbound A2A adapter, then serves Agent2Agent JSON-RPC over HTTP
// with the Agent Card at the well-known path. It is an independent deployable
// alongside cmd/api (ADR 0007).
//
// The agent is READ-ONLY and a deliberately thin clone of cmd/api: it keeps the
// Postgres pool and the item-price and quote repositories its two skills read
// and drops everything else. There is no REST route, no outbox relay, no Kafka
// consumer or producer, no policy loop and no readiness gate on Kafka, so it
// reads neither KAFKA_BROKERS nor any consumer-group variable. Like cmd/api it
// runs on real Postgres only: there is no in-memory mode, and a missing
// mandatory variable is a boot error naming it, raised before telemetry, any
// connection and the listener.
//
// Environment:
//
//	DATABASE_URL                     REQUIRED. Postgres DSN
//	A2A_AGENT_URL                    REQUIRED. Absolute http(s) URL of this agent's
//	                                 JSON-RPC endpoint, advertised on the Agent Card
//	MIGRATIONS_DATABASE_URL          direct DSN for the boot migrations (default DATABASE_URL)
//	A2A_ADDR                         listen address (default :8093)
//	LOG_LEVEL                        debug | info | warn | error (default info)
//	OTEL_SERVICE_NAME                service.name of spans and metrics (default pricing-a2a)
//	OTEL_EXPORTER_OTLP_ENDPOINT      Collector OTLP/gRPC endpoint (default telemetry.DefaultEndpoint); export never blocks
//	SERVICE_VERSION                  service.version and the Agent Card version (default dev)
//	ENVIRONMENT                      deployment.environment.name (default local)
//
// GET /healthz is liveness only (and the Kubernetes readiness probe: the agent
// holds no dependency other than Postgres, which each skill call reports in
// band). The Agent Card is at /.well-known/agent-card.json and the JSON-RPC
// endpoint is mounted at both / and /a2a.
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

	inbounda2a "github.com/IQVO/pricing/internal/adapters/inbound/a2a"
	"github.com/IQVO/pricing/internal/adapters/outbound/clock"
	"github.com/IQVO/pricing/internal/adapters/outbound/postgres"
	"github.com/IQVO/pricing/internal/application/usecases"
	"github.com/IQVO/pricing/internal/bootretry"
)

// shutdownTimeout bounds the HTTP server's graceful drain.
const shutdownTimeout = 10 * time.Second

func main() {
	if err := run(context.Background()); err != nil {
		slog.Error("a2a agent exited with error", "error", err)
		os.Exit(1)
	}
}

// run boots the agent and serves until SIGINT/SIGTERM or until ctx is
// cancelled (the integration test stops the real entry point that way).
func run(ctx context.Context) error {
	logger := newLogger(getenv("LOG_LEVEL", "info"))
	slog.SetDefault(logger)

	// Misconfiguration is refused before anything is opened or bound.
	cfg, err := loadBootConfig(os.Getenv)
	if err != nil {
		return err
	}

	// Telemetry comes up before the router is built (otelchimetric binds the
	// global MeterProvider at construction) and is flushed LAST: deferred before
	// everything below, so the final spans of the drain still leave the process.
	tel := loadTelemetryEnv(os.Getenv)
	flushTelemetry, err := startTelemetry(logger, tel)
	if err != nil {
		return err
	}
	defer flushTelemetry()

	deps, closeDeps, err := buildDeps(ctx, cfg, logger)
	if err != nil {
		return err
	}
	defer closeDeps()

	executor := inbounda2a.NewExecutor(deps)
	card := inbounda2a.Card(inbounda2a.Config{AgentURL: cfg.agentURL, Version: serviceVersion(os.Getenv)})
	srv := &http.Server{
		Addr:              getenv("A2A_ADDR", ":8093"),
		Handler:           newRouter(inbounda2a.Handler(executor), card, tel.serviceName),
		ReadHeaderTimeout: 5 * time.Second,
	}
	return serve(ctx, logger, srv)
}

// buildDeps runs the embedded migrations, opens the Postgres pool and wires the
// two query use cases the skills call. Only the item-price and quote
// repositories and the clock are set on the use cases' Deps: nothing else is
// reachable, so no command use case could run even by mistake.
func buildDeps(ctx context.Context, cfg bootConfig, logger *slog.Logger) (inbounda2a.Deps, func(), error) {
	noop := func() {}
	// The first outbound dial of an injected pod can be reset (Istio native
	// sidecars), so migrations and the first ping retry with backoff; on
	// exhaustion the LAST error is returned and the process refuses to boot.
	if err := bootretry.Retry(ctx, logger, "run migrations", func() error { return postgres.RunMigrations(cfg.migrationsURL) }); err != nil {
		return inbounda2a.Deps{}, noop, err
	}
	pool, err := postgres.NewPool(ctx, cfg.databaseURL)
	if err != nil {
		return inbounda2a.Deps{}, noop, err
	}
	if err := bootretry.Retry(ctx, logger, "ping postgres", func() error { return pool.Ping(ctx) }); err != nil {
		pool.Close()
		return inbounda2a.Deps{}, noop, err
	}
	logger.Info("postgres read side configured")

	d := usecases.Deps{
		ItemPrices: postgres.NewItemPriceRepository(pool),
		Quotes:     postgres.NewQuoteRepository(pool),
		Clock:      clock.System{},
	}
	return inbounda2a.Deps{GetItemPrice: usecases.GetItemPrice{Deps: d}, GetPriceQuote: usecases.GetPriceQuote{Deps: d}}, pool.Close, nil
}

// serve runs srv until it fails, SIGINT/SIGTERM arrives or ctx is cancelled,
// shutting the server down gracefully on a signal.
func serve(ctx context.Context, logger *slog.Logger, srv *http.Server) error {
	errCh := make(chan error, 1)
	go func() {
		logger.Info("a2a agent listening (JSON-RPC over HTTP)", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	sigCtx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	select {
	case err := <-errCh:
		return err
	case <-sigCtx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}
