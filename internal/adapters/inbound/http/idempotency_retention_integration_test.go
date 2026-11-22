//go:build integration

// The idempotency_keys retention (ADR 0011, addendum), driven through the REAL
// Idempotency-Key middleware over a real Postgres (testcontainers via
// internal/testing/pgtest), so it runs in the CI integration job only:
// the purge deletes only expired rows, a replay of an expired key EXECUTES
// (the age guard of the read), and a purge racing a request that holds a key
// neither blocks nor breaks it (order-sourcing #84 / last-mile-delivery #86).
package http_test

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	httpadapter "github.com/IQVO/pricing/internal/adapters/inbound/http"
	"github.com/IQVO/pricing/internal/adapters/outbound/clock"
	"github.com/IQVO/pricing/internal/adapters/outbound/postgres"
	"github.com/IQVO/pricing/internal/application/idempotency"
	"github.com/IQVO/pricing/internal/application/ports"
	"github.com/IQVO/pricing/internal/testing/pgtest"
)

// retentionPurger is ports.IdempotencyStore's retention half. It is asserted,
// not typed, so this file also compiles on a tree without the method: there
// the assertion fails and every test below is red for exactly the missing
// retention (nothing deletes expired idempotency_keys rows).
type retentionPurger interface {
	PurgeExpired(ctx context.Context, limit int) (int, error)
}

// retentionEnv is the real store, the real middleware over it, and SQL helpers
// on the pool, over ONE clean database.
type retentionEnv struct {
	store *postgres.IdempotencyStore
	mw    func(next http.Handler) http.Handler
	exec  func(t *testing.T, query string, args ...any)
	count func(t *testing.T, query string, args ...any) int
	keys  func(t *testing.T) []string
	holds func(t *testing.T, key string) (commit, rollback func())
}

// newRetentionEnv boots one clean database and wires the production store and
// middleware (the claim's expiry is judged by the database's now(), so the
// clock is the system clock, like the wiring test).
func newRetentionEnv(t *testing.T) retentionEnv {
	t.Helper()
	pool := pgtest.NewPool(t)
	store := postgres.NewIdempotencyStore(pool)
	var clk ports.Clock = clock.System{}
	return retentionEnv{
		store: store,
		mw:    httpadapter.RequireIdempotencyKey(store, clk),
		exec: func(t *testing.T, query string, args ...any) {
			t.Helper()
			if _, err := pool.Exec(context.Background(), query, args...); err != nil {
				t.Fatalf("%s: %v", query, err)
			}
		},
		count: func(t *testing.T, query string, args ...any) int {
			t.Helper()
			var n int
			if err := pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
				t.Fatalf("%s: %v", query, err)
			}
			return n
		},
		keys: func(t *testing.T) []string {
			t.Helper()
			var joined string
			if err := pool.QueryRow(context.Background(), `SELECT coalesce(string_agg(key, ',' ORDER BY key), '') FROM idempotency_keys`).Scan(&joined); err != nil {
				t.Fatal(err)
			}
			if joined == "" {
				return nil
			}
			return strings.Split(joined, ",")
		},
		holds: func(t *testing.T, key string) (commit, rollback func()) {
			t.Helper()
			ctx := context.Background()
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			// What the store's claim does on an expired row: take it over
			// inside the request's transaction; the row lock lives until the
			// commit.
			tag, err := tx.Exec(ctx, `INSERT INTO idempotency_keys (route, key, request_hash, status, content_type, body, created_at)
				VALUES ('', $1, 'h2', 0, '', ''::bytea, now())
				ON CONFLICT (route, key) DO UPDATE SET request_hash = 'h2', status = 0,
					content_type = '', body = ''::bytea, created_at = now()
				WHERE idempotency_keys.created_at <= now() - make_interval(secs => $2)`, key, idempotency.TTL.Seconds())
			if err != nil || tag.RowsAffected() != 1 {
				t.Fatalf("the claim's takeover = %d rows, %v, want 1", tag.RowsAffected(), err)
			}
			return func() { _ = tx.Commit(ctx) }, func() { _ = tx.Rollback(ctx) }
		},
	}
}

// purge returns the store's retention or fails the test when the port has no
// purge yet: that failure IS the red this file was pushed for.
func (e retentionEnv) purge(t *testing.T) retentionPurger {
	t.Helper()
	p, ok := any(e.store).(retentionPurger)
	if !ok {
		t.Fatalf("ports.IdempotencyStore (%T) has no PurgeExpired: nothing deletes expired idempotency_keys rows", e.store)
	}
	return p
}

// seedKey stores a 2xx key row created age ago (route is always ”).
func (e retentionEnv) seedKey(t *testing.T, key string, age time.Duration) {
	t.Helper()
	e.exec(t, `INSERT INTO idempotency_keys (route, key, request_hash, status, content_type, body, created_at)
		VALUES ('', $1, 'h', 201, 'application/json', '\x7b7d'::bytea, now() - make_interval(secs => $2))`, key, age.Seconds())
}

// ageingHandler answers 201 and counts how often it RAN (executions, not replays).
func ageingHandler(calls *atomic.Int32) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"answer":"fresh"}`))
	})
}

// TestRetentionPurgeDeletesExpiredRowsAndKeepsLiveOnes: a row older than
// idempotency.TTL goes, a row a hour short of it and a fresh one stay; a
// second pass has nothing left.
func TestRetentionPurgeDeletesExpiredRowsAndKeepsLiveOnes(t *testing.T) {
	e := newRetentionEnv(t)
	e.seedKey(t, "expired-a", idempotency.TTL+time.Hour)
	e.seedKey(t, "expired-b", 48*time.Hour)
	e.seedKey(t, "live-a", time.Hour)
	e.seedKey(t, "live-edge", idempotency.TTL-time.Hour)

	purge := e.purge(t)
	n, err := purge.PurgeExpired(context.Background(), 100)
	if err != nil || n != 2 {
		t.Fatalf("purge = %d, %v, want the 2 expired rows deleted", n, err)
	}
	if got := e.keys(t); !slices.Equal(got, []string{"live-a", "live-edge"}) {
		t.Fatalf("rows left = %v, want only the live ones", got)
	}
	if n, err := purge.PurgeExpired(context.Background(), 100); err != nil || n != 0 {
		t.Fatalf("second purge = %d, %v, want 0: a live row is never deleted", n, err)
	}
}

// TestRetentionReplayOfAnExpiredKeyExecutes: the age guard of the read. A key
// whose stored answer outlived the TTL is not answered from the store — the
// request EXECUTES again — and the re-executed answer is live and replays.
func TestRetentionReplayOfAnExpiredKeyExecutes(t *testing.T) {
	e := newRetentionEnv(t)
	var calls atomic.Int32
	h := e.mw(ageingHandler(&calls))

	first := post(t, h, "k-aged", `{"n":1}`)
	if first.Code != http.StatusCreated || replayed(first) || calls.Load() != 1 {
		t.Fatalf("first = %d replayed=%v calls=%d, want a fresh 201", first.Code, replayed(first), calls.Load())
	}
	e.exec(t, `UPDATE idempotency_keys SET created_at = created_at - make_interval(secs => $1)`,
		(idempotency.TTL + time.Hour).Seconds())

	again := post(t, h, "k-aged", `{"n":1}`)
	if again.Code != http.StatusCreated || replayed(again) || calls.Load() != 2 {
		t.Fatalf("a replay of an expired key = %d replayed=%v calls=%d, want a fresh execution (the age guard)", again.Code, replayed(again), calls.Load())
	}
	if n := e.count(t, `SELECT count(*) FROM idempotency_keys WHERE key = 'k-aged' AND created_at > now() - make_interval(secs => $1)`, idempotency.TTL.Seconds()); n != 1 {
		t.Fatalf("live rows for the re-executed key = %d, want 1", n)
	}
	third := post(t, h, "k-aged", `{"n":1}`)
	if third.Code != http.StatusCreated || !replayed(third) || calls.Load() != 2 {
		t.Fatalf("after the takeover = %d replayed=%v calls=%d, want the stored answer replayed", third.Code, replayed(third), calls.Load())
	}
	if n, err := e.purge(t).PurgeExpired(context.Background(), 100); err != nil || n != 0 {
		t.Fatalf("purge = %d, %v, want 0: the re-executed answer is live", n, err)
	}
}

// TestRetentionPurgeNeverDeletesARowARequestHoldsAndNeverWaits: a request
// whose claim took over an expired row holds the row lock until it commits
// (the claim refreshes created_at); the purge must skip the row, not wait for
// the request, and after the commit find the row live.
func TestRetentionPurgeNeverDeletesARowARequestHoldsAndNeverWaits(t *testing.T) {
	e := newRetentionEnv(t)
	e.seedKey(t, "held", 30*time.Hour)
	commit, rollback := e.holds(t, "held")
	defer rollback()

	purgeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan int, 1)
	go func() { n, _ := e.purge(t).PurgeExpired(purgeCtx, 100); done <- n }()
	select {
	case n := <-done:
		if n != 0 {
			t.Fatalf("purge while the request holds the row deleted %d, want 0 (skip, never wait)", n)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the purge waited on the row the request holds: it must SKIP LOCKED")
	}
	commit() // the deferred rollback after the commit is a harmless no-op
	if n, err := e.purge(t).PurgeExpired(context.Background(), 100); err != nil || n != 0 {
		t.Fatalf("purge after the request committed = %d, %v, want 0: the row was refreshed, it is live", n, err)
	}
	if got := e.keys(t); !slices.Equal(got, []string{"held"}) {
		t.Fatalf("rows = %v, want the refreshed key", got)
	}
}

// TestRetentionPurgeRacingRequestsKeepsEveryAnswerCorrect: requests on expired
// keys (every one a FRESH execution) racing concurrent purges: every answer is
// 201, the handler runs once per key, and exactly one live row per key
// remains, which then replays.
func TestRetentionPurgeRacingRequestsKeepsEveryAnswerCorrect(t *testing.T) {
	e := newRetentionEnv(t)
	var calls atomic.Int32
	h := e.mw(ageingHandler(&calls))

	const rounds = 10
	for i := range rounds {
		key := fmt.Sprintf("race-%d", i)
		e.seedKey(t, key, 30*time.Hour)
		var wg sync.WaitGroup
		codes := make([]int, 3)
		for j := range codes {
			wg.Add(1)
			go func() { defer wg.Done(); codes[j] = post(t, h, key, `{"n":1}`).Code }()
		}
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = e.purge(t).PurgeExpired(context.Background(), 100) }()
		wg.Wait()
		for _, c := range codes {
			if c != http.StatusCreated {
				t.Fatalf("round %d: a request racing the purge answered %d, want 201", i, c)
			}
		}
	}
	if calls.Load() != rounds {
		t.Fatalf("the handler ran %d times for %d keys: an expired row must be a fresh request, once, and its duplicates replay", calls.Load(), rounds)
	}
	if left := e.count(t, `SELECT count(*) FROM idempotency_keys`); left != rounds {
		t.Fatalf("%d key rows left, want one live row per key (%d)", left, rounds)
	}
	if again := post(t, h, "race-0", `{"n":1}`); !replayed(again) {
		t.Fatalf("a repeat of a stored key must replay: %v", again.Header())
	}
}
