package http_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func quoteBodyFor(tier string) string {
	return `{"cartId":"cart-1","destinationPostalPrefix":"010",
 "lines":[{"catalogItemId":"sku-1","quantity":2},{"catalogItemId":"sku-2","quantity":1}],
 "groups":[{"groupNo":1,"offerId":"off-1","tier":"` + tier + `","lineNos":[1,2]}]}`
}

func groupsBytes(t *testing.T, r reply) string {
	t.Helper()
	var doc struct {
		Groups json.RawMessage `json:"groups"`
	}
	if err := json.Unmarshal([]byte(r.Body), &doc); err != nil {
		t.Fatal(err)
	}
	return string(doc.Groups)
}

// A CONSOLIDATED delivery is quoted free (ADR 0010) and a classic quote's bytes do
// not move: the same groups JSON as before, tier and charge only.
func TestAConsolidatedQuoteIsFreeAndAClassicOneIsByteIdentical(t *testing.T) {
	e := newEnv(t)
	e.priced("sku-1", 1000)
	e.priced("sku-2", 300)
	e.policies() // the schedule names the four classic tiers only
	classic := e.post("/price-quotes", quoteBodyFor("STANDARD"))
	e.wantStatus(classic, http.StatusCreated)
	if got, want := groupsBytes(t, classic), `[{"groupNo":1,"tier":"STANDARD","deliveryCharge":{"amountMinor":500,"currency":"BRL"}}]`; got != want {
		t.Fatalf("classic groups = %s, want %s", got, want)
	}
	free := e.post("/price-quotes", quoteBodyFor("CONSOLIDATED"))
	e.wantStatus(free, http.StatusCreated)
	if got, want := groupsBytes(t, free), `[{"groupNo":1,"tier":"CONSOLIDATED","deliveryCharge":{"amountMinor":0,"currency":"BRL"}}]`; got != want {
		t.Fatalf("CONSOLIDATED groups = %s, want %s", got, want)
	}
	// 2300 goods + 10% tax = 2530; a STANDARD group adds its 500 charge under the tax: 3080.
	if got := free.json(t)["totalMinor"].(float64); got != 2530 {
		t.Errorf("CONSOLIDATED total = %v, want 2530 (no delivery)", got)
	}
	if got := classic.json(t)["totalMinor"].(float64); got != 3080 {
		t.Errorf("classic total = %v, want 3080", got)
	}
	read := e.get("/price-quotes/" + free.json(t)["quoteId"].(string))
	e.wantStatus(read, http.StatusOK)
	if !strings.Contains(groupsBytes(t, read), `"tier":"CONSOLIDATED"`) {
		t.Errorf("the stored quote lost its tier: %s", read.Body)
	}
}

func TestAnUnknownTierIsStillRefusedOverHTTP(t *testing.T) {
	e := newEnv(t)
	e.priced("sku-1", 1000)
	e.priced("sku-2", 300)
	e.policies()
	for _, tier := range []string{"consolidated", "OVERNIGHT", ""} {
		e.wantProblem(e.post("/price-quotes", quoteBodyFor(tier)), http.StatusUnprocessableEntity, "validation-failed")
	}
}
