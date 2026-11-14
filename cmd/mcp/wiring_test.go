package main

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/IQVO/pricing/internal/adapters/outbound/telemetry"
)

// TestBuildDepsRunsMigrationsAgainstTheMigrationsDSN proves the migrations get
// MIGRATIONS_DATABASE_URL and not DATABASE_URL: the migrations DSN is a
// schemeless string migrate rejects at once with a distinctive parse error,
// the pool DSN points where nothing listens (a dial or "connection refused"
// would mean the migrations ran against it). The first failed attempt is
// logged by bootretry before it backs off; the context ends the retry so the
// test does not sleep out the real budget.
func TestBuildDepsRunsMigrationsAgainstTheMigrationsDSN(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()

	cfg := bootConfig{
		databaseURL:   "postgres://u@127.0.0.1:1/db?sslmode=disable&connect_timeout=1",
		migrationsURL: "not-a-valid-connection-string",
	}
	_, closeDeps, err := buildDeps(ctx, cfg, logger)
	defer closeDeps()
	if err == nil {
		t.Fatal("a malformed migrations DSN must fail boot")
	}
	if !strings.Contains(logs.String(), "parse scheme") {
		t.Fatalf("log = %s\nwant the parse error of the bogus MIGRATIONS_DATABASE_URL; a dial error would mean migrations ran against DATABASE_URL", logs.String())
	}
}

// An unreachable database is a boot error after the bounded retries.
func TestBuildDepsBadDatabaseFailsBoot(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // a cancelled context makes bootretry give up at once
	cfg := bootConfig{databaseURL: "postgres://u@127.0.0.1:1/db", migrationsURL: "postgres://u@127.0.0.1:1/db"}
	_, closeDeps, err := buildDeps(ctx, cfg, quietLogger())
	defer closeDeps()
	if err == nil {
		t.Fatal("an unreachable database must fail boot")
	}
}

func TestLoadTelemetryEnvDefaultsToThePricingMCPServiceName(t *testing.T) {
	got := loadTelemetryEnv(envOf(map[string]string{}))
	if got.serviceName != "pricing-mcp" || got.serviceVersion != telemetry.DefaultServiceVersion || got.otlpEndpoint != telemetry.DefaultEndpoint {
		t.Fatalf("defaults = %+v, want service.name pricing-mcp and the telemetry package defaults", got)
	}
	tuned := loadTelemetryEnv(envOf(map[string]string{"OTEL_SERVICE_NAME": "prices-mcp", "SERVICE_VERSION": "v9", "OTEL_EXPORTER_OTLP_ENDPOINT": "c:4317"}))
	if tuned.serviceName != "prices-mcp" || tuned.serviceVersion != "v9" || tuned.otlpEndpoint != "c:4317" {
		t.Fatalf("overrides = %+v", tuned)
	}
}

func TestNewLoggerLevels(t *testing.T) {
	for level, want := range map[string]slog.Level{"debug": slog.LevelDebug, "WARN": slog.LevelWarn, "warning": slog.LevelWarn, "error": slog.LevelError, "": slog.LevelInfo, "bogus": slog.LevelInfo} {
		if got := newLogger(level); !got.Handler().Enabled(context.Background(), want) || (want > slog.LevelDebug && got.Handler().Enabled(context.Background(), want-1)) {
			t.Errorf("newLogger(%q): level %v not the minimum enabled", level, want)
		}
	}
}
