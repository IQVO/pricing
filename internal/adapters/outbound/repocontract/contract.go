// Package repocontract is the repository contract suite: the behaviours every
// persistence adapter (the in-memory doubles and Postgres) must show. It is
// test support (it imports "testing") kept out of _test.go files so both
// adapters can run it; the shipped binary never links it (enforced by
// internal/architecture).
package repocontract

import (
	"context"
	"testing"
	"time"

	"github.com/IQVO/pricing/internal/application/ports"
	"github.com/IQVO/pricing/internal/domain/itemprice"
	"github.com/IQVO/pricing/internal/domain/schedule"
	"github.com/IQVO/pricing/internal/domain/shared"
	"github.com/IQVO/pricing/internal/domain/tax"
)

// Env is what an adapter hands to the suite. Every test gets a fresh Env.
type Env struct {
	ItemPrices  ports.ItemPriceRepository
	Schedules   ports.ScheduleRepository
	TaxTables   ports.TaxTableRepository
	Quotes      ports.QuoteRepository
	Catalog     ports.CatalogView
	Inbox       ports.Inbox
	Outbox      ports.EventOutbox
	UoW         ports.UnitOfWork
	Idempotency ports.IdempotencyStore
	// Clock is the store's time authority: testIdempotency stamps records
	// with it, so the expired-record case expires relative to the SAME clock
	// the implementation under test judges TTL with (the memory double is
	// driven by a frozen clock; the Postgres store by the database's now()).
	Clock ports.Clock
	// OutboxCount returns how many events the outbox holds.
	OutboxCount func(t *testing.T) int
}

// Run executes the whole suite; newEnv must return an empty Env each time.
func Run(t *testing.T, newEnv func(t *testing.T) Env) {
	t.Helper()
	suites := map[string]func(*testing.T, Env){
		"itemprices":  testItemPrices,
		"boundaries":  testBoundaries,
		"schedules":   testSchedules,
		"taxtables":   testTaxTables,
		"quotes":      testQuotes,
		"expiry":      testQuoteExpiry,
		"catalog":     testCatalog,
		"inbox":       testInbox,
		"unitofwork":  testUnitOfWork,
		"idempotency": testIdempotency,
	}
	for name, fn := range suites {
		t.Run(name, func(t *testing.T) { fn(t, newEnv(t)) })
	}
}

const cur = "BRL"

var t0 = time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)

// at returns t0 plus m minutes.
func at(m int) time.Time { return t0.Add(time.Duration(m) * time.Minute) }

func brl(n int64) shared.Money { return shared.Money{AmountMinor: n, Currency: cur} }

func ctx() context.Context { return context.Background() }

func fatalIf(t *testing.T, err error, what string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
}

func inputs(now time.Time) itemprice.Inputs {
	return itemprice.Inputs{Currency: cur, Catalog: itemprice.CatalogActive, Now: now}
}

// newPrice builds an unsaved item price.
func newPrice(t *testing.T, id string, list int64) *itemprice.ItemPrice {
	t.Helper()
	p, _, err := itemprice.Create(itemprice.ID(id), brl(list), inputs(t0))
	fatalIf(t, err, "create item price "+id)
	return p
}

// priceEvents returns the events a new item price raises.
func priceEvents(t *testing.T, id string) []shared.Event {
	t.Helper()
	_, evs, err := itemprice.Create(itemprice.ID(id), brl(1000), inputs(t0))
	fatalIf(t, err, "create item price "+id)
	return evs
}

// savedPrice builds and saves an item price.
func savedPrice(t *testing.T, e Env, id string, list int64) *itemprice.ItemPrice {
	t.Helper()
	p := newPrice(t, id, list)
	fatalIf(t, e.ItemPrices.Save(ctx(), p), "save item price "+id)
	return p
}

func fullTiers() []schedule.TierCharge {
	return []schedule.TierCharge{
		{Tier: shared.TierSameDay, Charge: brl(2990)},
		{Tier: shared.TierNextDay, Charge: brl(1490), FreeOver: brl(30000)},
		{Tier: shared.TierTwoDay, Charge: brl(990)},
		{Tier: shared.TierStandard, Charge: brl(500), FreeOver: brl(15000)},
	}
}

func bands() []tax.TaxBand {
	return []tax.TaxBand{
		{PostalPrefixFrom: "000", PostalPrefixTo: "499", RateBasisPoints: 1000},
		{PostalPrefixFrom: "500", PostalPrefixTo: "999", RateBasisPoints: 250},
	}
}
