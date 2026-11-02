package repocontract

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/IQVO/pricing/internal/application/idempotency"
	"github.com/IQVO/pricing/internal/domain/itemprice"
)

// idemDo is Do with the Env clock as the record stamp.
func idemDo(e Env, key, hash string, fn func(ctx context.Context) (idempotency.Record, error)) (idempotency.Record, bool, error) {
	return e.Idempotency.Do(ctx(), key, hash, e.Clock.Now().UTC(), fn)
}

// answerAfterSaving returns an fn that saves an item price through the ctx it is
// handed (so the write joins the key's transaction) and answers status.
func answerAfterSaving(t *testing.T, id string, e Env, status int, calls *atomic.Int32) func(context.Context) (idempotency.Record, error) {
	t.Helper()
	return func(c context.Context) (idempotency.Record, error) {
		calls.Add(1)
		fatalIf(t, e.ItemPrices.Save(c, newPrice(t, id, 1000)), "save in the key's unit")
		fatalIf(t, e.Outbox.Append(c, priceEvents(t, id)...), "append in the key's unit")
		return idempotency.Record{Status: status, ContentType: "application/json", Body: []byte(`{"a":1}`)}, nil
	}
}

func priceExists(e Env, id string) bool {
	_, err := e.ItemPrices.Get(ctx(), itemprice.ID(id))
	return err == nil
}

func testIdempotency(t *testing.T, e Env) {
	t.Run("the first call runs fn and commits it with the record, a repeat replays", func(t *testing.T) { idemReplay(t, e) })
	t.Run("a 4xx answer is stored", func(t *testing.T) { idemClientError(t, e) })
	t.Run("a 5xx, an error and a panic store nothing and undo the writes", func(t *testing.T) { idemRollback(t, e) })
	t.Run("an expired record is taken over", func(t *testing.T) { idemExpired(t, e) })
	t.Run("concurrent calls with one key run fn once", func(t *testing.T) { idemConcurrent(t, e) })
}

func idemReplay(t *testing.T, e Env) {
	var calls atomic.Int32
	outboxBefore := e.OutboxCount(t)
	rec, replayed, err := idemDo(e, "k1", "h1", answerAfterSaving(t, "sku-i1", e, 201, &calls))
	fatalIf(t, err, "first call")
	if replayed || calls.Load() != 1 || rec.Status != 201 || !priceExists(e, "sku-i1") {
		t.Fatalf("first call: replayed=%v calls=%d rec=%+v price=%v", replayed, calls.Load(), rec, priceExists(e, "sku-i1"))
	}
	if n := e.OutboxCount(t); n != outboxBefore+1 {
		t.Fatalf("the outbox row must commit with the key: %d rows, want %d", n, outboxBefore+1)
	}
	// The key is global (REST guideline 2.3): a repeat, even with another fingerprint (another
	// method, path or body), finds the stored record untouched; the middleware compares hashes and
	// turns a mismatch into 409 idempotency-key-reused.
	for _, hash := range []string{"h1", "h2"} {
		got, replayed, err := idemDo(e, "k1", hash, answerAfterSaving(t, "sku-i1b", e, 500, &calls))
		fatalIf(t, err, "repeat")
		if !replayed || calls.Load() != 1 || got.RequestHash != "h1" || got.Status != 201 || got.ContentType != "application/json" || string(got.Body) != `{"a":1}` {
			t.Fatalf("repeat with %s: replayed=%v calls=%d rec=%+v", hash, replayed, calls.Load(), got)
		}
	}
	if priceExists(e, "sku-i1b") {
		t.Fatal("a replay must not run fn")
	}
	if n := e.OutboxCount(t); n != outboxBefore+1 {
		t.Fatalf("a replay must not append: %d rows, want %d", n, outboxBefore+1)
	}
}

func idemClientError(t *testing.T, e Env) {
	var calls atomic.Int32
	rec, replayed, err := idemDo(e, "k-4xx", "h", answerAfterSaving(t, "sku-i4", e, 422, &calls))
	fatalIf(t, err, "first call")
	if replayed || rec.Status != 422 {
		t.Fatalf("first call: replayed=%v rec=%+v", replayed, rec)
	}
	got, replayed, err := idemDo(e, "k-4xx", "h", answerAfterSaving(t, "sku-i4b", e, 201, &calls))
	fatalIf(t, err, "repeat")
	if !replayed || got.Status != 422 || calls.Load() != 1 {
		t.Fatalf("a 4xx is replayed: replayed=%v rec=%+v calls=%d", replayed, got, calls.Load())
	}
}

func idemRollback(t *testing.T, e Env) {
	var calls atomic.Int32
	outboxBefore := e.OutboxCount(t)
	failures := map[string]func(c context.Context) (idempotency.Record, error){
		"5xx": answerAfterSaving(t, "sku-i5", e, 503, &calls),
		"error": func(c context.Context) (idempotency.Record, error) {
			calls.Add(1)
			fatalIf(t, e.ItemPrices.Save(c, newPrice(t, "sku-ie", 1000)), "save")
			return idempotency.Record{}, errBoom
		},
		"panic": func(c context.Context) (idempotency.Record, error) {
			calls.Add(1)
			fatalIf(t, e.ItemPrices.Save(c, newPrice(t, "sku-ip", 1000)), "save")
			panic("boom")
		},
	}
	for name, id := range map[string]string{"5xx": "sku-i5", "error": "sku-ie", "panic": "sku-ip"} {
		key := "k-" + name
		func() {
			defer func() { _ = recover() }()
			_, replayed, _ := idemDo(e, key, "h", failures[name])
			if replayed {
				t.Errorf("%s: nothing was stored yet", name)
			}
		}()
		if priceExists(e, id) {
			t.Fatalf("%s: the writes of fn must be undone", name)
		}
		if n := e.OutboxCount(t); n != outboxBefore {
			t.Fatalf("%s: the outbox rows of fn must be undone: %d rows, want %d", name, n, outboxBefore)
		}
		before := calls.Load()
		_, replayed, _ := idemDo(e, key, "h", answerAfterSaving(t, "sku-ir-"+name, e, 201, &calls))
		if replayed || calls.Load() != before+1 {
			t.Fatalf("%s: the retry must run fn again (replayed=%v)", name, replayed)
		}
		outboxBefore = e.OutboxCount(t)
	}
}

func idemExpired(t *testing.T, e Env) {
	var calls atomic.Int32
	old := e.Clock.Now().UTC().Add(-idempotency.TTL - time.Minute)
	_, _, err := e.Idempotency.Do(ctx(), "k-old", "old", old, answerAfterSaving(t, "sku-io", e, 201, &calls))
	fatalIf(t, err, "store an old record")
	rec, replayed, err := idemDo(e, "k-old", "new", answerAfterSaving(t, "sku-in", e, 200, &calls))
	fatalIf(t, err, "take over the expired record")
	if replayed || rec.Status != 200 || calls.Load() != 2 {
		t.Fatalf("an expired record is replaceable: replayed=%v rec=%+v calls=%d", replayed, rec, calls.Load())
	}
	got, replayed, err := idemDo(e, "k-old", "new", answerAfterSaving(t, "sku-ix", e, 201, &calls))
	if err != nil || !replayed || got.RequestHash != "new" {
		t.Fatalf("the replacement is live: replayed=%v rec=%+v err=%v", replayed, got, err)
	}
}

func idemConcurrent(t *testing.T, e Env) {
	const callers = 8
	var (
		calls    atomic.Int32
		wg       sync.WaitGroup
		mu       sync.Mutex
		replays  int
		failures []string
	)
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec, replayed, err := e.Idempotency.Do(ctx(), "k-race", "h", e.Clock.Now().UTC(),
				func(c context.Context) (idempotency.Record, error) {
					calls.Add(1)
					time.Sleep(50 * time.Millisecond) // let the others queue behind the claim
					if err := e.ItemPrices.Save(c, newPrice(t, fmt.Sprintf("sku-race-%d", i), 1000)); err != nil {
						return idempotency.Record{}, err
					}
					return idempotency.Record{Status: 201, Body: []byte(fmt.Sprint(i))}, nil
				})
			mu.Lock()
			defer mu.Unlock()
			if err != nil || rec.Status != 201 {
				failures = append(failures, fmt.Sprintf("caller %d: %+v %v", i, rec, err))
			}
			if replayed {
				replays++
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 || replays != callers-1 || len(failures) != 0 {
		t.Fatalf("fn ran %d times, %d replays of %d, failures %v", calls.Load(), replays, callers, failures)
	}
	n := 0
	for i := range callers {
		if priceExists(e, fmt.Sprintf("sku-race-%d", i)) {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d item prices were created, want exactly one", n)
	}
}
