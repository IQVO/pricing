//go:build integration

package main

import (
	"fmt"
	"net/http"
	"sync"
	"testing"
)

// taxBody is a tax table of the given version (one band over every prefix).
func taxBody(version int) string {
	return fmt.Sprintf(`{"version":%d,"bands":[{"postalPrefixFrom":"000","postalPrefixTo":"999","rateBasisPoints":1000}]}`, version)
}

// currentTaxVersion reads the version of the CURRENT tax table; 0 when none.
func currentTaxVersion(t *testing.T, svc *service) int {
	t.Helper()
	r := svc.call(t, "GET", "/tax-tables/current", "", "")
	if r.status == http.StatusNotFound {
		return 0
	}
	if r.status != http.StatusOK {
		t.Fatalf("current tax table = %d %s", r.status, r.body)
	}
	return int(r.field(t, "version").(float64))
}

// TestIdempotencyKeyContractOverPostgres drives the Idempotency-Key contract
// through the shipped binary: the real router and the Postgres store joined to
// the unit of work (key row, aggregate and outbox rows commit together).
func TestIdempotencyKeyContractOverPostgres(t *testing.T) {
	svc := bootReal(t, "idempotency")
	t.Run("a missing key is 400 idempotency-key-required and runs nothing", func(t *testing.T) {
		r := svc.call(t, "POST", "/tax-tables", "", taxBody(1))
		if r.status != http.StatusBadRequest || r.json(t)["type"] != "/problems/idempotency-key-required" {
			t.Fatalf("no key = %d %s", r.status, r.body)
		}
		if v := currentTaxVersion(t, svc); v != 0 {
			t.Fatalf("a refused request published tax table version %d", v)
		}
	})
	t.Run("fresh key executes, byte-identical replay carries Idempotency-Replayed", func(t *testing.T) {
		first := svc.call(t, "POST", "/tax-tables", "k-fresh", taxBody(1))
		again := svc.call(t, "POST", "/tax-tables", "k-fresh", taxBody(1))
		if first.status != http.StatusCreated || first.header.Get("Idempotency-Replayed") != "" {
			t.Fatalf("first = %d %s (%v)", first.status, first.body, first.header)
		}
		if again.status != http.StatusCreated || again.body != first.body || again.header.Get("Idempotency-Replayed") != "true" {
			t.Fatalf("replay = %d %s (%v), first = %s", again.status, again.body, again.header, first.body)
		}
	})
	t.Run("the same key with another body or on another route is 409 idempotency-key-reused", func(t *testing.T) {
		for _, c := range []struct{ path, body string }{
			{"/tax-tables", taxBody(2)},
			{"/delivery-charge-schedules", `{"version":1,"tiers":[]}`},
		} {
			r := svc.call(t, "POST", c.path, "k-fresh", c.body)
			if r.status != http.StatusConflict || r.json(t)["type"] != "/problems/idempotency-key-reused" {
				t.Fatalf("reuse on %s = %d %s", c.path, r.status, r.body)
			}
		}
		if v := currentTaxVersion(t, svc); v != 1 {
			t.Fatalf("a reused key changed the current tax table to version %d", v)
		}
	})
	t.Run("a key of more than 128 characters is 400 idempotency-key-invalid", func(t *testing.T) {
		long := make([]byte, 129)
		for i := range long {
			long[i] = 'k'
		}
		r := svc.call(t, "POST", "/tax-tables", string(long), taxBody(2))
		if r.status != http.StatusBadRequest || r.json(t)["type"] != "/problems/idempotency-key-invalid" {
			t.Fatalf("long key = %d %s", r.status, r.body)
		}
	})
	t.Run("a 4xx answer is cached and replayed byte for byte", func(t *testing.T) {
		// Version 1 is current: publishing it again is the business 409 non-increasing-version.
		first := svc.call(t, "POST", "/tax-tables", "k-4xx", taxBody(1))
		again := svc.call(t, "POST", "/tax-tables", "k-4xx", taxBody(1))
		if first.status != http.StatusConflict || again.status != http.StatusConflict || again.body != first.body ||
			first.header.Get("Idempotency-Replayed") != "" || again.header.Get("Idempotency-Replayed") != "true" {
			t.Fatalf("first = %d %s (%v), again = %d %s (%v)", first.status, first.body, first.header, again.status, again.body, again.header)
		}
		if first.json(t)["type"] != "/problems/non-increasing-version" {
			t.Fatalf("the business conflict keeps its own problem type: %s", first.body)
		}
	})
	t.Run("concurrent requests with one key publish exactly one tax table", func(t *testing.T) { concurrentSameKey(t, svc) })
}

// concurrentSameKey sends n identical keyed publishes at once: exactly one runs,
// the others replay its answer.
func concurrentSameKey(t *testing.T, svc *service) {
	t.Helper()
	const n = 16
	before := currentTaxVersion(t, svc)
	body := taxBody(before + 1)
	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		fresh  int
		tables = map[string]bool{}
	)
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := svc.call(t, "POST", "/tax-tables", "k-race", body)
			mu.Lock()
			defer mu.Unlock()
			if r.status != http.StatusCreated {
				t.Errorf("concurrent publish = %d %s", r.status, r.body)
				return
			}
			if r.header.Get("Idempotency-Replayed") == "" {
				fresh++
			}
			tables[r.json(t)["taxTableId"].(string)] = true
		}()
	}
	wg.Wait()
	if fresh != 1 || len(tables) != 1 {
		t.Fatalf("%d executions and %d distinct tax tables from %d identical requests, want 1 and 1", fresh, len(tables), n)
	}
	if got := currentTaxVersion(t, svc); got != before+1 {
		t.Fatalf("current version = %d, want %d", got, before+1)
	}
}
