package repocontract

import (
	"testing"

	"github.com/IQVO/pricing/internal/domain/quote"
	"github.com/IQVO/pricing/internal/domain/schedule"
	"github.com/IQVO/pricing/internal/domain/shared"
)

// testConsolidated pins ADR 0010 for every adapter: a schedule that declares a
// CONSOLIDATED row and a quote group on the CONSOLIDATED tier both round-trip.
func testConsolidated(t *testing.T, e Env) {
	tiers := append(fullTiers(), schedule.TierCharge{Tier: shared.TierConsolidated, Charge: brl(0)})
	s, _, err := schedule.Publish("sch-c", 1, tiers, 0, cur, t0)
	fatalIf(t, err, "publish a schedule declaring CONSOLIDATED")
	fatalIf(t, e.Schedules.Save(ctx(), s), "save the schedule")
	got, err := e.Schedules.Current(ctx())
	fatalIf(t, err, "current schedule")
	sameSchedule(t, got, s)
	if n := len(got.Tiers()); n != 5 {
		t.Fatalf("the stored schedule holds %d tiers, want 5", n)
	}

	table := publishTable(t, "tax-c", 1, 0)
	q, _, err := quote.Issue(quote.IssueInput{
		ID: "q-c", CartID: "cart-1", DestinationPostalPrefix: "012", Currency: cur, TTL: quoteTTL, Now: t0,
		Lines:  []quote.LineInput{{CatalogItemID: "sku-c", Quantity: 1, Unit: brl(1000), Priced: true, PriceVersion: 1}},
		Groups: []quote.GroupInput{{GroupNo: 1, OfferID: "offer-1", Tier: shared.TierConsolidated, LineNos: []int{1}}},
	}, got, table)
	fatalIf(t, err, "issue a CONSOLIDATED quote")
	fatalIf(t, e.Quotes.Save(ctx(), q), "save the quote")
	stored, err := e.Quotes.Get(ctx(), "q-c")
	fatalIf(t, err, "get the quote")
	sameQuote(t, stored, q)
	if g := stored.Groups()[0]; g.Tier != shared.TierConsolidated || g.DeliveryCharge.AmountMinor != 0 {
		t.Fatalf("stored group = %+v, want a free CONSOLIDATED group", g)
	}
}
