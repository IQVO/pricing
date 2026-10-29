package repocontract

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/IQVO/pricing/internal/application/ports"
	"github.com/IQVO/pricing/internal/domain/quote"
	"github.com/IQVO/pricing/internal/domain/shared"
)

const quoteTTL = 15 * time.Minute

func issueQuote(t *testing.T, id string) *quote.PriceQuote {
	t.Helper()
	sched := publishSchedule(t, "sch-q", 1, 0)
	table := publishTable(t, "tax-q", 1, 0)
	q, _, err := quote.Issue(quote.IssueInput{
		ID: quote.ID(id), CartID: "cart-1", DestinationPostalPrefix: "012", Currency: cur, TTL: quoteTTL, Now: t0,
		Lines: []quote.LineInput{
			{CatalogItemID: "sku-1", Quantity: 2, Unit: brl(1000), Priced: true, PriceVersion: 3},
			{CatalogItemID: "sku-2", Quantity: 1, Unit: brl(500), Priced: true, PriceVersion: 1},
		},
		Groups: []quote.GroupInput{{GroupNo: 1, OfferID: "offer-1", Tier: shared.TierStandard, LineNos: []int{1, 2}}},
	}, sched, table)
	fatalIf(t, err, "issue quote "+id)
	return q
}

func sameQuote(t *testing.T, got, want *quote.PriceQuote) {
	t.Helper()
	g, _ := json.Marshal(got.Snapshot())
	w, _ := json.Marshal(want.Snapshot())
	if string(g) != string(w) {
		t.Fatalf("round trip changed the quote:\n got %s\nwant %s", g, w)
	}
}

func testQuotes(t *testing.T, e Env) {
	if _, err := e.Quotes.Get(ctx(), "nope"); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("Get of a missing quote = %v, want ErrNotFound", err)
	}
	q := issueQuote(t, "q-1")
	fatalIf(t, e.Quotes.Save(ctx(), q), "save quote")
	if q.LoadedVersion() != q.Version() {
		t.Fatalf("Save must MarkPersisted: loaded %d, version %d", q.LoadedVersion(), q.Version())
	}
	got, err := e.Quotes.Get(ctx(), "q-1")
	fatalIf(t, err, "get quote")
	sameQuote(t, got, q)

	if err := e.Quotes.Save(ctx(), issueQuote(t, "q-1")); !errors.Is(err, ports.ErrConcurrentUpdate) {
		t.Fatalf("a second create of the same id = %v, want ErrConcurrentUpdate", err)
	}
	stale := issueQuote(t, "q-ghost")
	fatalIf(t, stale.Expire(at(60)), "expire the ghost")
	stale.MarkPersisted()
	if err := e.Quotes.Save(ctx(), stale); !errors.Is(err, ports.ErrConcurrentUpdate) {
		t.Fatalf("an update of a quote that is not stored = %v, want ErrConcurrentUpdate", err)
	}
}
