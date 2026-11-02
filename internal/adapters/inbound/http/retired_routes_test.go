package http_test

import (
	"net/http"
	"testing"
)

// TestRetiredRoutesAreGone pins the REST guideline cutover: no :custom-method
// route and no alias of one answers any more (404 or 405).
func TestRetiredRoutesAreGone(t *testing.T) {
	e := newEnv(t)
	e.priced("sku-1", 1000)
	retired := []struct{ method, path string }{
		{http.MethodPost, "/item-prices/sku-1:set-list-price"},
		{http.MethodPut, "/item-prices/sku-1:set-list-price"},
		{http.MethodPost, "/item-prices/sku-1:withdraw"},
		{http.MethodPost, "/item-prices/sku-1/sales/sale-1:cancel"},
		// The act is no longer a POST on the attribute either.
		{http.MethodPost, "/item-prices/sku-1/list-price"},
	}
	for _, c := range retired {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			r := e.do(c.method, c.path, "retired-key", `{"listPrice":`+money(1)+`}`)
			if r.Status != http.StatusNotFound && r.Status != http.StatusMethodNotAllowed {
				t.Fatalf("%s %s answered %d, want 404 or 405: %s", c.method, c.path, r.Status, r.Body)
			}
		})
	}
}
