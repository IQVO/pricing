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

const (
	typeCreated      = "com.retailnetwork.commerce.offer-availability.catalogitem.CatalogItemCreated"
	typeDiscontinued = "com.retailnetwork.commerce.offer-availability.catalogitem.CatalogItemDiscontinued"
)

func TestCatalogItemCreatedProjectsTheViewIdempotently(t *testing.T) {
	e := newEnv(t)
	c := usecases.Consumer{Deps: e.deps}
	for i := 0; i < 2; i++ {
		if err := c.CatalogItemCreated(bg, "evt-1", typeCreated, "cat-1"); err != nil {
			t.Fatal(err)
		}
	}
	st, _ := e.deps.Catalog.State(bg, "cat-1")
	if st != itemprice.CatalogActive {
		t.Errorf("state = %s", st)
	}
	if n := e.store.Counts(); n.Catalog != 1 || n.Inbox != 1 {
		t.Errorf("counts = %+v", n)
	}
}

func TestCatalogItemDiscontinuedWithdrawsThePrice(t *testing.T) {
	e := newEnv(t)
	e.price("cat-1", 1000)
	c := usecases.Consumer{Deps: e.deps}
	for i := 0; i < 2; i++ { // the second delivery has the same id
		if err := c.CatalogItemDiscontinued(bg, "evt-d1", typeDiscontinued, "cat-1"); err != nil {
			t.Fatal(err)
		}
	}
	p, err := usecases.GetItemPrice{Deps: e.deps}.Execute(bg, "cat-1")
	if err != nil || p.State != "WITHDRAWN" {
		t.Fatalf("price = %+v, %v", p, err)
	}
	if got := eventNames(e); !equalStrings(got, []string{"ItemPriceChanged", "ItemPriceWithdrawn"}) {
		t.Errorf("events = %v", got)
	}
	st, _ := e.deps.Catalog.State(bg, "cat-1")
	if st != itemprice.CatalogDiscontinued {
		t.Errorf("state = %s", st)
	}
	// a distinct event for the same, already withdrawn item only touches the view.
	if err := c.CatalogItemDiscontinued(bg, "evt-d2", typeDiscontinued, "cat-1"); err != nil {
		t.Fatal(err)
	}
	if len(e.store.Events()) != 2 {
		t.Errorf("events = %v", eventNames(e))
	}
}

func TestCatalogItemDiscontinuedForAnUnpricedItemOnlyUpdatesTheView(t *testing.T) {
	e := newEnv(t)
	c := usecases.Consumer{Deps: e.deps}
	if err := c.CatalogItemDiscontinued(bg, "evt-d1", typeDiscontinued, "never-priced"); err != nil {
		t.Fatal(err)
	}
	st, _ := e.deps.Catalog.State(bg, "never-priced")
	if st != itemprice.CatalogDiscontinued || len(e.store.Events()) != 0 {
		t.Errorf("state %s events %v", st, eventNames(e))
	}
	// a replayed creation must not revive it.
	if err := c.CatalogItemCreated(bg, "evt-c1", typeCreated, "never-priced"); err != nil {
		t.Fatal(err)
	}
	if st, _ = e.deps.Catalog.State(bg, "never-priced"); st != itemprice.CatalogDiscontinued {
		t.Errorf("state after a late creation = %s", st)
	}
}

func TestConsumerRefusesAnInvalidIdWithoutClaimingTheEvent(t *testing.T) {
	e := newEnv(t)
	c := usecases.Consumer{Deps: e.deps}
	if err := c.CatalogItemCreated(bg, "evt-1", typeCreated, "bad id"); !isErr(err, usecases.ErrInvalidEvent) {
		t.Errorf("created: %v", err)
	}
	if err := c.CatalogItemDiscontinued(bg, "evt-2", typeDiscontinued, ""); !isErr(err, usecases.ErrInvalidEvent) {
		t.Errorf("discontinued: %v", err)
	}
	if n := e.store.Counts(); n.Inbox != 0 || n.Catalog != 0 {
		t.Errorf("counts = %+v", n)
	}
}

// failingOutbox fails the append, the LAST write of the discontinuation unit.
type failingOutbox struct{ err error }

func (f failingOutbox) Append(context.Context, ...shared.Event) error { return f.err }

func TestDiscontinuationIsAtomic(t *testing.T) {
	e := newEnv(t)
	e.price("cat-1", 1000)
	boom := errors.New("kafka outbox down")
	d := e.deps
	d.Outbox = failingOutbox{err: boom}
	before := e.store.Counts()
	err := usecases.Consumer{Deps: d}.CatalogItemDiscontinued(bg, "evt-d1", typeDiscontinued, "cat-1")
	if !isErr(err, boom) {
		t.Fatalf("err = %v, want the outbox failure", err)
	}
	// the claim, the view update and the withdrawal all rolled back.
	if e.store.Counts() != before {
		t.Errorf("counts after the failed unit = %+v, want %+v", e.store.Counts(), before)
	}
	st, _ := e.deps.Catalog.State(bg, "cat-1")
	if st != itemprice.CatalogActive {
		t.Errorf("view state = %s, want ACTIVE (rolled back)", st)
	}
	// the redelivery converges.
	if err := (usecases.Consumer{Deps: e.deps}).CatalogItemDiscontinued(bg, "evt-d1", typeDiscontinued, "cat-1"); err != nil {
		t.Fatal(err)
	}
	p, _ := usecases.GetItemPrice{Deps: e.deps}.Execute(bg, "cat-1")
	if p.State != "WITHDRAWN" {
		t.Errorf("state = %s", p.State)
	}
}

func TestCommandsRollBackWhenTheOutboxFails(t *testing.T) {
	e := newEnv(t)
	e.catalogCreated("cat-1")
	d := e.deps
	d.Outbox = failingOutbox{err: errors.New("outbox down")}
	before := e.store.Counts()
	if _, err := (usecases.CreateItemPrice{Deps: d}).Execute(bg, usecases.CreateItemPriceInput{CatalogItemID: "cat-1", ListPrice: brl(1)}); err == nil {
		t.Fatal("want the outbox failure")
	}
	if e.store.Counts() != before {
		t.Errorf("the aggregate was saved without its event: %+v", e.store.Counts())
	}
}

func TestConsumerRetriesATransientConflict(t *testing.T) {
	e := newEnv(t)
	e.price("cat-1", 1000)
	flaky := &flakyPrices{ItemPriceRepository: e.deps.ItemPrices, failures: 2}
	d := e.deps
	d.ItemPrices = flaky
	if err := (usecases.Consumer{Deps: d}).CatalogItemDiscontinued(bg, "evt-d1", typeDiscontinued, "cat-1"); err != nil {
		t.Fatalf("a conflict that clears must be retried: %v", err)
	}
	p, _ := usecases.GetItemPrice{Deps: e.deps}.Execute(bg, "cat-1")
	if p.State != "WITHDRAWN" || flaky.calls < 3 {
		t.Errorf("state %s after %d saves", p.State, flaky.calls)
	}
}

type flakyPrices struct {
	ports.ItemPriceRepository
	failures, calls int
}

func (f *flakyPrices) Save(ctx context.Context, p *itemprice.ItemPrice) error {
	f.calls++
	if f.failures > 0 {
		f.failures--
		return ports.ErrConcurrentUpdate
	}
	return f.ItemPriceRepository.Save(ctx, p)
}

func TestCommandsRetryOnceOnAConflictThenReportIt(t *testing.T) {
	e := newEnv(t)
	e.price("cat-1", 1000)
	flaky := &flakyPrices{ItemPriceRepository: e.deps.ItemPrices, failures: 1}
	d := e.deps
	d.ItemPrices = flaky
	e.clock.Advance(time.Hour)
	if _, err := (usecases.SetListPrice{Deps: d}).Execute(bg, usecases.SetListPriceInput{CatalogItemID: "cat-1", ListPrice: brl(1100)}); err != nil {
		t.Errorf("one conflict is retried: %v", err)
	}
	flaky.failures = 2
	if _, err := (usecases.SetListPrice{Deps: d}).Execute(bg, usecases.SetListPriceInput{CatalogItemID: "cat-1", ListPrice: brl(1200)}); !isErr(err, ports.ErrConcurrentUpdate) {
		t.Errorf("two conflicts are reported: %v", err)
	}
}
