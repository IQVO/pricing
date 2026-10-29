package usecases_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/IQVO/pricing/internal/application/ports"
	"github.com/IQVO/pricing/internal/application/usecases"
	"github.com/IQVO/pricing/internal/domain/quote"
	"github.com/IQVO/pricing/internal/domain/schedule"
	"github.com/IQVO/pricing/internal/domain/shared"
	"github.com/IQVO/pricing/internal/domain/tax"
)

func TestPublishChargeScheduleSupersedesThePrevious(t *testing.T) {
	e := newEnv(t)
	if _, err := (usecases.GetCurrentChargeSchedule{Deps: e.deps}).Execute(bg); !isErr(err, ports.ErrNotFound) {
		t.Fatalf("no schedule yet: %v", err)
	}
	pub := usecases.PublishChargeSchedule{Deps: e.deps}
	v1, err := pub.Execute(bg, usecases.PublishChargeScheduleInput{Version: 1, Tiers: fullTiers()})
	if err != nil {
		t.Fatal(err)
	}
	if v1.ScheduleID != "schedule-1" || v1.Version != 1 || v1.State != "CURRENT" || len(v1.Tiers) != 4 || v1.Tiers[1].FreeOver != brl(30000) {
		t.Errorf("v1 = %+v", v1)
	}
	v2, err := pub.Execute(bg, usecases.PublishChargeScheduleInput{Version: 2, Tiers: fullTiers()})
	if err != nil {
		t.Fatal(err)
	}
	cur, err := (usecases.GetCurrentChargeSchedule{Deps: e.deps}).Execute(bg)
	if err != nil || cur.ScheduleID != v2.ScheduleID || cur.Version != 2 || cur.State != "CURRENT" {
		t.Errorf("current = %+v, %v", cur, err)
	}
	if got := eventNames(e); !equalStrings(got, []string{"DeliveryChargeScheduleChanged", "DeliveryChargeScheduleChanged"}) {
		t.Errorf("events = %v", got)
	}
	if c := e.store.Counts(); c.Schedules != 2 {
		t.Errorf("history rows = %d, want 2", c.Schedules)
	}
}

func TestPublishChargeScheduleRefusals(t *testing.T) {
	e := newEnv(t)
	e.publishSchedule(3)
	before := e.store.Counts()
	pub := usecases.PublishChargeSchedule{Deps: e.deps}
	if _, err := pub.Execute(bg, usecases.PublishChargeScheduleInput{Version: 3, Tiers: fullTiers()}); !isErr(err, schedule.ErrNonIncreasingVersion) {
		t.Errorf("repeated version: %v", err)
	}
	if _, err := pub.Execute(bg, usecases.PublishChargeScheduleInput{Version: 2, Tiers: fullTiers()}); !isErr(err, schedule.ErrNonIncreasingVersion) {
		t.Errorf("lower version: %v", err)
	}
	if _, err := pub.Execute(bg, usecases.PublishChargeScheduleInput{Version: 4, Tiers: fullTiers()[:3]}); !isErr(err, schedule.ErrIncompleteChargeSchedule) {
		t.Errorf("incomplete: %v", err)
	}
	if e.store.Counts() != before {
		t.Error("a refused publication changed the store")
	}
	cur, _ := (usecases.GetCurrentChargeSchedule{Deps: e.deps}).Execute(bg)
	if cur.Version != 3 || cur.State != "CURRENT" {
		t.Errorf("the previous schedule must stay current: %+v", cur)
	}
}

func TestPublishTaxTableSupersedesThePrevious(t *testing.T) {
	e := newEnv(t)
	if _, err := (usecases.GetCurrentTaxTable{Deps: e.deps}).Execute(bg); !isErr(err, ports.ErrNotFound) {
		t.Fatalf("no table yet: %v", err)
	}
	pub := usecases.PublishTaxTable{Deps: e.deps}
	if _, err := pub.Execute(bg, usecases.PublishTaxTableInput{Version: 1, Bands: bands()}); err != nil {
		t.Fatal(err)
	}
	v2, err := pub.Execute(bg, usecases.PublishTaxTableInput{Version: 2, Bands: bands()[:1]})
	if err != nil {
		t.Fatal(err)
	}
	cur, err := (usecases.GetCurrentTaxTable{Deps: e.deps}).Execute(bg)
	if err != nil || cur.TaxTableID != v2.TaxTableID || cur.Version != 2 || cur.State != "CURRENT" || len(cur.Bands) != 1 || cur.Bands[0].RateBasisPoints != 1000 {
		t.Errorf("current = %+v, %v", cur, err)
	}
	if got := eventNames(e); !equalStrings(got, []string{"TaxTableChanged", "TaxTableChanged"}) {
		t.Errorf("events = %v", got)
	}
	before := e.store.Counts()
	if _, err := pub.Execute(bg, usecases.PublishTaxTableInput{Version: 2, Bands: bands()}); !isErr(err, tax.ErrNonIncreasingVersion) {
		t.Errorf("repeated version: %v", err)
	}
	if _, err := pub.Execute(bg, usecases.PublishTaxTableInput{Version: 3, Bands: []tax.TaxBand{bands()[0], bands()[0]}}); !isErr(err, tax.ErrOverlappingTaxBand) {
		t.Errorf("overlap: %v", err)
	}
	if e.store.Counts() != before {
		t.Error("a refused publication changed the store")
	}
}

func quoteInput() usecases.IssuePriceQuoteInput {
	return usecases.IssuePriceQuoteInput{
		CartID: "cart-1", DestinationPostalPrefix: "100",
		Lines: []usecases.QuoteLine{{CatalogItemID: "a", Quantity: 2}, {CatalogItemID: "b", Quantity: 1}},
		Groups: []usecases.QuoteGroup{
			{GroupNo: 1, OfferID: "off-1", Tier: shared.TierNextDay, LineNos: []int{1}},
			{GroupNo: 2, OfferID: "off-2", Tier: shared.TierTwoDay, LineNos: []int{2}},
		},
	}
}

func readyForQuotes(t *testing.T) *env {
	t.Helper()
	e := newEnv(t)
	e.price("a", 5000)
	e.price("b", 12345)
	e.publishSchedule(1)
	e.publishTax(1)
	return e
}

func TestIssuePriceQuote(t *testing.T) {
	e := readyForQuotes(t)
	before := len(e.store.Events())
	v, err := (usecases.IssuePriceQuote{Deps: e.deps}).Execute(bg, quoteInput())
	if err != nil {
		t.Fatal(err)
	}
	verify(t, "view", v,
		check{"QuoteID", v.QuoteID == "quote-1"},
		check{"CartID", v.CartID == "cart-1"},
		check{"State", v.State == "ISSUED"},
		check{"Currency", v.Currency == cur},
		check{"TaxMinor", v.TaxMinor == 2483},
		check{"TotalMinor", v.TotalMinor == 27308},
		check{"IssuedAt", v.IssuedAt.Equal(t0)},
		check{"ExpiresAt", v.ExpiresAt.Equal(t0.Add(usecases.DefaultQuoteTTL))},
	)
	if len(v.Lines) != 2 || len(v.Groups) != 2 {
		t.Fatalf("lines/groups = %+v / %+v", v.Lines, v.Groups)
	}
	verify(t, "lines/groups", v,
		check{"line total", v.Lines[0].LineTotal == brl(10000)},
		check{"delivery charge", v.Groups[0].DeliveryCharge == brl(1490)},
	)
	verify(t, "versions", v.Versions,
		check{"chargeSchedule", v.Versions["chargeSchedule"] == 1},
		check{"taxTable", v.Versions["taxTable"] == 1},
		check{"price:a", v.Versions["price:a"] == 1},
		check{"price:b", v.Versions["price:b"] == 1},
		check{"count", len(v.Versions) == 4},
	)
	if got := eventNames(e)[before:]; !equalStrings(got, []string{"PriceQuoteIssued"}) {
		t.Errorf("events = %v", got)
	}
	got, err := (usecases.GetPriceQuote{Deps: e.deps}).Execute(bg, v.QuoteID)
	if err != nil {
		t.Fatalf("GetPriceQuote = %v", err)
	}
	verify(t, "GetPriceQuote", got, check{"TotalMinor", got.TotalMinor == v.TotalMinor}, check{"State", got.State == "ISSUED"})
	if _, err := (usecases.GetPriceQuote{Deps: e.deps}).Execute(bg, "quote-404"); !isErr(err, ports.ErrNotFound) {
		t.Errorf("unknown quote: %v", err)
	}
}

type check struct {
	name string
	ok   bool
}

// verify reports every failed check, naming it and dumping got.
func verify(t *testing.T, what string, got any, checks ...check) {
	t.Helper()
	for _, c := range checks {
		if !c.ok {
			t.Errorf("%s: %s is wrong in %+v", what, c.name, got)
		}
	}
}

func TestIssuePriceQuoteHonoursTheConfiguredTTLAndTheEffectivePrice(t *testing.T) {
	e := readyForQuotes(t)
	if _, err := (usecases.ScheduleSale{Deps: e.deps}).Execute(bg, usecases.ScheduleSaleInput{CatalogItemID: "a", Price: brl(4000), From: t0.Add(-time.Hour), To: t0.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	e.deps.QuoteTTL = 5 * time.Minute
	v, err := (usecases.IssuePriceQuote{Deps: e.deps}).Execute(bg, quoteInput())
	if err != nil {
		t.Fatal(err)
	}
	if v.Lines[0].UnitPrice != brl(4000) || v.Lines[0].LineTotal != brl(8000) || !v.ExpiresAt.Equal(t0.Add(5*time.Minute)) {
		t.Errorf("view = %+v", v)
	}
	if v.Versions["price:a"] != 2 {
		t.Errorf("price:a version = %d, want 2 (create, sale)", v.Versions["price:a"])
	}
}

func TestIssuePriceQuoteRefusals(t *testing.T) {
	e := readyForQuotes(t)
	issue := usecases.IssuePriceQuote{Deps: e.deps}
	before := e.store.Counts()

	in := quoteInput()
	in.Lines[1].CatalogItemID = "unpriced"
	if _, err := issue.Execute(bg, in); !isErr(err, quote.ErrItemNotPriced) {
		t.Errorf("unpriced item: %v", err)
	}
	in = quoteInput()
	in.Lines[1].CatalogItemID = "bad id"
	if _, err := issue.Execute(bg, in); !isErr(err, shared.ErrValidation) {
		t.Errorf("malformed item id: %v", err)
	}
	in = quoteInput()
	in.Lines = nil
	if _, err := issue.Execute(bg, in); !isErr(err, quote.ErrEmptyQuote) {
		t.Errorf("empty quote: %v", err)
	}
	if e.store.Counts() != before {
		t.Error("a refused quote changed the store")
	}
	if _, err := (usecases.WithdrawPrice{Deps: e.deps}).Execute(bg, "b"); err != nil {
		t.Fatal(err)
	}
	before = e.store.Counts()
	if _, err := issue.Execute(bg, quoteInput()); !isErr(err, quote.ErrItemNotPriced) {
		t.Errorf("withdrawn price: %v", err)
	}
	if e.store.Counts() != before {
		t.Error("a refused quote changed the store")
	}
}

func TestIssuePriceQuoteNeedsAScheduleAndATaxTable(t *testing.T) {
	e := newEnv(t)
	e.price("a", 5000)
	e.price("b", 12345)
	issue := usecases.IssuePriceQuote{Deps: e.deps}
	if _, err := issue.Execute(bg, quoteInput()); !isErr(err, quote.ErrNoChargeSchedule) {
		t.Errorf("no schedule: %v", err)
	}
	e.publishSchedule(1)
	if _, err := issue.Execute(bg, quoteInput()); !isErr(err, quote.ErrNoTaxTable) {
		t.Errorf("no tax table: %v", err)
	}
	e.publishTax(1)
	if _, err := issue.Execute(bg, quoteInput()); err != nil {
		t.Errorf("with both: %v", err)
	}
}

func TestQuoteExpiresByTimeAndThePolicyStoresIt(t *testing.T) {
	e := readyForQuotes(t)
	v, err := (usecases.IssuePriceQuote{Deps: e.deps}).Execute(bg, quoteInput())
	if err != nil {
		t.Fatal(err)
	}
	policy := usecases.ExpireQuotes{Deps: e.deps}
	if n, err := policy.Execute(bg); err != nil || n != 0 {
		t.Fatalf("while binding: %d, %v", n, err)
	}
	e.clock.Set(v.ExpiresAt)
	// the read derives EXPIRED even before the policy stored it.
	got, err := (usecases.GetPriceQuote{Deps: e.deps}).Execute(bg, v.QuoteID)
	if err != nil || got.State != "EXPIRED" {
		t.Errorf("state at expiresAt = %q, %v", got.State, err)
	}
	events := len(e.store.Events())
	if n, err := policy.Execute(bg); err != nil || n != 1 {
		t.Fatalf("at expiresAt: %d, %v", n, err)
	}
	if n, err := policy.Execute(bg); err != nil || n != 0 {
		t.Errorf("second run: %d, %v", n, err)
	}
	if len(e.store.Events()) != events {
		t.Error("expiry must publish nothing")
	}
	q, err := e.deps.Quotes.Get(bg, quote.ID(v.QuoteID))
	if err != nil || q.State() != quote.StateExpired || q.Version() != 2 {
		t.Errorf("stored quote = %v, %v", q, err)
	}
}

func TestExpireQuotesSkipsAConflictAndReportsInfrastructureFailures(t *testing.T) {
	e := readyForQuotes(t)
	v, err := (usecases.IssuePriceQuote{Deps: e.deps}).Execute(bg, quoteInput())
	if err != nil {
		t.Fatal(err)
	}
	e.clock.Set(v.ExpiresAt)
	boom := errors.New("db down")
	d := e.deps
	d.Quotes = failingQuotes{QuoteRepository: e.deps.Quotes, saveErr: ports.ErrConcurrentUpdate}
	if n, err := (usecases.ExpireQuotes{Deps: d}).Execute(bg); err != nil || n != 0 {
		t.Errorf("conflicting save: %d, %v; want skipped", n, err)
	}
	d.Quotes = failingQuotes{QuoteRepository: e.deps.Quotes, saveErr: boom}
	if _, err := (usecases.ExpireQuotes{Deps: d}).Execute(bg); !isErr(err, boom) {
		t.Errorf("save failure: %v", err)
	}
	d.Quotes = failingQuotes{QuoteRepository: e.deps.Quotes, dueErr: boom}
	if _, err := (usecases.ExpireQuotes{Deps: d}).Execute(bg); !isErr(err, boom) {
		t.Errorf("scan failure: %v", err)
	}
	// the quote is gone between the scan and the load.
	d.Quotes = failingQuotes{QuoteRepository: e.deps.Quotes, getErr: ports.ErrNotFound}
	if n, err := (usecases.ExpireQuotes{Deps: d}).Execute(bg); err != nil || n != 0 {
		t.Errorf("vanished quote: %d, %v", n, err)
	}
}

type failingQuotes struct {
	ports.QuoteRepository
	dueErr, getErr, saveErr error
}

func (f failingQuotes) DueForExpiry(ctx context.Context, now time.Time, limit int) ([]quote.ID, error) {
	if f.dueErr != nil {
		return nil, f.dueErr
	}
	return f.QuoteRepository.DueForExpiry(ctx, now, limit)
}

func (f failingQuotes) Get(ctx context.Context, id quote.ID) (*quote.PriceQuote, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.QuoteRepository.Get(ctx, id)
}

func (f failingQuotes) Save(ctx context.Context, q *quote.PriceQuote) error {
	if f.saveErr != nil {
		return f.saveErr
	}
	return f.QuoteRepository.Save(ctx, q)
}
