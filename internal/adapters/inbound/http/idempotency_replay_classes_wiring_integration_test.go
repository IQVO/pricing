//go:build integration

// Rendered from warehouse-harness-template templates/idempotency/replay_classes_wiring_test.go.tmpl
// (docs/idempotency-replay-classes.md): pricing's half of the replay-class tests. The key row and
// the handler's writes live in ONE Postgres transaction (the claim, the savepoint and the stored
// answer of postgres.IdempotencyStore), so the wiring is the real thing: the production store over
// a testcontainers pool, a write through the transaction the middleware bound into the request
// context, counts from a different connection. ADR 0011. The pool comes from internal/testing/pgtest
// by type inference, so this file names no driver package (the testcontainers sensor stays happy).
package http_test

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	httpadapter "github.com/IQVO/pricing/internal/adapters/inbound/http"
	"github.com/IQVO/pricing/internal/adapters/outbound/clock"
	"github.com/IQVO/pricing/internal/adapters/outbound/postgres"
	"github.com/IQVO/pricing/internal/adapters/outbound/postgres/pgtx"
	"github.com/IQVO/pricing/internal/application/ports"
	"github.com/IQVO/pricing/internal/testing/pgtest"
)

// fixedClock is gone: the claim's expiry compares created_at with the database
// server's now(), so the wiring clock is the system clock (clock.System{}).

// newReplayOps returns the six operations over ONE clean database per call.
func newReplayOps(t *testing.T) replayOps {
	t.Helper()
	pool := pgtest.NewPool(t)
	store := postgres.NewIdempotencyStore(pool)
	// The claim's expiry (created_at vs the database's now()) is checked by the
	// server, so the clock must walk with real time: a clock frozen in the past
	// makes every stored row look expired and the next claim takes it over.
	var clk ports.Clock = clock.System{}
	if _, err := pool.Exec(context.Background(), `CREATE TABLE replay_probe (tag text NOT NULL PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	errs := map[string]error{}
	for _, row := range httpadapter.Catalogue() {
		errs[row.Slug] = row.Err
	}
	count := func(t *testing.T, query string, args ...any) int {
		t.Helper()
		var n int
		if err := pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	return replayOps{
		Catalogue: func() []problemEntry {
			rows := make([]problemEntry, 0, len(httpadapter.Catalogue()))
			for _, row := range httpadapter.Catalogue() {
				rows = append(rows, problemEntry{Slug: row.Slug, Status: row.Status, Class: row.Class})
			}
			return rows
		},
		Middleware: func(_ *testing.T, next http.Handler) http.Handler {
			return httpadapter.RequireIdempotencyKey(store, clk)(next)
		},
		Refuse: func(w http.ResponseWriter, r *http.Request, e problemEntry) {
			httpadapter.WriteError(w, r, errs[e.Slug])
		},
		Write: func(ctx context.Context, tag string) error {
			tx, ok := pgtx.From(ctx)
			if !ok {
				return context.Canceled // not inside the middleware's transaction
			}
			_, err := tx.Exec(ctx, `INSERT INTO replay_probe (tag) VALUES ($1)`, tag)
			return err
		},
		Committed: func(t *testing.T, tag string) int {
			t.Helper()
			return count(t, `SELECT count(*) FROM replay_probe WHERE tag = $1`, tag)
		},
		KeyRows: func(t *testing.T, key string) int {
			t.Helper()
			return count(t, `SELECT count(*) FROM idempotency_keys WHERE key = $1`, key)
		},
	}
}

// An error answer whose problem type declares no class (a handler that wrote a
// bare 4xx) is not remembered: the fail-safe direction is to run the work again.
func TestAnUnclassifiedErrorAnswerIsNotStored(t *testing.T) {
	a := newReplayAdapter(t)
	bare := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusConflict)
	}
	h := &fakeHandler{script: []answerFn{bare, succeeding(a, "ok")}}
	mw := a.Middleware(t, h)

	if first := post(t, mw, "k-bare", `{"n":1}`); first.Code != http.StatusConflict {
		t.Fatalf("first = %d, want the 409", first.Code)
	}
	if got := a.KeyRows(t, "k-bare"); got != 0 {
		t.Fatalf("key rows after an unclassified 409 = %d, want 0", got)
	}
	if retry := post(t, mw, "k-bare", `{"n":1}`); retry.Code != http.StatusCreated || h.calls != 2 || replayed(retry) {
		t.Fatalf("retry = %d replayed=%v calls=%d, want a fresh 201 after 2 calls", retry.Code, replayed(retry), h.calls)
	}
}

// A copy of a request that waits on the key while the first ends RETRYABLE
// blocks on the unique index until the first transaction rolls back; the row
// never existed, so the copy claims the key and runs the handler itself, and
// the copies behind it replay that run's answer.
func TestACopyWaitingOnARetryableFirstRunsItself(t *testing.T) {
	a := newReplayAdapter(t)
	var calls, active, peak atomic.Int32
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		cur := active.Add(1)
		for p := peak.Load(); cur > p && !peak.CompareAndSwap(p, cur); p = peak.Load() {
		}
		time.Sleep(20 * time.Millisecond) // let the copies queue behind the key
		active.Add(-1)
		if n == 1 {
			httpadapter.WriteError(w, r, ports.ErrConcurrentUpdate)
			return
		}
		succeeding(a, "ok")(w, r)
	})
	mw := a.Middleware(t, h)

	const copies = 12
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		fresh   = map[int]int{}
		replays int
	)
	for range copies {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := post(t, mw, "k-wait", `{"n":1}`)
			mu.Lock()
			defer mu.Unlock()
			if replayed(rec) {
				replays++
				if rec.Code != http.StatusCreated {
					t.Errorf("a replay answered %d: only the stored success may be replayed", rec.Code)
				}
				return
			}
			fresh[rec.Code]++
		}()
	}
	wg.Wait()
	if calls.Load() != 2 || peak.Load() != 1 {
		t.Fatalf("handler ran %d times with %d at once, want 2 and 1", calls.Load(), peak.Load())
	}
	if fresh[http.StatusConflict] != 1 || fresh[http.StatusCreated] != 1 || replays != copies-2 {
		t.Fatalf("fresh answers %v and %d replays, want one 409, one 201 and %d replays", fresh, replays, copies-2)
	}
	if got := a.KeyRows(t, "k-wait"); got != 1 {
		t.Fatalf("key rows = %d, want 1: the success is stored", got)
	}
}
