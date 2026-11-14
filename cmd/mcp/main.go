// Command mcp is the composition root for the pricing MCP server
// (docs/adr/0009-read-only-mcp-server.md): it wires env config to the Postgres
// read side, the read side to the two query use cases the REST GETs and the
// A2A skills already use (GetItemPrice, GetPriceQuote), and those to the
// inbound MCP adapter, then serves MCP over Streamable HTTP (and nothing else:
// no stdio, no SSE). It is an independent deployable alongside cmd/api and
// cmd/a2a.
//
// The surface is READ-ONLY and a deliberately thin clone of cmd/a2a: it keeps
// the Postgres pool and the item-price and quote repositories its two tools
// read and drops everything else. No write use case and no write seam is wired
// here at all (no unit of work, no outbox, no inbox), so this binary can never
// append to the outbox. There is no REST route, no outbox relay, no Kafka
// consumer or producer and no policy loop, so it reads neither KAFKA_BROKERS
// nor any consumer-group variable. Like cmd/api it runs on real Postgres only:
// there is no in-memory mode, and a missing mandatory variable is a boot error
// naming it, raised before telemetry, any connection and the listener. It makes
// no REST, A2A or MCP call to any sibling context.
//
// There is NO auth of any kind (fleet-wide revert 2026-09-11): no keys, no
// bearer checks. Access control is the in-cluster ClusterIP boundary.
//
// Environment:
//
//	DATABASE_URL                     REQUIRED. Postgres DSN (the service database cmd/api owns)
//	MIGRATIONS_DATABASE_URL          direct DSN for the boot migrations (default DATABASE_URL)
//	MCP_ADDR                         listen address (default :8090)
//	LOG_LEVEL                        debug | info | warn | error (default info)
//	OTEL_SERVICE_NAME                service.name of spans and metrics (default pricing-mcp)
//	OTEL_EXPORTER_OTLP_ENDPOINT      Collector OTLP/gRPC endpoint (default telemetry.DefaultEndpoint); export never blocks
//	SERVICE_VERSION                  service.version, logged at boot too (default dev)
//	ENVIRONMENT                      deployment.environment.name, logged at boot too (default local)
//
// GET /healthz is liveness only (and the Kubernetes readiness probe: the
// server holds no dependency other than Postgres, which each tool call reports
// in band). The MCP endpoint is mounted at both / and /mcp.
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

	inboundmcp "github.com/IQVO/pricing/internal/adapters/inbound/mcp"
	"github.com/IQVO/pricing/internal/adapters/outbound/clock"
	"github.com/IQVO/pricing/internal/adapters/outbound/postgres"
	"github.com/IQVO/pricing/internal/application/usecases"
	"github.com/IQVO/pricing/internal/bootretry"
)

// shutdownTimeout bounds the HTTP server's graceful drain.
const shutdownTimeout = 10 * time.Second

func main() {
	if err := run(context.Background()); err != nil {
		slog.Error("mcp server exited with error", "error", err)
		os.Exit(1)
	}
}

// run boots the server and serves until SIGINT/SIGTERM or until ctx is
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

	logger.Info("mcp server starting",
		"version", getenv("SERVICE_VERSION", "dev"), "environment", getenv("ENVIRONMENT", "local"))

	srv := &http.Server{
		Addr:              getenv("MCP_ADDR", ":8090"),
		Handler:           newRouter(inboundmcp.Handler(inboundmcp.NewServer(deps)), logger, tel.serviceName),
		ReadHeaderTimeout: 5 * time.Second,
	}
	return serve(ctx, logger, srv)
}

// buildDeps runs the embedded migrations, opens the Postgres pool and wires the
// two query use cases the tools call. Only the item-price and quote
// repositories and the clock are set on the use cases' Deps: nothing else is
// reachable, so no command use case could run even by mistake. The returned
// close func releases the pool.
//
// Like cmd/api this binary runs the idempotent embedded migrations on start,
// so it can boot against a fresh database before cmd/api has; golang-migrate's
// advisory lock makes concurrent starts safe. cfg.migrationsURL is used ONLY
// for that step (a DIRECT Postgres DSN where DATABASE_URL points at a pooled
// connection, which cannot honour the session-scoped advisory lock); the
// runtime pool always uses cfg.databaseURL.
func buildDeps(ctx context.Context, cfg bootConfig, logger *slog.Logger) (inboundmcp.Deps, func(), error) {
	noop := func() {}
	// The first outbound dial of an injected pod can be reset (native
	// sidecars), so migrations and the first ping retry with backoff; on
	// exhaustion the LAST error is returned and the process refuses to boot.
	if err := bootretry.Retry(ctx, logger, "run migrations", func() error { return postgres.RunMigrations(cfg.migrationsURL) }); err != nil {
		return inboundmcp.Deps{}, noop, err
	}
	pool, err := postgres.NewPool(ctx, cfg.databaseURL)
	if err != nil {
		return inboundmcp.Deps{}, noop, err
	}
	if err := bootretry.Retry(ctx, logger, "ping postgres", func() error { return pool.Ping(ctx) }); err != nil {
		pool.Close()
		return inboundmcp.Deps{}, noop, err
	}
	logger.Info("postgres read side configured")

	d := usecases.Deps{
		ItemPrices: postgres.NewItemPriceRepository(pool),
		Quotes:     postgres.NewQuoteRepository(pool),
		Clock:      clock.System{},
	}
	return inboundmcp.Deps{GetItemPrice: usecases.GetItemPrice{Deps: d}, GetPriceQuote: usecases.GetPriceQuote{Deps: d}}, pool.Close, nil
}

// serve runs srv until it fails, SIGINT/SIGTERM arrives or ctx is cancelled,
// shutting the server down gracefully on a signal.
func serve(ctx context.Context, logger *slog.Logger, srv *http.Server) error {
	errCh := make(chan error, 1)
	go func() {
		logger.Info("mcp server listening (Streamable HTTP)", "addr", srv.Addr)
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
