package repocontract

import (
	"context"
	"testing"
	"time"

	"github.com/IQVO/pricing/internal/application/idempotency"
)

// jsonType is the content type of the records the suite stores.
const jsonType = "application/json"

// RunIdempotencyPurge is the retention contract of PurgeExpired (the replay
// contract of Do lives in Run's idempotency suite): every case runs over a
// FRESH Env of its own, because each case seeds and ages its own rows. The
// memory double and the Postgres store both run it. Age (Env.Age) makes every
// answer stored so far d older — the memory double advances its clock,
// Postgres moves created_at back — the only way a test reaches an expired row
// without waiting a day.
func RunIdempotencyPurge(t *testing.T, newEnv func(t *testing.T) Env) {
	t.Helper()
	cases := []struct {
		name string
		run  func(*testing.T, Env)
	}{
		{"deletes expired keeps live", purgeKeepsLive},
		{"one batch per pass", purgeBatches},
		{"oldest first", purgeOldestFirst},
		{"a request holding the key", purgeHeldKey},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			if e.Age == nil {
				t.Fatal("Env.Age is not wired: the purge suite cannot age records")
			}
			c.run(t, e)
		})
	}
}

// purgeKeepsLive: a row older than the TTL goes, a row a minute short of it
// and a fresh one stay and replay; a second pass has nothing left to delete.
func purgeKeepsLive(t *testing.T, e Env) {
	idemPut(t, e, "old", "v-old")
	e.Age(t, idempotency.TTL+time.Minute)
	idemPut(t, e, "edge", "v-edge")
	e.Age(t, idempotency.TTL-time.Minute)
	idemPut(t, e, "live", "v-live")
	if n, err := e.Idempotency.PurgeExpired(ctx(), 100); err != nil || n != 1 {
		t.Fatalf("purge = %d %v, want 1 (only the expired row)", n, err)
	}
	idemWantReplay(t, e, "edge", "v-edge")
	idemWantReplay(t, e, "live", "v-live")
	if n, err := e.Idempotency.PurgeExpired(ctx(), 100); err != nil || n != 0 {
		t.Fatalf("second purge = %d %v, want 0", n, err)
	}
}

// purgeBatches: one pass deletes at most limit rows, the next ones finish.
func purgeBatches(t *testing.T, e Env) {
	for _, k := range []string{"a", "b", "c", "d", "e"} {
		idemPut(t, e, k, k)
	}
	e.Age(t, idempotency.TTL+time.Hour)
	for _, want := range []int{2, 2, 1, 0} {
		if n, err := e.Idempotency.PurgeExpired(ctx(), 2); err != nil || n != want {
			t.Fatalf("purge = %d %v, want %d", n, err, want)
		}
	}
}

// purgeOldestFirst: with two expired rows and a limit of one, the older goes.
// Ageing everything back by more than both ages makes the survivor live again,
// which is what tells the two apart (an expired row is never replayed).
func purgeOldestFirst(t *testing.T, e Env) {
	idemPut(t, e, "older", "v-older")
	e.Age(t, time.Hour)
	idemPut(t, e, "newer", "v-newer")
	e.Age(t, idempotency.TTL)
	if n, err := e.Idempotency.PurgeExpired(ctx(), 1); err != nil || n != 1 {
		t.Fatalf("purge = %d %v, want 1", n, err)
	}
	e.Age(t, -(idempotency.TTL + time.Hour)) // both would be live again were they still stored
	idemWantReplay(t, e, "newer", "v-newer")
}

// purgeHeldKey: a purge that overlaps a request holding the (expired) key
// neither fails nor breaks it: the request answers fresh, its row is stored
// and replays afterwards. (Postgres additionally never blocks the purge on
// that request: the SKIP LOCKED integration test.)
func purgeHeldKey(t *testing.T, e Env) {
	idemPut(t, e, "held", "stale")
	e.Age(t, idempotency.TTL+time.Hour)
	inFlight, release := make(chan struct{}), make(chan struct{})
	type result struct {
		rec      idempotency.Record
		replayed bool
		err      error
	}
	done := make(chan result, 1)
	go func() {
		rec, replayed, err := e.Idempotency.Do(ctx(), "held", "h", e.Clock.Now().UTC(),
			func(context.Context) (idempotency.Record, error) {
				close(inFlight)
				<-release
				return idempotency.Record{Status: 201, ContentType: jsonType, Body: []byte("fresh")}, nil
			})
		done <- result{rec, replayed, err}
	}()
	<-inFlight
	purged := make(chan error, 1)
	go func() { _, err := e.Idempotency.PurgeExpired(ctx(), 100); purged <- err }()
	time.Sleep(20 * time.Millisecond) // let the purge overlap the request (the double's purge waits for it)
	close(release)
	if err := <-purged; err != nil {
		t.Fatalf("purge during an in-flight request: %v", err)
	}
	if r := <-done; r.err != nil || r.replayed || string(r.rec.Body) != "fresh" {
		t.Fatalf("the request holding the key = %+v replayed %v err %v, want a fresh answer", r.rec, r.replayed, r.err)
	}
	idemWantReplay(t, e, "held", "fresh")
}

// idemPut stores body under key through Do (a fresh execution).
func idemPut(t *testing.T, e Env, key, body string) {
	t.Helper()
	rec, replayed, err := e.Idempotency.Do(ctx(), key, "h", e.Clock.Now().UTC(),
		func(context.Context) (idempotency.Record, error) {
			return idempotency.Record{Status: 201, ContentType: jsonType, Body: []byte(body)}, nil
		})
	if err != nil || replayed || string(rec.Body) != body {
		t.Fatalf("store %s: replayed %v err %v rec %+v", key, replayed, err, rec)
	}
}

// idemWantReplay asserts key replays its stored body (fn must not run).
func idemWantReplay(t *testing.T, e Env, key, body string) {
	t.Helper()
	rec, replayed, err := e.Idempotency.Do(ctx(), key, "h", e.Clock.Now().UTC(),
		func(context.Context) (idempotency.Record, error) {
			return idempotency.Record{Status: 200, ContentType: jsonType, Body: []byte("ran")}, nil
		})
	if err != nil || !replayed || string(rec.Body) != body {
		t.Fatalf("%s = %+v replayed %v err %v, want the stored %q replayed", key, rec, replayed, err, body)
	}
}
