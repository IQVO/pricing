package repocontract

import (
	"context"
	"errors"
	"testing"

	"github.com/IQVO/pricing/internal/application/ports"
)

var errBoom = errors.New("boom")

func wantErr(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("error = %v, want %v", got, want)
	}
}

func testInbox(t *testing.T, e Env) {
	first, err := e.Inbox.Claim(ctx(), "ev-1", "type.A")
	fatalIf(t, err, "claim")
	again, err := e.Inbox.Claim(ctx(), "ev-1", "type.A")
	fatalIf(t, err, "claim again")
	other, err := e.Inbox.Claim(ctx(), "ev-2", "type.A")
	fatalIf(t, err, "claim other")
	if !first || again || !other {
		t.Fatalf("claims = %v %v %v, want true false true", first, again, other)
	}
}

func testUnitOfWork(t *testing.T, e Env) {
	t.Run("a unit that fails rolls back every table", func(t *testing.T) { uowRollsBack(t, e) })
	t.Run("a unit that succeeds commits everything", func(t *testing.T) { uowCommits(t, e) })
	t.Run("a nested unit joins the outer one", func(t *testing.T) { uowNests(t, e) })
	t.Run("a nested unit that fails undoes only itself", func(t *testing.T) { uowNestedFailure(t, e) })
}

// uowNestedFailure: the use cases retry a whole attempt on ErrConcurrentUpdate,
// so a failed attempt must leave nothing behind inside the unit it joined.
func uowNestedFailure(t *testing.T, e Env) {
	err := e.UoW.Do(ctx(), func(c context.Context) error {
		fatalIf(t, e.ItemPrices.Save(c, newPrice(t, "sku-keep", 1000)), "outer write")
		inner := e.UoW.Do(c, func(c context.Context) error {
			fatalIf(t, e.ItemPrices.Save(c, newPrice(t, "sku-undone", 1000)), "inner write")
			return errBoom
		})
		wantErr(t, inner, errBoom)
		return nil
	})
	fatalIf(t, err, "outer unit")
	load(t, e, "sku-keep")
	_, err = e.ItemPrices.Get(ctx(), "sku-undone")
	wantErr(t, err, ports.ErrNotFound)
}

func uowRollsBack(t *testing.T, e Env) {
	err := e.UoW.Do(ctx(), func(c context.Context) error {
		fatalIf(t, e.ItemPrices.Save(c, newPrice(t, "sku-rb", 1000)), "save in unit")
		fatalIf(t, e.Catalog.MarkCreated(c, "sku-rb"), "catalog in unit")
		first, err := e.Inbox.Claim(c, "ev-rb", "type.A")
		fatalIf(t, err, "claim in unit")
		if !first {
			t.Fatal("the claim inside the unit must be the first")
		}
		fatalIf(t, e.Outbox.Append(c, priceEvents(t, "sku-rb")...), "append in unit")
		return errBoom
	})
	wantErr(t, err, errBoom)
	_, err = e.ItemPrices.Get(ctx(), "sku-rb")
	wantErr(t, err, ports.ErrNotFound)
	if s := catalogState(t, e, "sku-rb"); s != "UNKNOWN" {
		t.Fatalf("a rolled back catalog write is visible: %s", s)
	}
	if first, _ := e.Inbox.Claim(ctx(), "ev-rb", "type.A"); !first {
		t.Fatal("the rolled back claim must be free again")
	}
	if n := e.OutboxCount(t); n != 0 {
		t.Fatalf("outbox holds %d events after a rollback", n)
	}
}

func uowCommits(t *testing.T, e Env) {
	fatalIf(t, e.UoW.Do(ctx(), func(c context.Context) error {
		fatalIf(t, e.ItemPrices.Save(c, newPrice(t, "sku-ok", 1000)), "save in unit")
		return e.Outbox.Append(c, priceEvents(t, "sku-ok")...)
	}), "commit")
	load(t, e, "sku-ok")
	if n := e.OutboxCount(t); n != 1 {
		t.Fatalf("outbox holds %d events, want 1", n)
	}
}

func uowNests(t *testing.T, e Env) {
	err := e.UoW.Do(ctx(), func(c context.Context) error {
		fatalIf(t, e.UoW.Do(c, func(c context.Context) error { return e.ItemPrices.Save(c, newPrice(t, "sku-nest", 1000)) }), "inner")
		return errBoom
	})
	wantErr(t, err, errBoom)
	_, err = e.ItemPrices.Get(ctx(), "sku-nest")
	wantErr(t, err, ports.ErrNotFound)
}
