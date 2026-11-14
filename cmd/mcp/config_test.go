package main

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"
)

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func fullEnv() map[string]string {
	return map[string]string{"DATABASE_URL": "postgres://u@127.0.0.1:1/db?sslmode=disable"}
}

func TestLoadBootConfigAcceptsACompleteEnvironment(t *testing.T) {
	cfg, err := loadBootConfig(envOf(fullEnv()))
	if err != nil || cfg.databaseURL == "" {
		t.Fatalf("config = %+v, %v", cfg, err)
	}
	if cfg.migrationsURL != cfg.databaseURL {
		t.Fatalf("migrationsURL = %q, want the DATABASE_URL fallback %q", cfg.migrationsURL, cfg.databaseURL)
	}
}

// MIGRATIONS_DATABASE_URL is the direct, non-pooled DSN for the boot
// migrations only; the pool keeps DATABASE_URL. Unset falls back to it.
func TestLoadBootConfigKeepsTheMigrationsDSNApart(t *testing.T) {
	env := fullEnv()
	env["MIGRATIONS_DATABASE_URL"] = "postgres://u@direct:5432/db"
	cfg, err := loadBootConfig(envOf(env))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.migrationsURL != "postgres://u@direct:5432/db" || cfg.databaseURL != env["DATABASE_URL"] {
		t.Fatalf("config = %+v: migrations must use the direct DSN and the pool the pooled one", cfg)
	}
}

// The server is read-only: Kafka, the publisher and every consumer group are
// not part of its contract, so a deployment that still sets them boots.
func TestLoadBootConfigIgnoresKafkaAndOutboxVariables(t *testing.T) {
	env := fullEnv()
	env["KAFKA_BROKERS"] = ""
	env["EVENT_PUBLISHER"] = "log"
	env["OFFER_AVAILABILITY_CONSUMER_GROUP"] = ""
	if _, err := loadBootConfig(envOf(env)); err != nil {
		t.Fatalf("a read-only server must not require or validate Kafka variables: %v", err)
	}
}

// A missing DATABASE_URL is a boot error naming it, never a silent in-memory
// fallback.
func TestLoadBootConfigRefusesAMissingDatabase(t *testing.T) {
	_, err := loadBootConfig(envOf(map[string]string{}))
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL is required: this context runs on real Postgres") {
		t.Fatalf("err = %v, want a boot error naming DATABASE_URL", err)
	}
}

// run fails fast on a bad configuration: before telemetry, any connection and
// before the listener binds.
func TestRunFailsFastBeforeBindingTheListener(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	l, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	t.Setenv("MCP_ADDR", addr)

	start := time.Now()
	if err := run(context.Background()); err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("run = %v, want a boot error naming DATABASE_URL", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("a configuration error must not wait on any dependency")
	}
	if c, derr := (&net.Dialer{Timeout: 200 * time.Millisecond}).DialContext(t.Context(), "tcp", addr); derr == nil {
		_ = c.Close()
		t.Fatal("the listener was bound although the configuration is invalid")
	}
}

func TestGetenv(t *testing.T) {
	t.Setenv("PRICING_MCP_TEST_SET", "v")
	t.Setenv("PRICING_MCP_TEST_EMPTY", "")
	if got := getenv("PRICING_MCP_TEST_SET", "d"); got != "v" {
		t.Errorf("set: %q", got)
	}
	if got := getenv("PRICING_MCP_TEST_EMPTY", "d"); got != "d" {
		t.Errorf("empty: %q", got)
	}
}
