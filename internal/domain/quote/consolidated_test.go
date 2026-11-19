package quote_test

import (
	"errors"
	"testing"

	"github.com/IQVO/pricing/internal/domain/quote"
	"github.com/IQVO/pricing/internal/domain/schedule"
	"github.com/IQVO/pricing/internal/domain/shared"
)

func consolidatedInput() quote.IssueInput {
	in := input()
	in.Groups = []quote.GroupInput{
		{GroupNo: 1, OfferID: "off-1", Tier: shared.TierConsolidated, LineNos: []int{1}},
		{GroupNo: 2, OfferID: "off-2", Tier: shared.TierTwoDay, LineNos: []int{2}},
	}
	return in
}

// A CONSOLIDATED delivery is quoted free (ADR 0010); the classic group beside it
// keeps its charge, so the totals differ from input()'s only by group 1's 1490.
func TestAConsolidatedGroupIsQuotedFree(t *testing.T) {
	q := mustIssue(t, consolidatedInput())
	g := q.Groups()
	if g[0].Tier != shared.TierConsolidated || g[0].DeliveryCharge != brl(0) {
		t.Errorf("group 1 = %+v, want CONSOLIDATED at 0", g[0])
	}
	if g[1].Tier != shared.TierTwoDay || g[1].DeliveryCharge != brl(990) {
		t.Errorf("group 2 = %+v, want TWO_DAY at 990", g[1])
	}
	// base 22345 + 0 + 990 = 23335; tax 10% = 2333.5 -> 2334; total 25669.
	verify(t, "totals", q, check{"tax", q.TaxMinor() == 2334}, check{"total", q.TotalMinor() == 25669})
}

func TestAQuoteOfOnlyConsolidatedGroupsChargesNoDelivery(t *testing.T) {
	in := input()
	in.Groups = []quote.GroupInput{{GroupNo: 1, OfferID: "off-1", Tier: shared.TierConsolidated, LineNos: []int{1, 2}}}
	q := mustIssue(t, in)
	// goods 22345 + 0 delivery; tax 2234.5 -> 2235; total 24580.
	verify(t, "totals", q, check{"tax", q.TaxMinor() == 2235}, check{"total", q.TotalMinor() == 24580})
}

func TestADeclaredConsolidatedRowPricesTheGroup(t *testing.T) {
	tiers := []schedule.TierCharge{
		{Tier: shared.TierSameDay, Charge: brl(2990)},
		{Tier: shared.TierNextDay, Charge: brl(1490), FreeOver: brl(30000)},
		{Tier: shared.TierTwoDay, Charge: brl(990)},
		{Tier: shared.TierStandard, Charge: brl(500), FreeOver: brl(15000)},
		{Tier: shared.TierConsolidated, Charge: brl(390), FreeOver: brl(12000)},
	}
	s, _, err := schedule.Publish("sch-2", 8, tiers, 7, cur, t0)
	if err != nil {
		t.Fatal(err)
	}
	q, _, err := quote.Issue(consolidatedInput(), s, table(t)) // group 1 goods 10000 < 12000
	if err != nil {
		t.Fatal(err)
	}
	if got := q.Groups()[0].DeliveryCharge; got != brl(390) {
		t.Errorf("a declared CONSOLIDATED row must be honoured, got %v", got)
	}
}

func TestAnUnknownTierIsStillRefusedInAQuote(t *testing.T) {
	for _, tier := range []shared.Tier{"", "consolidated", "WEEKLY"} {
		in := consolidatedInput()
		in.Groups[0].Tier = tier
		if _, _, err := quote.Issue(in, sched(t), table(t)); !errors.Is(err, shared.ErrValidation) {
			t.Errorf("Issue() with tier %q = %v, want ErrValidation", tier, err)
		}
	}
}

func TestAConsolidatedQuoteRestores(t *testing.T) {
	q := mustIssue(t, consolidatedInput())
	got, err := quote.Restore(q.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if g := got.Groups()[0]; g.Tier != shared.TierConsolidated || g.DeliveryCharge != brl(0) {
		t.Errorf("restored group = %+v", g)
	}
}
