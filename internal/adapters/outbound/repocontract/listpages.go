package repocontract

import (
	"slices"
	"testing"

	"github.com/IQVO/pricing/internal/domain/itemprice"
)

// listIDs reads one page and returns its ids.
func listIDs(t *testing.T, e Env, after itemprice.ID, limit int) []itemprice.ID {
	t.Helper()
	found, err := e.ItemPrices.List(ctx(), after, limit)
	fatalIf(t, err, "list item prices")
	ids := make([]itemprice.ID, 0, len(found))
	for _, p := range found {
		ids = append(ids, p.ID())
	}
	return ids
}

// testListPages is the keyset contract of GET /item-prices: the list is ordered
// by catalogItemId (the unique sort key and id), resumes strictly after the
// cursor id, and a walk of every page with any page size returns each row
// exactly once in the order of one unpaged read.
func testListPages(t *testing.T, e Env) {
	for _, id := range []string{"sku-1", "sku-10", "sku-2", "sku-9", "SKU-3", "sku-3", "sku-30", "sku-a"} {
		savedPrice(t, e, id, 1000)
	}
	whole := listIDs(t, e, "", 100)
	if len(whole) != 8 || !slices.Equal(whole, listIDs(t, e, "", 100)) {
		t.Fatalf("unpaged list = %v, want the 8 rows in a stable order", whole)
	}
	for _, limit := range []int{1, 2, 3, 7, 8, 9} {
		var walked []itemprice.ID
		after := itemprice.ID("")
		for pages := 0; ; pages++ {
			if pages > 20 {
				t.Fatalf("limit %d: the walk did not end", limit)
			}
			page := listIDs(t, e, after, limit)
			if len(page) > limit {
				t.Fatalf("limit %d: a page of %d rows", limit, len(page))
			}
			walked = append(walked, page...)
			if len(page) < limit {
				break
			}
			after = page[len(page)-1]
		}
		if !slices.Equal(walked, whole) {
			t.Fatalf("limit %d: walk = %v, want %v (no duplicate, no skipped row)", limit, walked, whole)
		}
	}
	if rest := listIDs(t, e, whole[len(whole)-1], 10); len(rest) != 0 {
		t.Fatalf("after the last row = %v, want nothing", rest)
	}
}
