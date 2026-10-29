package usecases_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/IQVO/pricing/internal/adapters/outbound/memory"
	"github.com/IQVO/pricing/internal/application/usecases"
	"github.com/IQVO/pricing/internal/domain/schedule"
	"github.com/IQVO/pricing/internal/domain/shared"
	"github.com/IQVO/pricing/internal/domain/tax"
)

const cur = "BRL"

var t0 = time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)

func brl(n int64) shared.Money { return shared.Money{AmountMinor: n, Currency: cur} }

// env is a use-case fixture over the in-memory doubles.
type env struct {
	t     *testing.T
	store *memory.Store
	clock *memory.Clock
	deps  usecases.Deps
}

func newEnv(t *testing.T) *env {
	t.Helper()
	clock := memory.NewClock(t0)
	store := memory.NewStore(clock)
	return &env{t: t, store: store, clock: clock, deps: usecases.Deps{
		ItemPrices: store.ItemPrices(), Schedules: store.Schedules(), TaxTables: store.TaxTables(), Quotes: store.Quotes(),
		Catalog: store.Catalog(), Inbox: store.Inbox(), Outbox: store.Outbox(), UoW: store.UnitOfWork(),
		Clock: clock, IDs: &memory.SequentialIDs{}, Currency: cur,
	}}
}

var eventSeq int

// catalogCreated feeds CatalogItemCreated for the items through the consumer.
func (e *env) catalogCreated(ids ...string) {
	e.t.Helper()
	for _, id := range ids {
		eventSeq++
		if err := (usecases.Consumer{Deps: e.deps}).CatalogItemCreated(context.Background(), fmt.Sprintf("evt-%s-%d", id, eventSeq), "t", id); err != nil {
			e.t.Fatalf("CatalogItemCreated(%s) = %v", id, err)
		}
	}
}

// price creates an ItemPrice for a catalogue item the view knows.
func (e *env) price(id string, list int64) {
	e.t.Helper()
	e.catalogCreated(id)
	if _, err := (usecases.CreateItemPrice{Deps: e.deps}).Execute(context.Background(), usecases.CreateItemPriceInput{CatalogItemID: id, ListPrice: brl(list)}); err != nil {
		e.t.Fatalf("CreateItemPrice(%s) = %v", id, err)
	}
}

func (e *env) publishSchedule(version int64) {
	e.t.Helper()
	_, err := (usecases.PublishChargeSchedule{Deps: e.deps}).Execute(context.Background(), usecases.PublishChargeScheduleInput{Version: version, Tiers: fullTiers()})
	if err != nil {
		e.t.Fatalf("PublishChargeSchedule(%d) = %v", version, err)
	}
}

func (e *env) publishTax(version int64) {
	e.t.Helper()
	_, err := (usecases.PublishTaxTable{Deps: e.deps}).Execute(context.Background(), usecases.PublishTaxTableInput{Version: version, Bands: bands()})
	if err != nil {
		e.t.Fatalf("PublishTaxTable(%d) = %v", version, err)
	}
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

func eventNames(e *env) []string {
	var out []string
	for _, ev := range e.store.Events() {
		out = append(out, ev.EventName())
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func isErr(err, target error) bool { return errors.Is(err, target) }
