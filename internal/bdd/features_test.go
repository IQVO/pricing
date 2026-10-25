package bdd

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cucumber/godog"

	httpadapter "github.com/IQVO/pricing/internal/adapters/inbound/http"
	"github.com/IQVO/pricing/internal/adapters/outbound/memory"
	"github.com/IQVO/pricing/internal/application/usecases"
)

var t0 = time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)

// TestFeatures runs the Gherkin scenarios of ../../features through the REST
// adapter wired to the use cases over the in-memory doubles.
func TestFeatures(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: initializeScenario,
		Options:             &godog.Options{Format: "pretty", Paths: []string{"../../features"}, TestingT: t, Strict: true},
	}
	if suite.Run() != 0 {
		t.Fatal("non-zero status returned, failed to run feature tests")
	}
}

// world is the state of one scenario.
type world struct {
	store *memory.Store
	clock *memory.Clock
	deps  usecases.Deps
	srv   *httptest.Server
	seq   int

	status int
	header map[string][]string
	body   string
	// saved holds values captured from responses, by name.
	saved map[string]string
}

func newWorld() *world {
	clock := memory.NewClock(t0)
	s := memory.NewStore(clock)
	w := &world{store: s, clock: clock, saved: map[string]string{}}
	w.deps = usecases.Deps{
		ItemPrices: s.ItemPrices(), Schedules: s.Schedules(), TaxTables: s.TaxTables(), Quotes: s.Quotes(),
		Catalog: s.Catalog(), Inbox: s.Inbox(), Outbox: s.Outbox(), UoW: s.UnitOfWork(),
		Clock: clock, IDs: &memory.SequentialIDs{}, Currency: "BRL",
	}
	d := w.deps
	api := &httpadapter.Server{
		CreateItemPrice: usecases.CreateItemPrice{Deps: d}, SetListPrice: usecases.SetListPrice{Deps: d},
		ScheduleSale: usecases.ScheduleSale{Deps: d}, CancelSale: usecases.CancelSale{Deps: d},
		WithdrawPrice: usecases.WithdrawPrice{Deps: d}, GetItemPrice: usecases.GetItemPrice{Deps: d},
		ListItemPrices: usecases.ListItemPrices{Deps: d}, PublishChargeSchedule: usecases.PublishChargeSchedule{Deps: d},
		GetChargeSchedule: usecases.GetCurrentChargeSchedule{Deps: d}, PublishTaxTable: usecases.PublishTaxTable{Deps: d},
		GetTaxTable: usecases.GetCurrentTaxTable{Deps: d}, IssuePriceQuote: usecases.IssuePriceQuote{Deps: d},
		GetPriceQuote: usecases.GetPriceQuote{Deps: d}, IdempotencyStore: s.Idempotency(), Clock: clock,
	}
	w.srv = httptest.NewServer(httpadapter.NewRouter(api))
	return w
}

func initializeScenario(sc *godog.ScenarioContext) {
	var w *world
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		w = newWorld()
		return ctx, nil
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		w.srv.Close()
		return ctx, nil
	})
	registerGiven(sc, func() *world { return w })
	registerWhen(sc, func() *world { return w })
	registerThen(sc, func() *world { return w })
}
