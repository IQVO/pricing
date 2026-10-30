package main

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"
)

const testAgentURL = "http://pricing-a2a.warehouse-systems.svc.cluster.local:8093/"

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func fullEnv() map[string]string {
	return map[string]string{"DATABASE_URL": "postgres://u@127.0.0.1:1/db?sslmode=disable", "A2A_AGENT_URL": testAgentURL}
}

func TestLoadBootConfigAcceptsACompleteEnvironment(t *testing.T) {
	cfg, err := loadBootConfig(envOf(fullEnv()))
	if err != nil || cfg.databaseURL == "" || cfg.agentURL != testAgentURL {
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

// The agent is read-only: Kafka, the publisher and every consumer group are
// not part of its contract, so a deployment that still sets them boots.
func TestLoadBootConfigIgnoresKafkaAndOutboxVariables(t *testing.T) {
	env := fullEnv()
	env["KAFKA_BROKERS"] = ""
	env["EVENT_PUBLISHER"] = "log"
	env["OFFER_AVAILABILITY_CONSUMER_GROUP"] = ""
	if _, err := loadBootConfig(envOf(env)); err != nil {
		t.Fatalf("a read-only agent must not require or validate Kafka variables: %v", err)
	}
}

// Each missing or rejected value is a boot error that names its variable.
func TestLoadBootConfigRefusesEachMissingOrRejectedValue(t *testing.T) {
	cases := []struct {
		name, variable, value, want string
	}{
		{"database unset", "DATABASE_URL", "", "DATABASE_URL is required: this context runs on real Postgres"},
		{"agent url unset", "A2A_AGENT_URL", "", "A2A_AGENT_URL is required: the Agent Card must advertise"},
		{"agent url blank", "A2A_AGENT_URL", "   ", "A2A_AGENT_URL is required"},
		{"agent url relative", "A2A_AGENT_URL", "/a2a", `A2A_AGENT_URL="/a2a" is not an absolute http(s) URL`},
		{"agent url schemeless", "A2A_AGENT_URL", "pricing-a2a:8093", "is not an absolute http(s) URL"},
		{"agent url wrong scheme", "A2A_AGENT_URL", "ftp://host/", "is not an absolute http(s) URL"},
		{"agent url no host", "A2A_AGENT_URL", "http:///a2a", "is not an absolute http(s) URL"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := fullEnv()
			env[tc.variable] = tc.value
			_, err := loadBootConfig(envOf(env))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("%s=%q: %v, want a boot error containing %q", tc.variable, tc.value, err, tc.want)
			}
			if !strings.Contains(err.Error(), tc.variable) {
				t.Fatalf("the error does not name %s: %v", tc.variable, err)
			}
		})
	}
}

// Every problem is reported at once.
func TestLoadBootConfigReportsEveryProblem(t *testing.T) {
	_, err := loadBootConfig(envOf(map[string]string{}))
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") || !strings.Contains(err.Error(), "A2A_AGENT_URL") {
		t.Fatalf("err = %v, want both DATABASE_URL and A2A_AGENT_URL named", err)
	}
}

// run fails fast on a bad configuration: before telemetry, any connection and
// before the listener binds.
func TestRunFailsFastBeforeBindingTheListener(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u@127.0.0.1:1/db?sslmode=disable")
	t.Setenv("A2A_AGENT_URL", "not-a-url")
	l, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	t.Setenv("A2A_ADDR", addr)

	start := time.Now()
	if err := run(context.Background()); err == nil || !strings.Contains(err.Error(), "A2A_AGENT_URL") {
		t.Fatalf("run = %v, want a boot error naming A2A_AGENT_URL", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("a configuration error must not wait on any dependency")
	}
	if c, derr := (&net.Dialer{Timeout: 200 * time.Millisecond}).DialContext(t.Context(), "tcp", addr); derr == nil {
		_ = c.Close()
		t.Fatal("the listener was bound although the configuration is invalid")
	}
}
