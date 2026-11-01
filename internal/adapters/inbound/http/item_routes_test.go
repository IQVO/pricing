package http_test

import (
	"fmt"
	"net/http"
	"testing"
)

func TestItemPriceLifecycleRoutes(t *testing.T) {
	e := newEnv(t)
	e.priced("sku-1", 1000)
	got := e.get("/item-prices/sku-1")
	e.wantStatus(got, http.StatusOK)
	if v := got.json(t); v["state"] != "ACTIVE" || v["version"].(float64) < 1 {
		t.Fatalf("item price = %s", got.Body)
	}
	set := e.put("/item-prices/sku-1/list-price", `{"listPrice":`+money(1200)+`}`)
	e.wantStatus(set, http.StatusOK)
	if lp := set.json(t)["listPrice"].(map[string]any)["amountMinor"]; lp != float64(1200) {
		t.Fatalf("listPrice = %v", lp)
	}
	if set.Header.Get("Idempotency-Replayed") != "" {
		t.Fatalf("a PUT is idempotent by method and never replays: %v", set.Header)
	}
	e.wantProblem(e.put("/item-prices/sku-1/list-price", `{"listPrice":{"amountMinor":-1,"currency":"BRL"}}`), http.StatusUnprocessableEntity, "negative-amount")
	e.wantProblem(e.put("/item-prices/sku-1/list-price", `{"listPrice":{"amountMinor":1,"currency":"USD"}}`), http.StatusUnprocessableEntity, "currency-mismatch")
	e.wantProblem(e.put("/item-prices/nope/list-price", `{"listPrice":`+money(1)+`}`), http.StatusNotFound, "not-found")
	w := e.post("/item-prices/sku-1/withdrawals", ``)
	e.wantStatus(w, http.StatusOK)
	if w.json(t)["state"] != "WITHDRAWN" {
		t.Fatalf("withdraw = %s", w.Body)
	}
	e.wantProblem(e.post("/item-prices/sku-1/withdrawals", ``), http.StatusConflict, "illegal-transition")
	e.wantProblem(e.put("/item-prices/sku-1/list-price", `{"listPrice":`+money(5)+`}`), http.StatusConflict, "illegal-transition")
	e.wantProblem(e.post("/item-prices/nope/withdrawals", ``), http.StatusNotFound, "not-found")
	e.wantProblem(e.get("/item-prices/nope"), http.StatusNotFound, "not-found")
}

func saleBody(price int64, from, to string) string {
	return fmt.Sprintf(`{"price":%s,"from":%q,"to":%q}`, money(price), from, to)
}

func TestSaleRoutes(t *testing.T) {
	e := newEnv(t)
	e.priced("sku-1", 1000)
	sale := e.post("/item-prices/sku-1/sales", saleBody(800, "2026-10-25T00:00:00Z", "2026-10-26T00:00:00Z"))
	e.wantStatus(sale, http.StatusCreated)
	id := sale.json(t)["saleId"].(string)
	e.wantProblem(e.post("/item-prices/sku-1/sales", saleBody(700, "2026-10-25T12:00:00Z", "2026-10-27T00:00:00Z")), http.StatusConflict, "overlapping-sale-window")
	e.wantProblem(e.post("/item-prices/sku-1/sales", saleBody(1000, "2026-11-01T00:00:00Z", "2026-11-02T00:00:00Z")), http.StatusUnprocessableEntity, "sale-not-below-list")
	e.wantProblem(e.post("/item-prices/nope/sales", saleBody(1, "2026-10-25T00:00:00Z", "2026-10-26T00:00:00Z")), http.StatusNotFound, "not-found")
	e.wantProblem(e.post("/item-prices/sku-1/sales", `{"price":`+money(1)+`,"from":"yesterday","to":"x"}`), http.StatusBadRequest, "malformed-request")

	cancelled := e.post("/item-prices/sku-1/sales/"+id+"/cancellations", ``)
	e.wantStatus(cancelled, http.StatusOK)
	if n := len(cancelled.json(t)["sales"].([]any)); n != 0 {
		t.Fatalf("sales after cancel = %d", n)
	}
	e.wantProblem(e.post("/item-prices/sku-1/sales/"+id+"/cancellations", ``), http.StatusNotFound, "not-found")
}
