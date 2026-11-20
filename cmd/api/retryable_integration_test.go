//go:build integration

package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/IQVO/pricing/internal/testing/kafkatest"
	"github.com/IQVO/pricing/internal/testing/pgtest"
)

// The replay classes of ADR 0011 over the REAL router, the REAL binary and REAL
// Postgres (testcontainers): a RETRYABLE refusal rolls the request back, key row
// included, so the same key retries the work and is then stored like any success.
// The rig shares the service's database through pgtest.Pool (no driver import
// here: the pool's type is inferred, keeping the testcontainers sensor green).

type retryRig struct {
	svc  *service
	url  string
	rows func(t *testing.T, query string, args ...any) int
	exec func(t *testing.T, sql string)
}

func newRetryRig(t *testing.T, group string) *retryRig {
	t.Helper()
	kafkatest.CreateTopic(t, topicEvents)
	kafkatest.CreateTopic(t, topicAnalytics)
	kafkatest.CreateTopic(t, topicAvailability)
	url := pgtest.URL(t)
	svc := boot(t, map[string]string{
		"DATABASE_URL": url, "OFFER_AVAILABILITY_CONSUMER_GROUP": "pricing.e2e." + group,
		"OUTBOX_RELAY_INTERVAL": "100ms", "POLICY_INTERVAL": "200ms", "SHUTDOWN_DRAIN_DELAY": "0",
		"KAFKA_BROKERS": strings.Join(kafkatest.Brokers(t), ","),
	})
	pool := pgtest.Pool(t, url)
	ctx := context.Background()
	r := &retryRig{
		svc: svc, url: url,
		rows: func(t *testing.T, query string, args ...any) int {
			t.Helper()
			var n int
			if err := pool.QueryRow(ctx, query, args...).Scan(&n); err != nil {
				t.Fatal(err)
			}
			return n
		},
		exec: func(t *testing.T, sql string) {
			t.Helper()
			if _, err := pool.Exec(ctx, sql); err != nil {
				t.Fatalf("%s: %v", sql, err)
			}
		},
	}
	return r
}

func (r *retryRig) keyRows(t *testing.T, key string) int {
	t.Helper()
	return r.rows(t, `SELECT count(*) FROM idempotency_keys WHERE key = $1`, key)
}

// concurrent-update is RETRYABLE. The version-guarded UPDATE of an item price
// cannot be raced by timing here, so a trigger makes it match no row — exactly
// what the guard sees when another writer got between the load and the save, on
// both attempts of the use case (commandAttempts). The refused request must
// leave no key row; the same key then runs again, is stored and is replayed.
func TestAConcurrentUpdateRefusalIsNotStoredSoItsKeyRetries(t *testing.T) {
	r := newRetryRig(t, "retry-cu")
	kafkatest.Produce(t, topicAvailability, availabilityEvent("evt-retry-cu", typeCreated, "sku-cu"))
	priceOnceKnown(t, r.svc, "sku-cu", "k-create-cu")
	sale := fmt.Sprintf(`{"price":%s,"from":"2027-03-01T00:00:00Z","to":"2027-04-01T00:00:00Z"}`, money(500))

	r.exec(t, `CREATE FUNCTION pr_drop_update() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RETURN NULL; END $$`)
	r.exec(t, `CREATE TRIGGER pr_drop_update BEFORE UPDATE ON item_prices FOR EACH ROW EXECUTE FUNCTION pr_drop_update()`)
	refused := r.svc.call(t, "POST", "/item-prices/sku-cu/sales", "sale-cu", sale)
	if refused.status != http.StatusConflict || refused.json(t)["type"] != "/problems/concurrent-update" {
		t.Fatalf("the guarded update = %d %s, want 409 concurrent-update", refused.status, refused.body)
	}
	if n := r.keyRows(t, "sale-cu"); n != 0 {
		t.Fatalf("the refused request left %d row(s) under its key, want 0: a concurrent-update refusal is RETRYABLE and must release the key", n)
	}

	r.exec(t, `DROP TRIGGER pr_drop_update ON item_prices`)
	r.exec(t, `DROP FUNCTION pr_drop_update()`)
	retry := r.svc.call(t, "POST", "/item-prices/sku-cu/sales", "sale-cu", sale)
	if retry.status != http.StatusCreated || retry.header.Get("Idempotency-Replayed") != "" {
		t.Fatalf("the retry under the released key = %d %v, want a fresh 201", retry.status, retry.header)
	}
	if n := r.keyRows(t, "sale-cu"); n != 1 {
		t.Fatalf("%d row(s) under the key after the retry succeeded, want 1 (a 2xx is stored)", n)
	}
	again := r.svc.call(t, "POST", "/item-prices/sku-cu/sales", "sale-cu", sale)
	if again.status != http.StatusCreated || again.body != retry.body || again.header.Get("Idempotency-Replayed") != "true" {
		t.Fatalf("third call = %d %v, want the stored 201 replayed", again.status, again.header)
	}
	if n := r.rows(t, `SELECT count(*) FROM sale_windows WHERE catalog_item_id = 'sku-cu'`); n != 1 {
		t.Fatalf("%d sale window(s) for sku-cu, want 1: the replay must not schedule another", n)
	}
}

// no-tax-table is RETRYABLE: a CURRENT tax table is published by another
// request, so a quote refused now can be quotable a moment later under the same
// key (the refusal of quote.Issue's table == nil check, which precedes the line
// pricing, so no catalogue item is needed).
func TestANoTaxTableRefusalIsNotStoredSoItsKeyRetries(t *testing.T) {
	r := newRetryRig(t, "retry-tax")
	tiers := `[{"tier":"SAME_DAY","charge":%[1]s},{"tier":"NEXT_DAY","charge":%[1]s},{"tier":"TWO_DAY","charge":%[1]s},{"tier":"STANDARD","charge":%[1]s}]`
	if resp := r.svc.call(t, "POST", "/delivery-charge-schedules", "k-sched-tt", fmt.Sprintf(`{"version":1,"tiers":`+tiers+`}`, money(500))); resp.status != http.StatusCreated {
		t.Fatalf("schedule: %d %s", resp.status, resp.body)
	}
	const key = "quote-tt"
	quote := `{"cartId":"cart-tt","destinationPostalPrefix":"787","lines":[{"catalogItemId":"sku-tt","quantity":1}],"groups":[{"groupNo":1,"offerId":"off-tt","tier":"STANDARD","lineNos":[1]}]}`

	refused := r.svc.call(t, "POST", "/price-quotes", key, quote)
	if refused.status != http.StatusUnprocessableEntity || refused.json(t)["type"] != "/problems/no-tax-table" {
		t.Fatalf("the quote without a tax table = %d %s, want 422 no-tax-table", refused.status, refused.body)
	}
	if n := r.keyRows(t, key); n != 0 {
		t.Fatalf("the refused quote left %d row(s) under its key, want 0: no-tax-table is RETRYABLE and must release the key", n)
	}

	if resp := r.svc.call(t, "POST", "/tax-tables", "k-tax-tt", taxBody(1)); resp.status != http.StatusCreated {
		t.Fatalf("tax table: %d %s", resp.status, resp.body)
	}
	retry := r.svc.call(t, "POST", "/price-quotes", key, quote)
	if retry.status != http.StatusCreated || retry.header.Get("Idempotency-Replayed") != "" {
		t.Fatalf("the retry under the released key = %d %v, want a fresh 201", retry.status, retry.header)
	}
	if n := r.keyRows(t, key); n != 1 {
		t.Fatalf("%d row(s) under the key after the retry succeeded, want 1", n)
	}
	again := r.svc.call(t, "POST", "/price-quotes", key, quote)
	if again.status != http.StatusCreated || again.body != retry.body || again.header.Get("Idempotency-Replayed") != "true" {
		t.Fatalf("third call = %d %v, want the stored 201 replayed", again.status, again.header)
	}
	if n := r.rows(t, `SELECT count(*) FROM price_quotes`); n != 1 {
		t.Fatalf("%d quote(s) stored, want 1: the replay must not quote another", n)
	}
}
