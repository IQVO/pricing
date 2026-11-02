package http_test

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"testing"

	"github.com/IQVO/pricing/internal/adapters/inbound/http/page"
)

// walkItemPrices follows nextCursor from the first page to the last and returns
// every catalogItemId in the order served and the number of pages.
func walkItemPrices(t *testing.T, e *env, limit int) (ids []string, pages int) {
	t.Helper()
	cursor := ""
	for {
		pages++
		if pages > 100 {
			t.Fatalf("limit %d: the walk did not end", limit)
		}
		path := fmt.Sprintf("/item-prices?limit=%d", limit)
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		r := e.get(path)
		e.wantStatus(r, http.StatusOK)
		body := r.json(t)
		if _, retired := body["next"]; retired {
			t.Fatalf("the retired next member is back: %s", r.Body)
		}
		items, ok := body["items"].([]any)
		if !ok || len(items) > limit {
			t.Fatalf("limit %d: items = %v", limit, body["items"])
		}
		for _, it := range items {
			ids = append(ids, it.(map[string]any)["catalogItemId"].(string))
		}
		next, _ := body["nextCursor"].(string)
		if next == "" {
			return ids, pages
		}
		cursor = next
	}
}

// More rows than limit: the walk of every page returns each row exactly once, in
// order, for several page sizes. The sort key is catalogItemId, which is unique,
// so the (sortKey, id) keyset is the id itself and no two rows tie.
func TestListItemPricesWalksEveryPage(t *testing.T) {
	e := newEnv(t)
	var want []string
	for i := 1; i <= 7; i++ {
		id := fmt.Sprintf("sku-%02d", i)
		e.priced(id, int64(100*i))
		want = append(want, id)
	}
	for limit, wantPages := range map[int]int{1: 7, 2: 4, 3: 3, 6: 2, 7: 1, 8: 1, 500: 1} {
		got, pages := walkItemPrices(t, e, limit)
		if !slices.Equal(got, want) || pages != wantPages {
			t.Fatalf("limit %d: %v in %d pages, want %v in %d", limit, got, pages, want, wantPages)
		}
	}
}

// A row created while a consumer is mid-walk is seen when it sorts after the
// cursor and is never repeated.
func TestListItemPricesWalkSeesRowsAfterTheCursor(t *testing.T) {
	e := newEnv(t)
	for _, id := range []string{"sku-1", "sku-3"} {
		e.priced(id, 100)
	}
	first := e.get("/item-prices?limit=1").json(t)
	e.priced("sku-2", 100)
	e.priced("sku-0", 100)
	next := first["nextCursor"].(string)
	second := e.get("/item-prices?limit=10&cursor=" + url.QueryEscape(next)).json(t)
	var got []string
	for _, it := range second["items"].([]any) {
		got = append(got, it.(map[string]any)["catalogItemId"].(string))
	}
	if !slices.Equal(got, []string{"sku-2", "sku-3"}) {
		t.Fatalf("second page = %v, want [sku-2 sku-3]", got)
	}
}

func TestListItemPricesEmptyIsAnEnvelope(t *testing.T) {
	e := newEnv(t)
	r := e.get("/item-prices")
	e.wantStatus(r, http.StatusOK)
	if r.Body != "{\"items\":[]}\n" {
		t.Fatalf("empty collection = %q", r.Body)
	}
}

func TestListItemPricesRejectsBadPagingAndRetiredParameters(t *testing.T) {
	e := newEnv(t)
	e.priced("sku-1", 100)
	for _, q := range []string{"limit=0", "limit=501", "limit=-1", "limit=abc", "limit=0x"} {
		e.wantProblem(e.get("/item-prices?"+q), http.StatusBadRequest, "invalid-limit")
	}
	wrongKey := page.Encode(page.Position{Sort: "a", ID: "b"}) // the sort key of this list is the id itself
	for _, c := range []string{"%25%25", "bm90LWpzb24", "e30", wrongKey} {
		e.wantProblem(e.get("/item-prices?cursor="+c), http.StatusBadRequest, "invalid-cursor")
	}
	// The retired paging parameters fail loudly instead of reading page one forever.
	for _, q := range []string{"after=sku-1", "offset=1", "nextToken=x", "from=sku-1", "page=2", "limit=5&after=sku-1"} {
		e.wantProblem(e.get("/item-prices?"+q), http.StatusBadRequest, "unknown-query-parameter")
	}
}
