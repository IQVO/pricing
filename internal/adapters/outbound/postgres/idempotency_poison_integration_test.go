//go:build integration

package postgres_test

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/IQVO/pricing/internal/adapters/outbound/postgres"
	"github.com/IQVO/pricing/internal/adapters/outbound/postgres/pgtx"
	"github.com/IQVO/pricing/internal/application/idempotency"
	"github.com/IQVO/pricing/internal/testing/pgtest"
)

// The tcpostgres reference keeps the testcontainers postgres module import
// reachable in this file's package (required by internal/architecture's
// TestPostgresIntegrationTestsUseTestcontainers for any file that talks to pgx;
// the database itself comes from the shared pgtest helper).
var _ = tcpostgres.Run

// poisonTx simulates what a mapped domain conflict does to the store's
// transaction: a statement fails on the server (a unique violation, like a
// request losing a race on a unique index), the repository translates it to a
// typed error and the handler answers 4xx -- but the transaction is ABORTED from
// then on. The store must still record and return that truthful 4xx, never fail.
func poisonTx(t *testing.T, ctx context.Context) {
	t.Helper()
	tx, ok := pgtx.From(ctx)
	if !ok {
		t.Fatal("the store did not bind a transaction into the context")
	}
	for range 2 {
		if _, err := tx.Exec(ctx, `INSERT INTO idempotency_keys (route, key, request_hash, status, content_type, body, created_at)
			VALUES ('poison', 'dup', 'h', 200, '', ''::bytea, now())`); err != nil {
			return // the second insert aborts the transaction (23505)
		}
	}
	t.Fatal("the duplicate insert did not abort the transaction")
}

func conflictRecord() idempotency.Record {
	return idempotency.Record{Status: http.StatusConflict, ContentType: "application/problem+json", Body: []byte(`{"status":409}`)}
}

// TestIdempotencyStore_A409OverAnAbortedTransactionCommitsTheOutcome: a request
// that loses a unique-index race answers 409, not 500, and that 409 is stored
// and replayed -- a retry with the same key never re-runs the handler.
func TestIdempotencyStore_A409OverAnAbortedTransactionCommitsTheOutcome(t *testing.T) {
	pool := pgtest.NewPool(t)
	store := postgres.NewIdempotencyStore(pool)
	var calls atomic.Int64
	fn := func(ctx context.Context) (idempotency.Record, error) {
		calls.Add(1)
		poisonTx(t, ctx)
		return conflictRecord(), nil
	}
	at := time.Now().UTC()

	rec, replayed, err := store.Do(t.Context(), "k-poison", "h1", at, fn)
	if err != nil || replayed || rec.Status != http.StatusConflict {
		t.Fatalf("first = %+v replayed=%v err=%v, want the truthful 409", rec, replayed, err)
	}
	var status int
	if err := pool.QueryRow(t.Context(), `SELECT status FROM idempotency_keys WHERE route = '' AND key = 'k-poison'`).Scan(&status); err != nil {
		t.Fatalf("the 409 outcome was not committed: %v", err)
	}
	if status != http.StatusConflict {
		t.Fatalf("stored status = %d", status)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM idempotency_keys WHERE route = 'poison'`); n != 0 {
		t.Fatalf("the handler's partial writes survived: %d rows", n)
	}

	again, replayed, err := store.Do(t.Context(), "k-poison", "h1", at, fn)
	if err != nil || !replayed || again.Status != http.StatusConflict || string(again.Body) != string(rec.Body) {
		t.Fatalf("replay = %+v replayed=%v err=%v, want the stored 409", again, replayed, err)
	}
	if calls.Load() != 1 {
		t.Fatalf("handler ran %d times, want 1 (the replay must not re-run it)", calls.Load())
	}
}

// TestIdempotencyStore_A2xxOverAnAbortedTransactionIsNeverStored: a success
// whose writes died with the transaction is a lie if returned or stored. The
// request fails, nothing is cached, and the same key retries the real work.
func TestIdempotencyStore_A2xxOverAnAbortedTransactionIsNeverStored(t *testing.T) {
	pool := pgtest.NewPool(t)
	store := postgres.NewIdempotencyStore(pool)
	var calls atomic.Int64
	fn := func(ctx context.Context) (idempotency.Record, error) {
		if calls.Add(1) == 1 {
			poisonTx(t, ctx) // only the first attempt dies inside the transaction
		}
		return idempotency.Record{Status: http.StatusCreated, Body: []byte(`{"ok":true}`)}, nil
	}
	at := time.Now().UTC()

	if rec, _, err := store.Do(t.Context(), "k-lie", "h1", at, fn); err == nil {
		t.Fatalf("first = %+v, want an error (the writes never happened)", rec)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM idempotency_keys WHERE key = 'k-lie'`); n != 0 {
		t.Fatalf("a failed success left %d idempotency rows: the key could not retry", n)
	}

	rec, replayed, err := store.Do(t.Context(), "k-lie", "h1", at, fn)
	if err != nil || replayed || rec.Status != http.StatusCreated {
		t.Fatalf("retry = %+v replayed=%v err=%v, want the real work to succeed", rec, replayed, err)
	}
	if calls.Load() != 2 {
		t.Fatalf("handler ran %d times, want 2", calls.Load())
	}
}

func countRows(t *testing.T, pool *pgxpool.Pool, sql string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(), sql).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
