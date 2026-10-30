package main

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"

	"github.com/IQVO/pricing/internal/adapters/outbound/telemetry"
)

// Boot errors for the variables the agent cannot run without. Each message
// names the variable.
var (
	errMissingDatabaseURL = errors.New("DATABASE_URL is required: this context runs on real Postgres, there is no in-memory mode")
	errNoAgentURL         = errors.New("A2A_AGENT_URL is required: the Agent Card must advertise the absolute URL of this agent's JSON-RPC endpoint (ADR 0007), e.g. http://<release>-a2a.<ns>.svc.cluster.local:8093/")
)

// bootConfig is the mandatory configuration of cmd/a2a: real Postgres and the
// URL the Agent Card advertises. There is no default and no degraded mode
// behind either.
type bootConfig struct {
	databaseURL string
	// migrationsURL is the direct DSN for the boot migrations (default databaseURL).
	migrationsURL string
	agentURL      string
}

// checkAgentURL validates A2A_AGENT_URL: an absolute http(s) URL. The card is
// the discovery contract: a relative or schemeless URL is a misconfiguration
// every client would hit, so it is refused at boot, never silently served.
func checkAgentURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return errNoAgentURL
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("A2A_AGENT_URL=%q is not an absolute http(s) URL: the Agent Card must advertise the endpoint clients dial (ADR 0007)", raw)
	}
	return nil
}

// loadBootConfig reads and validates the mandatory environment before
// anything is opened or bound. Every problem is reported at once, each naming
// its variable. KAFKA_BROKERS and every Kafka/outbox variable are deliberately
// NOT read: this agent is read-only and neither produces nor consumes.
func loadBootConfig(env func(string) string) (bootConfig, error) {
	cfg := bootConfig{databaseURL: env("DATABASE_URL"), agentURL: strings.TrimSpace(env("A2A_AGENT_URL"))}
	cfg.migrationsURL = orDefault(env("MIGRATIONS_DATABASE_URL"), cfg.databaseURL)
	var errs []error
	if cfg.databaseURL == "" {
		errs = append(errs, errMissingDatabaseURL)
	}
	errs = append(errs, checkAgentURL(cfg.agentURL))
	if err := errors.Join(errs...); err != nil {
		return bootConfig{}, fmt.Errorf("invalid configuration: %w", err)
	}
	return cfg, nil
}

// serviceVersion is the version reported on the Agent Card and, through
// telemetryEnv, as service.version.
func serviceVersion(env func(string) string) string {
	return orDefault(env("SERVICE_VERSION"), telemetry.DefaultServiceVersion)
}

// newLogger builds the JSON logger; LOG_LEVEL debug|info|warn|warning|error
// (case-insensitive). The handler is wrapped so a record written with a
// span-carrying context also carries trace_id and span_id.
func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(telemetry.WithTraceContext(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})))
}

func getenv(key, fallback string) string {
	return orDefault(os.Getenv(key), fallback)
}
