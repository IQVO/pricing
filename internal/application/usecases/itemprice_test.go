package usecases_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/IQVO/pricing/internal/application/ports"
	"github.com/IQVO/pricing/internal/application/usecases"
	"github.com/IQVO/pricing/internal/domain/itemprice"
	"github.com/IQVO/pricing/internal/domain/shared"
)

var bg = context.Background()

func TestCreateItemPricePublishesItemPriceChanged(t *testing.T) {
	e := newEnv(t)
	e.catalogCreated("cat-1")
	v, err := (usecases.CreateItemPrice{Deps: e.deps}).Execute(bg, usecases.CreateItemPriceInput{CatalogItemID: "cat-1", ListPrice: brl(12990)})
	if err != nil {
		t.Fatal(err)
	}
	if v.CatalogItemID != "cat-1" || v.State != "ACTIVE" || v.ListPrice != brl(12990) || v.EffectivePrice != brl(12990) || v.Version != 1 || len(v.Sales) != 0 {
		t.Errorf("view = %+v", v)
	}
	if got := eventNames(e); !equalStrings(got, []string{"ItemPriceChanged"}) {
		t.Errorf("events = %v", got)
	}
	// a second create is a conflict, nothing more is stored or published.
	_, err = (usecases.CreateItemPrice{Deps: e.deps}).Execute(bg, usecases.CreateItemPriceInput{CatalogItemID: "cat-1", ListPrice: brl(1)})
	if !isErr(err, ports.ErrConcurrentUpdate) {
		t.Errorf("second create = %v, want ErrConcurrentUpdate", err)
	}
	if len(e.store.Events()) != 1 {
		t.Errorf("events after the conflict = %v", eventNames(e))
	}
}

func TestCreateItemPriceNeedsAnActiveCatalogEntry(t *testing.T) {
	e := newEnv(t)
	_, err := (usecases.CreateItemPrice{Deps: e.deps}).Execute(bg, usecases.CreateItemPriceInput{CatalogItemID: "cat-1", ListPrice: brl(1)})
	if !isErr(err, itemprice.ErrUnknownCatalogItem) {
		t.Errorf("unknown item: %v", err)
	}
	e.catalogCreated("cat-1")
	if err := (usecases.Consumer{Deps: e.deps}).CatalogItemDiscontinued(bg, "d-1", "t", "cat-1"); err != nil {
		t.Fatal(err)
	}
	_, err = (usecases.CreateItemPrice{Deps: e.deps}).Execute(bg, usecases.CreateItemPriceInput{CatalogItemID: "cat-1", ListPrice: brl(1)})
	if !isErr(err, itemprice.ErrUnknownCatalogItem) {
		t.Errorf("discontinued item: %v", err)
	}
	c := e.store.Counts()
	if c.ItemPrices != 0 || c.OutboxEvents != 0 {
		t.Errorf("counts after refusals = %+v", c)
	}
}

func TestSetListPrice(t *testing.T) {
	e := newEnv(t)
	e.price("cat-1", 1000)
	e.clock.Advance(time.Hour)
	v, err := (usecases.SetListPrice{Deps: e.deps}).Execute(bg, usecases.SetListPriceInput{CatalogItemID: "cat-1", ListPrice: brl(1200)})
	if err != nil {
		t.Fatal(err)
	}
	if v.ListPrice != brl(1200) || v.EffectivePrice != brl(1200) || v.Version != 2 {
		t.Errorf("view = %+v", v)
	}
	if got := eventNames(e); !equalStrings(got, []string{"ItemPriceChanged", "ItemPriceChanged"}) {
		t.Errorf("events = %v", got)
	}
	// the optimistic guard: the right version passes, a stale one conflicts.
	if _, err := (usecases.SetListPrice{Deps: e.deps}).Execute(bg, usecases.SetListPriceInput{CatalogItemID: "cat-1", ListPrice: brl(1300), ExpectedVersion: 2}); err != nil {
		t.Errorf("matching version: %v", err)
	}
	before := e.store.Counts()
	_, err = (usecases.SetListPrice{Deps: e.deps}).Execute(bg, usecases.SetListPriceInput{CatalogItemID: "cat-1", ListPrice: brl(1400), ExpectedVersion: 2})
	if !isErr(err, ports.ErrConcurrentUpdate) {
		t.Errorf("stale version: %v", err)
	}
	if e.store.Counts() != before {
		t.Error("a refused SetListPrice changed the store")
	}
}

func TestSetListPriceErrors(t *testing.T) {
	e := newEnv(t)
	_, err := (usecases.SetListPrice{Deps: e.deps}).Execute(bg, usecases.SetListPriceInput{CatalogItemID: "nope", ListPrice: brl(1)})
	if !isErr(err, ports.ErrNotFound) {
		t.Errorf("unpriced item: %v", err)
	}
	e.price("cat-1", 1000)
	for name, c := range map[string]struct {
		price shared.Money
		want  error
	}{
		"negative": {brl(-1), shared.ErrNegativeAmount},
		"currency": {shared.Money{AmountMinor: 5, Currency: "USD"}, shared.ErrCurrencyMismatch},
	} {
		if _, err := (usecases.SetListPrice{Deps: e.deps}).Execute(bg, usecases.SetListPriceInput{CatalogItemID: "cat-1", ListPrice: c.price}); !isErr(err, c.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestScheduleSaleAndCancelSale(t *testing.T) {
	e := newEnv(t)
	e.price("cat-1", 1000)
	w, err := (usecases.ScheduleSale{Deps: e.deps}).Execute(bg, usecases.ScheduleSaleInput{CatalogItemID: "cat-1", Price: brl(700), From: t0.Add(-time.Hour), To: t0.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if w.SaleID != "sale-1" || w.Price != brl(700) || !w.To.Equal(t0.Add(time.Hour)) {
		t.Errorf("window = %+v", w)
	}
	// the window is already open: the effective price moved.
	if got := eventNames(e); !equalStrings(got, []string{"ItemPriceChanged", "ItemPriceChanged"}) {
		t.Errorf("events after ScheduleSale = %v", got)
	}
	got, err := (usecases.GetItemPrice{Deps: e.deps}).Execute(bg, "cat-1")
	if err != nil || got.EffectivePrice != brl(700) || len(got.Sales) != 1 {
		t.Fatalf("GetItemPrice = %+v, %v", got, err)
	}
	v, err := (usecases.CancelSale{Deps: e.deps}).Execute(bg, "cat-1", w.SaleID)
	if err != nil {
		t.Fatal(err)
	}
	if v.EffectivePrice != brl(1000) || len(v.Sales) != 0 {
		t.Errorf("view after CancelSale = %+v", v)
	}
	// CancelSale has no event of its own: it raises ItemPriceChanged.
	if got := eventNames(e); !equalStrings(got, []string{"ItemPriceChanged", "ItemPriceChanged", "ItemPriceChanged"}) {
		t.Errorf("events after CancelSale = %v", got)
	}
}

func TestScheduleSaleAndCancelSaleErrors(t *testing.T) {
	e := newEnv(t)
	in := usecases.ScheduleSaleInput{CatalogItemID: "cat-1", Price: brl(700), From: t0.Add(time.Hour), To: t0.Add(2 * time.Hour)}
	if _, err := (usecases.ScheduleSale{Deps: e.deps}).Execute(bg, in); !isErr(err, ports.ErrNotFound) {
		t.Errorf("ScheduleSale on an unpriced item: %v", err)
	}
	if _, err := (usecases.CancelSale{Deps: e.deps}).Execute(bg, "cat-1", "s"); !isErr(err, ports.ErrNotFound) {
		t.Errorf("CancelSale on an unpriced item: %v", err)
	}
	e.price("cat-1", 1000)
	if _, err := (usecases.ScheduleSale{Deps: e.deps}).Execute(bg, in); err != nil {
		t.Fatal(err)
	}
	if _, err := (usecases.ScheduleSale{Deps: e.deps}).Execute(bg, in); !isErr(err, itemprice.ErrOverlappingSaleWindow) {
		t.Errorf("overlap: %v", err)
	}
	in.Price = brl(1000)
	in.From, in.To = t0.Add(5*time.Hour), t0.Add(6*time.Hour)
	if _, err := (usecases.ScheduleSale{Deps: e.deps}).Execute(bg, in); !isErr(err, itemprice.ErrSaleNotBelowList) {
		t.Errorf("sale not below list: %v", err)
	}
	if _, err := (usecases.CancelSale{Deps: e.deps}).Execute(bg, "cat-1", "sale-404"); !isErr(err, itemprice.ErrUnknownSale) {
		t.Errorf("unknown sale: %v", err)
	}
	// the catalogue entry went away: scheduling is refused.
	if err := (usecases.Consumer{Deps: e.deps}).CatalogItemDiscontinued(bg, "d-1", "t", "cat-1"); err != nil {
		t.Fatal(err)
	}
	in.Price, in.From, in.To = brl(500), t0.Add(8*time.Hour), t0.Add(9*time.Hour)
	if _, err := (usecases.ScheduleSale{Deps: e.deps}).Execute(bg, in); !isErr(err, shared.ErrIllegalTransition) {
		t.Errorf("sale on a withdrawn price: %v", err)
	}
}

func TestWithdrawPrice(t *testing.T) {
	e := newEnv(t)
	if _, err := (usecases.WithdrawPrice{Deps: e.deps}).Execute(bg, "cat-1"); !isErr(err, ports.ErrNotFound) {
		t.Errorf("unpriced item: %v", err)
	}
	e.price("cat-1", 1000)
	v, err := (usecases.WithdrawPrice{Deps: e.deps}).Execute(bg, "cat-1")
	if err != nil {
		t.Fatal(err)
	}
	if v.State != "WITHDRAWN" {
		t.Errorf("state = %s", v.State)
	}
	if got := eventNames(e); !equalStrings(got, []string{"ItemPriceChanged", "ItemPriceWithdrawn"}) {
		t.Errorf("events = %v", got)
	}
	before := e.store.Counts()
	if _, err := (usecases.WithdrawPrice{Deps: e.deps}).Execute(bg, "cat-1"); !isErr(err, shared.ErrIllegalTransition) {
		t.Errorf("second withdraw: %v", err)
	}
	if e.store.Counts() != before {
		t.Error("a refused WithdrawPrice changed the store")
	}
}

func seededList(t *testing.T) usecases.ListItemPrices {
	t.Helper()
	e := newEnv(t)
	for _, id := range []string{"c-3", "c-1", "c-2"} {
		e.price(id, 1000)
	}
	return usecases.ListItemPrices{Deps: e.deps}
}

func TestListItemPricesKeysetPagesInIDOrder(t *testing.T) {
	list := seededList(t)
	p1, err := list.Execute(bg, "", 2)
	if err != nil || len(p1.Items) != 2 || p1.Items[0].CatalogItemID != "c-1" || p1.Items[1].CatalogItemID != "c-2" || p1.Next != "c-2" {
		t.Fatalf("page 1 = %+v, %v", p1, err)
	}
	p2, err := list.Execute(bg, p1.Next, 2)
	if err != nil || len(p2.Items) != 1 || p2.Items[0].CatalogItemID != "c-3" || p2.Next != "" {
		t.Fatalf("page 2 = %+v, %v", p2, err)
	}
}

func TestListItemPricesDefaultPageReturnsEverything(t *testing.T) {
	all, err := seededList(t).Execute(bg, "", 0)
	if err != nil || len(all.Items) != 3 || all.Next != "" {
		t.Errorf("default page = %+v, %v", all, err)
	}
}

func TestListItemPricesPageExactlyTheSizeOfTheDataHasNoNext(t *testing.T) {
	exact, err := seededList(t).Execute(bg, "", 3)
	if err != nil || len(exact.Items) != 3 || exact.Next != "" {
		t.Errorf("a page exactly the size of the data = %+v, %v", exact, err)
	}
}

func TestListItemPricesRefusesLimitsOutsideTheRangeAndAcceptsTheMaximum(t *testing.T) {
	list := seededList(t)
	for _, bad := range []int{-1, usecases.MaxPageSize + 1} {
		if _, err := list.Execute(bg, "", bad); !isErr(err, shared.ErrValidation) {
			t.Errorf("limit %d: %v", bad, err)
		}
	}
	if _, err := list.Execute(bg, "", usecases.MaxPageSize); err != nil {
		t.Errorf("limit %d: %v", usecases.MaxPageSize, err)
	}
}

func TestPublishBoundariesPublishesEachBoundaryOnce(t *testing.T) {
	e := newEnv(t)
	e.price("cat-1", 1000)
	from, to := t0.Add(2*time.Hour), t0.Add(4*time.Hour)
	if _, err := (usecases.ScheduleSale{Deps: e.deps}).Execute(bg, usecases.ScheduleSaleInput{CatalogItemID: "cat-1", Price: brl(700), From: from, To: to}); err != nil {
		t.Fatal(err)
	}
	policy := usecases.PublishBoundaries{Deps: e.deps}
	n, err := policy.Execute(bg)
	if err != nil || n != 0 || len(e.store.Events()) != 1 {
		t.Fatalf("before the window: %d, %v, events %v", n, err, eventNames(e))
	}
	e.clock.Set(from)
	if n, err = policy.Execute(bg); err != nil || n != 1 {
		t.Fatalf("at the opening: %d, %v", n, err)
	}
	if got := eventNames(e); len(got) != 2 {
		t.Fatalf("events = %v", got)
	}
	if n, err = policy.Execute(bg); err != nil || n != 0 || len(e.store.Events()) != 2 {
		t.Errorf("a second run of the same boundary: %d, %v, events %v", n, err, eventNames(e))
	}
	e.clock.Set(to)
	if n, err = policy.Execute(bg); err != nil || n != 1 || len(e.store.Events()) != 3 {
		t.Errorf("at the closing: %d, %v, events %v", n, err, eventNames(e))
	}
	got, _ := (usecases.GetItemPrice{Deps: e.deps}).Execute(bg, "cat-1")
	if got.EffectivePrice != brl(1000) {
		t.Errorf("effective price after the sale = %v", got.EffectivePrice)
	}
}

func TestPublishBoundariesSkipsAnItemThatChangedUnderneath(t *testing.T) {
	e := newEnv(t)
	e.price("cat-1", 1000)
	if _, err := (usecases.ScheduleSale{Deps: e.deps}).Execute(bg, usecases.ScheduleSaleInput{CatalogItemID: "cat-1", Price: brl(700), From: t0.Add(time.Hour), To: t0.Add(2 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	e.clock.Set(t0.Add(time.Hour))
	// the scan lists the item, then another writer withdraws it before the
	// policy reloads it: the policy skips it and the run still succeeds.
	racing := e.deps
	racing.ItemPrices = withdrawingRepo{ItemPriceRepository: e.deps.ItemPrices, onList: func() {
		if _, err := (usecases.WithdrawPrice{Deps: e.deps}).Execute(bg, "cat-1"); err != nil {
			t.Error(err)
		}
	}}
	n, err := (usecases.PublishBoundaries{Deps: racing}).Execute(bg)
	if err != nil || n != 0 {
		t.Errorf("Execute() = %d, %v; want 0 and no error", n, err)
	}
}

type withdrawingRepo struct {
	ports.ItemPriceRepository
	onList func()
}

func (r withdrawingRepo) DueForBoundary(ctx context.Context, now time.Time, limit int) ([]itemprice.ID, error) {
	ids, err := r.ItemPriceRepository.DueForBoundary(ctx, now, limit)
	r.onList()
	return ids, err
}

func TestPublishBoundariesReportsInfrastructureFailures(t *testing.T) {
	e := newEnv(t)
	boom := errors.New("db down")
	d := e.deps
	d.ItemPrices = failingPrices{ItemPriceRepository: e.deps.ItemPrices, dueErr: boom}
	if _, err := (usecases.PublishBoundaries{Deps: d}).Execute(bg); !isErr(err, boom) {
		t.Errorf("scan failure: %v", err)
	}
	e.price("cat-1", 1000)
	if _, err := (usecases.ScheduleSale{Deps: e.deps}).Execute(bg, usecases.ScheduleSaleInput{CatalogItemID: "cat-1", Price: brl(700), From: t0.Add(time.Hour), To: t0.Add(2 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	e.clock.Set(t0.Add(time.Hour))
	d.ItemPrices = failingPrices{ItemPriceRepository: e.deps.ItemPrices, saveErr: boom}
	if _, err := (usecases.PublishBoundaries{Deps: d}).Execute(bg); !isErr(err, boom) {
		t.Errorf("save failure: %v", err)
	}
}

type failingPrices struct {
	ports.ItemPriceRepository
	dueErr, saveErr error
}

func (f failingPrices) DueForBoundary(ctx context.Context, now time.Time, limit int) ([]itemprice.ID, error) {
	if f.dueErr != nil {
		return nil, f.dueErr
	}
	return f.ItemPriceRepository.DueForBoundary(ctx, now, limit)
}

func (f failingPrices) Save(ctx context.Context, p *itemprice.ItemPrice) error {
	if f.saveErr != nil {
		return f.saveErr
	}
	return f.ItemPriceRepository.Save(ctx, p)
}
