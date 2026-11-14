package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/IQVO/pricing/internal/adapters/outbound/telemetry"
)

// errMissingDatabaseURL is the boot error of a missing DATABASE_URL: this
// binary has no in-memory mode.
var errMissingDatabaseURL = errors.New("DATABASE_URL is required: this context runs on real Postgres, there is no in-memory mode")

// bootConfig is the mandatory configuration of cmd/mcp: real Postgres. There
// is no default and no degraded mode behind it.
type bootConfig struct {
	databaseURL string
	// migrationsURL is the direct DSN for the boot migrations (default databaseURL).
	migrationsURL string
}

// loadBootConfig reads and validates the mandatory environment before
// anything is opened or bound. KAFKA_BROKERS and every Kafka/outbox variable
// are deliberately NOT read: this server is read-only and neither produces nor
// consumes.
func loadBootConfig(env func(string) string) (bootConfig, error) {
	cfg := bootConfig{databaseURL: env("DATABASE_URL")}
	cfg.migrationsURL = orDefault(env("MIGRATIONS_DATABASE_URL"), cfg.databaseURL)
	if cfg.databaseURL == "" {
		return bootConfig{}, fmt.Errorf("invalid configuration: %w", errMissingDatabaseURL)
	}
	return cfg, nil
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
