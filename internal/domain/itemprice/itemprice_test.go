package itemprice_test

import (
	"errors"
	"testing"
	"time"

	"github.com/IQVO/pricing/internal/domain/itemprice"
	"github.com/IQVO/pricing/internal/domain/shared"
)

const cur = "BRL"

var t0 = time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)

func brl(n int64) shared.Money { return shared.Money{AmountMinor: n, Currency: cur} }

func in(now time.Time) itemprice.Inputs {
	return itemprice.Inputs{Currency: cur, Catalog: itemprice.CatalogActive, Now: now}
}

func hours(h int) time.Duration { return time.Duration(h) * time.Hour }

func mustCreate(t *testing.T, list int64) *itemprice.ItemPrice {
	t.Helper()
	p, _, err := itemprice.Create("cat-1", brl(list), in(t0))
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}
	return p
}

func sale(id string, price int64, from, to time.Time) itemprice.SaleWindow {
	return itemprice.SaleWindow{SaleID: id, Price: brl(price), From: from, To: to}
}

func mustSchedule(t *testing.T, p *itemprice.ItemPrice, w itemprice.SaleWindow, now time.Time) []shared.Event {
	t.Helper()
	ev, err := p.ScheduleSale(w, in(now))
	if err != nil {
		t.Fatalf("ScheduleSale(%s) = %v", w.SaleID, err)
	}
	return ev
}

func changed(t *testing.T, evs []shared.Event) itemprice.ItemPriceChanged {
	t.Helper()
	if len(evs) != 1 {
		t.Fatalf("got %d events, want 1: %#v", len(evs), evs)
	}
	e, ok := evs[0].(itemprice.ItemPriceChanged)
	if !ok {
		t.Fatalf("event = %T, want ItemPriceChanged", evs[0])
	}
	return e
}

func assertCreatedPayload(t *testing.T, e itemprice.ItemPriceChanged) {
	t.Helper()
	checks := []struct {
		name string
		ok   bool
	}{
		{"CatalogItemID", e.CatalogItemID == "cat-1"},
		{"Currency", e.Currency == cur},
		{"ListPriceMinor", e.ListPriceMinor == 12990},
		{"EffectivePriceMinor", e.EffectivePriceMinor == 12990},
		{"SaleEndsAt", e.SaleEndsAt.IsZero()},
	}
	for _, c := range checks {
		if !c.ok {
			t.Errorf("event.%s wrong: %+v", c.name, e)
		}
	}
}

func assertCreatedHeader(t *testing.T, e itemprice.ItemPriceChanged) {
	t.Helper()
	checks := []struct {
		name string
		ok   bool
	}{
		{"AggregateVersion", e.AggregateVersion() == 1},
		{"OccurredAt", e.OccurredAt().Equal(t0)},
		{"SubjectID", e.SubjectID() == "cat-1"},
		{"EventName", e.EventName() == "ItemPriceChanged"},
		{"Entity", e.Entity() == "itemprice"},
	}
	for _, c := range checks {
		if !c.ok {
			t.Errorf("event header %s wrong: %d %v %s %s %s", c.name, e.AggregateVersion(), e.OccurredAt(), e.SubjectID(), e.EventName(), e.Entity())
		}
	}
}

func assertCreatedAggregate(t *testing.T, p *itemprice.ItemPrice) {
	t.Helper()
	checks := []struct {
		name string
		ok   bool
	}{
		{"ID", p.ID() == "cat-1"},
		{"State", p.State() == itemprice.StateActive},
		{"ListPrice", p.ListPrice() == brl(12990)},
		{"Version", p.Version() == 1},
		{"LoadedVersion", p.LoadedVersion() == 0},
	}
	for _, c := range checks {
		if !c.ok {
			t.Errorf("aggregate.%s wrong: %v %v %v %d %d", c.name, p.ID(), p.State(), p.ListPrice(), p.Version(), p.LoadedVersion())
		}
	}
}

func TestCreateRaisesItemPriceChanged(t *testing.T) {
	p, evs, err := itemprice.Create("cat-1", brl(12990), in(t0))
	if err != nil {
		t.Fatal(err)
	}
	e := changed(t, evs)
	assertCreatedPayload(t, e)
	assertCreatedHeader(t, e)
	assertCreatedAggregate(t, p)
	p.MarkPersisted()
	if p.LoadedVersion() != 1 {
		t.Errorf("LoadedVersion() after MarkPersisted = %d, want 1", p.LoadedVersion())
	}
	if !p.EvaluatedAt().Equal(t0) {
		t.Errorf("EvaluatedAt() = %v, want %v", p.EvaluatedAt(), t0)
	}
}

func TestCreateRefusals(t *testing.T) {
	cases := []struct {
		name string
		id   itemprice.ID
		list shared.Money
		in   itemprice.Inputs
		want error
	}{
		{"bad id", "bad id", brl(1), in(t0), shared.ErrValidation},
		{"negative", "cat-1", brl(-1), in(t0), shared.ErrNegativeAmount},
		{"currency", "cat-1", shared.Money{AmountMinor: 1, Currency: "USD"}, in(t0), shared.ErrCurrencyMismatch},
		{"too large", "cat-1", brl(shared.MaxMinor + 1), in(t0), shared.ErrValidation},
		{"unknown item", "cat-1", brl(1), itemprice.Inputs{Currency: cur, Catalog: itemprice.CatalogUnknown, Now: t0}, itemprice.ErrUnknownCatalogItem},
		{"discontinued item", "cat-1", brl(1), itemprice.Inputs{Currency: cur, Catalog: itemprice.CatalogDiscontinued, Now: t0}, itemprice.ErrUnknownCatalogItem},
	}
	for _, c := range cases {
		p, evs, err := itemprice.Create(c.id, c.list, c.in)
		if !errors.Is(err, c.want) || p != nil || evs != nil {
			t.Errorf("%s: Create() = %v, %v, %v; want %v", c.name, p, evs, err, c.want)
		}
	}
	if _, _, err := itemprice.Create("cat-1", brl(0), in(t0)); err != nil {
		t.Errorf("a free item (list price 0) must be accepted: %v", err)
	}
}

func TestSetListPrice(t *testing.T) {
	p := mustCreate(t, 1000)
	later := t0.Add(hours(1))
	evs, err := p.SetListPrice(brl(1200), in(later))
	if err != nil {
		t.Fatal(err)
	}
	e := changed(t, evs)
	if e.ListPriceMinor != 1200 || e.EffectivePriceMinor != 1200 || e.AggregateVersion() != 2 || !e.OccurredAt().Equal(later) {
		t.Errorf("event = %+v", e)
	}
	if p.ListPrice() != brl(1200) || p.Version() != 2 {
		t.Errorf("aggregate = %v v%d", p.ListPrice(), p.Version())
	}
	// the same price again: the version moves, nothing is published.
	evs, err = p.SetListPrice(brl(1200), in(later))
	if err != nil || len(evs) != 0 {
		t.Errorf("same price: %v, %v; want no events", evs, err)
	}
	if p.Version() != 3 {
		t.Errorf("Version() = %d, want 3", p.Version())
	}
}

func TestSetListPriceRefusals(t *testing.T) {
	p := mustCreate(t, 1000)
	mustSchedule(t, p, sale("s1", 800, t0.Add(hours(5)), t0.Add(hours(9))), t0)
	cases := []struct {
		name string
		list shared.Money
		in   itemprice.Inputs
		want error
	}{
		{"negative", brl(-5), in(t0), shared.ErrNegativeAmount},
		{"currency", shared.Money{AmountMinor: 900, Currency: "EUR"}, in(t0), shared.ErrCurrencyMismatch},
		{"unknown item", brl(900), itemprice.Inputs{Currency: cur, Catalog: itemprice.CatalogUnknown, Now: t0}, itemprice.ErrUnknownCatalogItem},
		{"not above a scheduled sale", brl(800), in(t0), itemprice.ErrSaleNotBelowList},
		{"below a scheduled sale", brl(700), in(t0), itemprice.ErrSaleNotBelowList},
	}
	for _, c := range cases {
		version := p.Version()
		if _, err := p.SetListPrice(c.list, c.in); !errors.Is(err, c.want) {
			t.Errorf("%s: SetListPrice() = %v, want %v", c.name, err, c.want)
		}
		if p.Version() != version || p.ListPrice() != brl(1000) {
			t.Errorf("%s: a refused command changed the aggregate", c.name)
		}
	}
	if _, err := p.SetListPrice(brl(801), in(t0)); err != nil {
		t.Errorf("a list price just above the sale must be accepted: %v", err)
	}
}

func TestSetListPriceUnderActiveSaleKeepsEffectivePrice(t *testing.T) {
	p := mustCreate(t, 1000)
	mustSchedule(t, p, sale("s1", 800, t0.Add(-hours(1)), t0.Add(hours(4))), t0)
	now := t0.Add(hours(1))
	evs, err := p.SetListPrice(brl(1500), in(now))
	if err != nil {
		t.Fatal(err)
	}
	e := changed(t, evs)
	if e.ListPriceMinor != 1500 || e.EffectivePriceMinor != 800 || !e.SaleEndsAt.Equal(t0.Add(hours(4))) {
		t.Errorf("event = %+v, want list 1500, effective 800, sale end %v", e, t0.Add(hours(4)))
	}
}

func TestScheduleSaleFutureWindowPublishesNothingYet(t *testing.T) {
	p := mustCreate(t, 1000)
	from, to := t0.Add(hours(2)), t0.Add(hours(6))
	evs := mustSchedule(t, p, sale("s1", 700, from, to), t0)
	if len(evs) != 0 {
		t.Fatalf("a future window raised %v", evs)
	}
	if got := p.EffectivePrice(t0); got != brl(1000) {
		t.Errorf("EffectivePrice(now) = %v, want the list price", got)
	}
	if got := p.EffectivePrice(from); got != brl(700) {
		t.Errorf("EffectivePrice(from) = %v, want the sale price (From is inclusive)", got)
	}
	if got := p.EffectivePrice(to.Add(-time.Nanosecond)); got != brl(700) {
		t.Errorf("EffectivePrice(just before to) = %v, want the sale price", got)
	}
	if got := p.EffectivePrice(to); got != brl(1000) {
		t.Errorf("EffectivePrice(to) = %v, want the list price (To is exclusive)", got)
	}
	if got := p.EffectivePrice(from.Add(-time.Nanosecond)); got != brl(1000) {
		t.Errorf("EffectivePrice(just before from) = %v, want the list price", got)
	}
	if s := p.Sales(); len(s) != 1 || s[0].SaleID != "s1" {
		t.Errorf("Sales() = %v", s)
	}
	if b := p.NextBoundary(); !b.Equal(from) {
		t.Errorf("NextBoundary() = %v, want %v", b, from)
	}
}

func TestScheduleSaleOpenWindowPublishesTheEffectivePrice(t *testing.T) {
	p := mustCreate(t, 12990)
	to := t0.Add(hours(24))
	evs := mustSchedule(t, p, sale("s1", 9990, t0, to), t0)
	e := changed(t, evs)
	if e.ListPriceMinor != 12990 || e.EffectivePriceMinor != 9990 || !e.SaleEndsAt.Equal(to) || e.AggregateVersion() != 2 {
		t.Errorf("event = %+v", e)
	}
}

func TestScheduleSaleRefusals(t *testing.T) {
	p := mustCreate(t, 1000)
	mustSchedule(t, p, sale("s1", 700, t0.Add(hours(2)), t0.Add(hours(6))), t0)
	good := sale("s2", 600, t0.Add(hours(10)), t0.Add(hours(12)))
	cases := []struct {
		name string
		w    itemprice.SaleWindow
		in   itemprice.Inputs
		want error
	}{
		{"bad sale id", itemprice.SaleWindow{SaleID: "a b", Price: good.Price, From: good.From, To: good.To}, in(t0), shared.ErrValidation},
		{"negative", sale("s2", -1, good.From, good.To), in(t0), shared.ErrNegativeAmount},
		{"currency", itemprice.SaleWindow{SaleID: "s2", Price: shared.Money{AmountMinor: 5, Currency: "USD"}, From: good.From, To: good.To}, in(t0), shared.ErrCurrencyMismatch},
		{"unknown item", good, itemprice.Inputs{Currency: cur, Catalog: itemprice.CatalogDiscontinued, Now: t0}, itemprice.ErrUnknownCatalogItem},
		{"no start", sale("s2", 600, time.Time{}, good.To), in(t0), shared.ErrValidation},
		{"ends with start", sale("s2", 600, good.From, good.From), in(t0), shared.ErrValidation},
		{"ends before start", sale("s2", 600, good.To, good.From), in(t0), shared.ErrValidation},
		{"already ended", sale("s2", 600, t0.Add(-hours(3)), t0.Add(-hours(1))), in(t0), shared.ErrValidation},
		{"ends right now", sale("s2", 600, t0.Add(-hours(3)), t0), in(t0), shared.ErrValidation},
		{"equal to list", sale("s2", 1000, good.From, good.To), in(t0), itemprice.ErrSaleNotBelowList},
		{"above list", sale("s2", 1001, good.From, good.To), in(t0), itemprice.ErrSaleNotBelowList},
		{"overlaps the head", sale("s2", 600, t0.Add(hours(1)), t0.Add(hours(3))), in(t0), itemprice.ErrOverlappingSaleWindow},
		{"overlaps the tail", sale("s2", 600, t0.Add(hours(5)), t0.Add(hours(8))), in(t0), itemprice.ErrOverlappingSaleWindow},
		{"inside", sale("s2", 600, t0.Add(hours(3)), t0.Add(hours(4))), in(t0), itemprice.ErrOverlappingSaleWindow},
		{"around", sale("s2", 600, t0.Add(hours(1)), t0.Add(hours(7))), in(t0), itemprice.ErrOverlappingSaleWindow},
		{"same window", sale("s2", 600, t0.Add(hours(2)), t0.Add(hours(6))), in(t0), itemprice.ErrOverlappingSaleWindow},
	}
	for _, c := range cases {
		version := p.Version()
		evs, err := p.ScheduleSale(c.w, c.in)
		if !errors.Is(err, c.want) || evs != nil {
			t.Errorf("%s: ScheduleSale() = %v, %v; want %v", c.name, evs, err, c.want)
		}
		if p.Version() != version || len(p.Sales()) != 1 {
			t.Errorf("%s: a refused command changed the aggregate", c.name)
		}
	}
	// windows that only touch do not overlap.
	mustSchedule(t, p, sale("s3", 600, t0.Add(hours(6)), t0.Add(hours(7))), t0)
	mustSchedule(t, p, sale("s4", 600, t0.Add(hours(1)), t0.Add(hours(2))), t0)
	if s := p.Sales(); len(s) != 3 || s[0].SaleID != "s4" || s[1].SaleID != "s1" || s[2].SaleID != "s3" {
		t.Errorf("Sales() = %v, want s4, s1, s3 in order", s)
	}
}

func TestScheduleSaleBoundsTheNumberOfWindowsAndDropsEndedOnes(t *testing.T) {
	p := mustCreate(t, 1000)
	for i := 0; i < itemprice.MaxSales; i++ {
		from := t0.Add(hours(2 * i))
		id := "s" + string(rune('a'+i/26)) + string(rune('a'+i%26))
		mustSchedule(t, p, sale(id, 500, from.Add(time.Minute), from.Add(hours(1))), t0)
	}
	extra := sale("extra", 500, t0.Add(hours(2*itemprice.MaxSales)), t0.Add(hours(2*itemprice.MaxSales+1)))
	if _, err := p.ScheduleSale(extra, in(t0)); !errors.Is(err, shared.ErrValidation) {
		t.Fatalf("window %d: err = %v, want ErrValidation", itemprice.MaxSales+1, err)
	}
	// much later, the ended windows no longer count and are dropped.
	later := t0.Add(hours(2*itemprice.MaxSales + 10))
	extra = sale("extra", 500, later.Add(hours(1)), later.Add(hours(2)))
	mustSchedule(t, p, extra, later)
	if s := p.Sales(); len(s) != 1 || s[0].SaleID != "extra" {
		t.Errorf("Sales() = %v, want only the new window", s)
	}
}

func TestCancelSale(t *testing.T) {
	p := mustCreate(t, 1000)
	to := t0.Add(hours(5))
	mustSchedule(t, p, sale("s1", 700, t0, to), t0)
	mustSchedule(t, p, sale("s2", 600, t0.Add(hours(10)), t0.Add(hours(12))), t0)
	version := p.Version()

	now := t0.Add(hours(1))
	evs, err := p.CancelSale("s1", now)
	if err != nil {
		t.Fatal(err)
	}
	e := changed(t, evs)
	if e.ListPriceMinor != 1000 || e.EffectivePriceMinor != 1000 || !e.SaleEndsAt.IsZero() || e.AggregateVersion() != version+1 || !e.OccurredAt().Equal(now) {
		t.Errorf("event = %+v, want the price back at list", e)
	}
	if s := p.Sales(); len(s) != 1 || s[0].SaleID != "s2" {
		t.Errorf("Sales() = %v, want only s2", s)
	}
	// cancelling a window that is not open raises nothing.
	evs, err = p.CancelSale("s2", now)
	if err != nil || len(evs) != 0 {
		t.Errorf("CancelSale(s2) = %v, %v; want no events", evs, err)
	}
	if len(p.Sales()) != 0 {
		t.Errorf("Sales() = %v, want none", p.Sales())
	}
}

func TestCancelSaleUnknown(t *testing.T) {
	p := mustCreate(t, 1000)
	mustSchedule(t, p, sale("s1", 700, t0.Add(hours(1)), t0.Add(hours(2))), t0)
	version := p.Version()
	if _, err := p.CancelSale("s9", t0); !errors.Is(err, itemprice.ErrUnknownSale) {
		t.Errorf("CancelSale(unknown) = %v, want ErrUnknownSale", err)
	}
	if p.Version() != version || len(p.Sales()) != 1 {
		t.Error("a refused cancel changed the aggregate")
	}
}

func TestWithdrawPrice(t *testing.T) {
	p := mustCreate(t, 1000)
	mustSchedule(t, p, sale("s1", 700, t0.Add(hours(1)), t0.Add(hours(2))), t0)
	now := t0.Add(hours(3))
	evs, err := p.WithdrawPrice(now)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 {
		t.Fatalf("got %d events, want 1", len(evs))
	}
	w, ok := evs[0].(itemprice.ItemPriceWithdrawn)
	if !ok {
		t.Fatalf("event = %T", evs[0])
	}
	if w.CatalogItemID != "cat-1" || w.AggregateVersion() != p.Version() || !w.OccurredAt().Equal(now) || w.EventName() != "ItemPriceWithdrawn" || w.Entity() != "itemprice" || w.SubjectID() != "cat-1" {
		t.Errorf("event = %+v", w)
	}
	if p.State() != itemprice.StateWithdrawn || p.Version() != 3 {
		t.Errorf("state %s version %d", p.State(), p.Version())
	}
	if !p.NextBoundary().IsZero() {
		t.Errorf("NextBoundary() = %v, a withdrawn price has none", p.NextBoundary())
	}
}

func TestWithdrawnPriceRefusesEveryCommand(t *testing.T) {
	p := mustCreate(t, 1000)
	mustSchedule(t, p, sale("s1", 700, t0.Add(hours(1)), t0.Add(hours(2))), t0)
	if _, err := p.WithdrawPrice(t0); err != nil {
		t.Fatal(err)
	}
	version := p.Version()
	checks := map[string]func() ([]shared.Event, error){
		"withdraw": func() ([]shared.Event, error) { return p.WithdrawPrice(t0) },
		"set list": func() ([]shared.Event, error) { return p.SetListPrice(brl(900), in(t0)) },
		"schedule": func() ([]shared.Event, error) {
			return p.ScheduleSale(sale("s2", 500, t0.Add(hours(8)), t0.Add(hours(9))), in(t0))
		},
		"cancel":     func() ([]shared.Event, error) { return p.CancelSale("s1", t0) },
		"reevaluate": func() ([]shared.Event, error) { return p.Reevaluate(t0) },
	}
	for name, run := range checks {
		evs, err := run()
		if !errors.Is(err, shared.ErrIllegalTransition) || evs != nil {
			t.Errorf("%s: = %v, %v; want ErrIllegalTransition", name, evs, err)
		}
		if errors.Is(err, shared.ErrValidation) {
			t.Errorf("%s: an illegal transition must not read as a validation failure", name)
		}
	}
	if p.Version() != version || p.State() != itemprice.StateWithdrawn {
		t.Error("a refused command changed the aggregate")
	}
}

func TestReevaluatePublishesEachBoundaryOnce(t *testing.T) {
	p := mustCreate(t, 1000)
	from, to := t0.Add(hours(2)), t0.Add(hours(6))
	mustSchedule(t, p, sale("s1", 700, from, to), t0)

	// before the window opens nothing moved.
	evs, err := p.Reevaluate(t0.Add(hours(1)))
	if err != nil || len(evs) != 0 {
		t.Fatalf("early Reevaluate = %v, %v", evs, err)
	}
	// the window opens.
	evs, err = p.Reevaluate(from)
	if err != nil {
		t.Fatal(err)
	}
	e := changed(t, evs)
	if e.EffectivePriceMinor != 700 || !e.SaleEndsAt.Equal(to) || e.ListPriceMinor != 1000 {
		t.Errorf("opening event = %+v", e)
	}
	// the same boundary again: idempotent.
	evs, err = p.Reevaluate(from.Add(time.Minute))
	if err != nil || len(evs) != 0 {
		t.Errorf("repeat Reevaluate = %v, %v; want nothing", evs, err)
	}
	if b := p.NextBoundary(); !b.Equal(to) {
		t.Errorf("NextBoundary() = %v, want %v", b, to)
	}
	// the window closes.
	evs, err = p.Reevaluate(to)
	if err != nil {
		t.Fatal(err)
	}
	e = changed(t, evs)
	if e.EffectivePriceMinor != 1000 || !e.SaleEndsAt.IsZero() {
		t.Errorf("closing event = %+v", e)
	}
	if !p.NextBoundary().IsZero() {
		t.Errorf("NextBoundary() = %v, want none left", p.NextBoundary())
	}
}

func TestNextBoundary(t *testing.T) {
	p := mustCreate(t, 1000)
	if !p.NextBoundary().IsZero() {
		t.Errorf("a price with no sale has no boundary, got %v", p.NextBoundary())
	}
	a, b := t0.Add(hours(2)), t0.Add(hours(4))
	c, d := t0.Add(hours(6)), t0.Add(hours(8))
	mustSchedule(t, p, sale("s2", 500, c, d), t0)
	mustSchedule(t, p, sale("s1", 500, a, b), t0)
	want := []time.Time{a, b, c, d}
	for i, w := range want {
		if got := p.NextBoundary(); !got.Equal(w) {
			t.Fatalf("boundary %d = %v, want %v", i, got, w)
		}
		// handling the boundary moves the next one on (a boundary equal to
		// EvaluatedAt is handled, a later one is not).
		if _, err := p.Reevaluate(w); err != nil {
			t.Fatal(err)
		}
	}
	if !p.NextBoundary().IsZero() {
		t.Errorf("NextBoundary() = %v, want none", p.NextBoundary())
	}
}

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	p := mustCreate(t, 1000)
	mustSchedule(t, p, sale("s1", 700, t0, t0.Add(hours(5))), t0)
	mustSchedule(t, p, sale("s2", 600, t0.Add(hours(8)), t0.Add(hours(9))), t0)
	snap := p.Snapshot()
	if snap.NextBoundary() != p.NextBoundary() {
		t.Errorf("Snapshot.NextBoundary() = %v, want %v", snap.NextBoundary(), p.NextBoundary())
	}
	// mutating the snapshot must not reach the aggregate.
	snap.Sales[0].SaleID = "mutated"
	if p.Sales()[0].SaleID != "s1" {
		t.Error("Snapshot() shares its sale slice with the aggregate")
	}
	snap = p.Snapshot()
	r, err := itemprice.Restore(snap)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Snapshot(); !equalSnapshots(got, snap) {
		t.Errorf("round trip changed the snapshot:\n got %+v\nwant %+v", got, snap)
	}
	if r.LoadedVersion() != p.Version() || r.Version() != p.Version() {
		t.Errorf("restored versions = %d/%d, want %d", r.Version(), r.LoadedVersion(), p.Version())
	}
	// the restored aggregate keeps publishing from the stored view: a repeat
	// evaluation raises nothing.
	evs, err := r.Reevaluate(t0.Add(time.Hour))
	if err != nil || len(evs) != 0 {
		t.Errorf("Reevaluate after Restore = %v, %v; want nothing", evs, err)
	}
}

func equalSnapshots(a, b itemprice.Snapshot) bool {
	if a.ID != b.ID || a.State != b.State || a.ListPrice != b.ListPrice || a.Version != b.Version ||
		!a.EvaluatedAt.Equal(b.EvaluatedAt) || a.Published.ListMinor != b.Published.ListMinor ||
		a.Published.EffectiveMinor != b.Published.EffectiveMinor || !a.Published.SaleEndsAt.Equal(b.Published.SaleEndsAt) ||
		len(a.Sales) != len(b.Sales) {
		return false
	}
	for i := range a.Sales {
		x, y := a.Sales[i], b.Sales[i]
		if x.SaleID != y.SaleID || x.Price != y.Price || !x.From.Equal(y.From) || !x.To.Equal(y.To) {
			return false
		}
	}
	return true
}

func TestRestoreTruncatesToMicroseconds(t *testing.T) {
	snap := mustCreate(t, 1000).Snapshot()
	snap.EvaluatedAt = time.Date(2026, 10, 24, 9, 0, 0, 123456789, time.UTC)
	snap.Published.SaleEndsAt = time.Date(2026, 10, 25, 9, 0, 0, 123456789, time.UTC)
	snap.Sales = []itemprice.SaleWindow{{
		SaleID: "s1", Price: brl(500),
		From: time.Date(2026, 10, 24, 9, 0, 0, 123456789, time.UTC), To: time.Date(2026, 10, 25, 9, 0, 0, 123456789, time.UTC),
	}}
	r, err := itemprice.Restore(snap)
	if err != nil {
		t.Fatal(err)
	}
	got := r.Snapshot()
	if got.EvaluatedAt.Nanosecond() != 123456000 || got.Published.SaleEndsAt.Nanosecond() != 123456000 ||
		got.Sales[0].From.Nanosecond() != 123456000 || got.Sales[0].To.Nanosecond() != 123456000 {
		t.Errorf("Restore() kept sub-microsecond precision: %+v", got)
	}
}

func TestRestoreRefusesCorruptSnapshots(t *testing.T) {
	good := func() itemprice.Snapshot {
		p := mustCreate(t, 1000)
		mustSchedule(t, p, sale("s1", 700, t0.Add(hours(1)), t0.Add(hours(2))), t0)
		return p.Snapshot()
	}
	cases := map[string]func(*itemprice.Snapshot){
		"bad id":              func(s *itemprice.Snapshot) { s.ID = "bad id" },
		"unknown state":       func(s *itemprice.Snapshot) { s.State = "GONE" },
		"zero version":        func(s *itemprice.Snapshot) { s.Version = 0 },
		"bad currency":        func(s *itemprice.Snapshot) { s.ListPrice.Currency = "br" },
		"negative list":       func(s *itemprice.Snapshot) { s.ListPrice.AmountMinor = -1 },
		"too many sales":      func(s *itemprice.Snapshot) { s.Sales = make([]itemprice.SaleWindow, itemprice.MaxSales+1) },
		"bad sale id":         func(s *itemprice.Snapshot) { s.Sales[0].SaleID = "a b" },
		"sale currency":       func(s *itemprice.Snapshot) { s.Sales[0].Price.Currency = "USD" },
		"sale equal to list":  func(s *itemprice.Snapshot) { s.Sales[0].Price.AmountMinor = 1000 },
		"sale above list":     func(s *itemprice.Snapshot) { s.Sales[0].Price.AmountMinor = 1001 },
		"sale ends at start":  func(s *itemprice.Snapshot) { s.Sales[0].To = s.Sales[0].From },
		"sale ends before it": func(s *itemprice.Snapshot) { s.Sales[0].To = s.Sales[0].From.Add(-time.Second) },
		"overlapping sales": func(s *itemprice.Snapshot) {
			s.Sales = append(s.Sales, sale("s2", 600, s.Sales[0].From.Add(time.Minute), s.Sales[0].To))
		},
		"overlapping, reversed": func(s *itemprice.Snapshot) {
			s.Sales = append([]itemprice.SaleWindow{sale("s2", 600, s.Sales[0].From.Add(-time.Minute), s.Sales[0].From.Add(time.Minute))}, s.Sales...)
		},
		"duplicate sale id": func(s *itemprice.Snapshot) {
			s.Sales = append(s.Sales, sale("s1", 600, s.Sales[0].To.Add(time.Hour), s.Sales[0].To.Add(2*time.Hour)))
		},
	}
	for name, mutate := range cases {
		s := good()
		mutate(&s)
		if _, err := itemprice.Restore(s); !errors.Is(err, shared.ErrInvalidSnapshot) {
			t.Errorf("%s: Restore() = %v, want ErrInvalidSnapshot", name, err)
		}
	}
	if _, err := itemprice.Restore(good()); err != nil {
		t.Errorf("a good snapshot must restore: %v", err)
	}
	s := good()
	s.Sales = append(s.Sales, sale("s2", 600, s.Sales[0].To, s.Sales[0].To.Add(time.Hour)))
	if _, err := itemprice.Restore(s); err != nil {
		t.Errorf("touching windows are not overlapping: %v", err)
	}
	s = good()
	s.Sales = make([]itemprice.SaleWindow, 0, itemprice.MaxSales)
	for i := 0; i < itemprice.MaxSales; i++ {
		from := t0.Add(hours(2 * i))
		s.Sales = append(s.Sales, sale("w"+string(rune('a'+i/26))+string(rune('a'+i%26)), 500, from, from.Add(hours(1))))
	}
	if _, err := itemprice.Restore(s); err != nil {
		t.Errorf("exactly MaxSales windows must restore: %v", err)
	}
}

func TestStateValid(t *testing.T) {
	if !itemprice.StateActive.Valid() || !itemprice.StateWithdrawn.Valid() || itemprice.State("").Valid() || itemprice.State("active").Valid() {
		t.Error("State.Valid() is wrong")
	}
}
