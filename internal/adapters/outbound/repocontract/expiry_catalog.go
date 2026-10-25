package repocontract

import (
	"testing"

	"github.com/IQVO/pricing/internal/domain/itemprice"
)

func testQuoteExpiry(t *testing.T, e Env) {
	q := issueQuote(t, "q-1")
	fatalIf(t, e.Quotes.Save(ctx(), q), "save quote")
	for _, c := range []struct {
		min  int
		want int
	}{{14, 0}, {15, 1}, {90, 1}} {
		ids, err := e.Quotes.DueForExpiry(ctx(), at(c.min), 10)
		fatalIf(t, err, "due for expiry")
		if len(ids) != c.want {
			t.Fatalf("DueForExpiry(+%dm) = %v, want %d ids", c.min, ids, c.want)
		}
	}
	fatalIf(t, q.Expire(at(15)), "expire")
	fatalIf(t, e.Quotes.Save(ctx(), q), "save expired")
	if ids, _ := e.Quotes.DueForExpiry(ctx(), at(90), 10); len(ids) != 0 {
		t.Fatalf("an EXPIRED quote is still due: %v", ids)
	}
	got, err := e.Quotes.Get(ctx(), "q-1")
	fatalIf(t, err, "get expired")
	sameQuote(t, got, q)
}

func catalogState(t *testing.T, e Env, id string) itemprice.CatalogState {
	t.Helper()
	s, err := e.Catalog.State(ctx(), itemprice.ID(id))
	fatalIf(t, err, "catalog state "+id)
	return s
}

func testCatalog(t *testing.T, e Env) {
	if s := catalogState(t, e, "sku-1"); s != itemprice.CatalogUnknown {
		t.Fatalf("a never seen item = %s, want UNKNOWN", s)
	}
	fatalIf(t, e.Catalog.MarkCreated(ctx(), "sku-1"), "mark created")
	fatalIf(t, e.Catalog.MarkCreated(ctx(), "sku-1"), "replayed creation")
	if s := catalogState(t, e, "sku-1"); s != itemprice.CatalogActive {
		t.Fatalf("created item = %s, want ACTIVE", s)
	}
	fatalIf(t, e.Catalog.MarkDiscontinued(ctx(), "sku-1"), "mark discontinued")
	if s := catalogState(t, e, "sku-1"); s != itemprice.CatalogDiscontinued {
		t.Fatalf("discontinued item = %s, want DISCONTINUED", s)
	}
	fatalIf(t, e.Catalog.MarkCreated(ctx(), "sku-1"), "creation replayed after discontinuation")
	if s := catalogState(t, e, "sku-1"); s != itemprice.CatalogDiscontinued {
		t.Fatalf("a replayed creation revived the item: %s", s)
	}
	fatalIf(t, e.Catalog.MarkDiscontinued(ctx(), "sku-9"), "discontinue an unseen item")
	if s := catalogState(t, e, "sku-9"); s != itemprice.CatalogDiscontinued {
		t.Fatalf("an unseen item discontinued = %s, want DISCONTINUED", s)
	}
}
