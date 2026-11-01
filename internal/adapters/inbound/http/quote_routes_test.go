package http_test

import (
	"fmt"
	"net/http"
	"testing"
)

const quoteBody = `{"cartId":"cart-1","destinationPostalPrefix":"010",
 "lines":[{"catalogItemId":"sku-1","quantity":2},{"catalogItemId":"sku-2","quantity":1}],
 "groups":[{"groupNo":1,"offerId":"off-1","tier":"STANDARD","lineNos":[1,2]}]}`

// policies publishes a charge schedule and a tax table.
func (e *env) policies() {
	e.t.Helper()
	tiers := fmt.Sprintf(tiersBody, money(500), money(9000))
	e.wantStatus(e.post("/delivery-charge-schedules", scheduleBody(1, tiers)), http.StatusCreated)
	bands := `[{"postalPrefixFrom":"000","postalPrefixTo":"999","rateBasisPoints":1000}]`
	e.wantStatus(e.post("/tax-tables", `{"version":1,"bands":`+bands+`}`), http.StatusCreated)
}

func TestIssueAndGetPriceQuote(t *testing.T) {
	e := newEnv(t)
	e.priced("sku-1", 1000)
	e.priced("sku-2", 300)
	e.policies()
	issued := e.post("/price-quotes", quoteBody)
	e.wantStatus(issued, http.StatusCreated)
	q := issued.json(t)
	// 2*1000 + 300 = 2300 goods, 500 delivery, 10% tax on the goods side.
	if q["state"] != "ISSUED" || q["totalMinor"].(float64) <= 2800 || len(q["lines"].([]any)) != 2 {
		t.Fatalf("quote = %s", issued.Body)
	}
	got := e.get("/price-quotes/" + q["quoteId"].(string))
	e.wantStatus(got, http.StatusOK)
	e.clock.Advance(16 * 60 * 1e9)
	late := e.get("/price-quotes/" + q["quoteId"].(string))
	if late.json(t)["state"] != "EXPIRED" {
		t.Fatalf("late quote = %s", late.Body)
	}
	e.wantProblem(e.get("/price-quotes/nope"), http.StatusNotFound, "not-found")
}

func TestIssuePriceQuoteRefusals(t *testing.T) {
	e := newEnv(t)
	e.priced("sku-1", 1000)
	one := `{"cartId":"c","destinationPostalPrefix":"010","lines":[{"catalogItemId":"sku-1","quantity":1}],"groups":[{"groupNo":1,"offerId":"o","tier":"STANDARD","lineNos":[1]}]}`
	e.wantProblem(e.post("/price-quotes", one), http.StatusUnprocessableEntity, "no-charge-schedule")
	e.policies()
	e.wantStatus(e.post("/price-quotes", one), http.StatusCreated)
	missing := `{"cartId":"c","destinationPostalPrefix":"010","lines":[{"catalogItemId":"ghost","quantity":1}],"groups":[{"groupNo":1,"offerId":"o","tier":"STANDARD","lineNos":[1]}]}`
	e.wantProblem(e.post("/price-quotes", missing), http.StatusUnprocessableEntity, "item-not-priced")
	e.wantProblem(e.post("/price-quotes", `{"cartId":"c","destinationPostalPrefix":"010","lines":[],"groups":[]}`), http.StatusUnprocessableEntity, "empty-quote")
	e.wantStatus(e.post("/item-prices/sku-1/withdrawals", ``), http.StatusOK)
	e.wantProblem(e.post("/price-quotes", one), http.StatusUnprocessableEntity, "item-not-priced")
}
