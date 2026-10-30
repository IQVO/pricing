package main

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"
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
		agentURL:      testAgentURL,
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
