package repocontract

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/IQVO/pricing/internal/application/ports"
	"github.com/IQVO/pricing/internal/domain/itemprice"
)

// same fails when the two snapshots differ in any persisted field.
func samePrice(t *testing.T, got, want *itemprice.ItemPrice) {
	t.Helper()
	g, _ := json.Marshal(got.Snapshot())
	w, _ := json.Marshal(want.Snapshot())
	if string(g) != string(w) {
		t.Fatalf("round trip changed the item price:\n got %s\nwant %s", g, w)
	}
}

func load(t *testing.T, e Env, id string) *itemprice.ItemPrice {
	t.Helper()
	p, err := e.ItemPrices.Get(ctx(), itemprice.ID(id))
	fatalIf(t, err, "get item price "+id)
	return p
}

func testItemPrices(t *testing.T, e Env) {
	if _, err := e.ItemPrices.Get(ctx(), "nope"); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("Get of a missing item price = %v, want ErrNotFound", err)
	}
	p := savedPrice(t, e, "sku-1", 1000)
	if p.LoadedVersion() != p.Version() {
		t.Fatalf("Save must MarkPersisted: loaded %d, version %d", p.LoadedVersion(), p.Version())
	}
	samePrice(t, load(t, e, "sku-1"), p)

	again := newPrice(t, "sku-1", 1000)
	if err := e.ItemPrices.Save(ctx(), again); !errors.Is(err, ports.ErrConcurrentUpdate) {
		t.Fatalf("a second create of the same id = %v, want ErrConcurrentUpdate", err)
	}
}

func testBoundaries(t *testing.T, e Env) {
	p := savedPrice(t, e, "sku-1", 1000)
	_, err := p.ScheduleSale(itemprice.SaleWindow{SaleID: "s1", Price: brl(800), From: at(10), To: at(20)}, inputs(t0))
	fatalIf(t, err, "schedule sale")
	fatalIf(t, e.ItemPrices.Save(ctx(), p), "save with sale")
	samePrice(t, load(t, e, "sku-1"), p)

	for _, c := range []struct {
		min  int
		want int
	}{{5, 0}, {10, 1}, {60, 1}} {
		ids, err := e.ItemPrices.DueForBoundary(ctx(), at(c.min), 10)
		fatalIf(t, err, "due for boundary")
		if len(ids) != c.want {
			t.Fatalf("DueForBoundary(+%dm) = %v, want %d ids", c.min, ids, c.want)
		}
	}

	_, err = p.Reevaluate(at(10))
	fatalIf(t, err, "reevaluate at the start boundary")
	fatalIf(t, e.ItemPrices.Save(ctx(), p), "save reevaluated")
	if ids, _ := e.ItemPrices.DueForBoundary(ctx(), at(15), 10); len(ids) != 0 {
		t.Fatalf("a handled boundary is still due: %v", ids)
	}
	if ids, _ := e.ItemPrices.DueForBoundary(ctx(), at(20), 10); len(ids) != 1 {
		t.Fatalf("the end boundary is not due at +20m: %v", ids)
	}
}
