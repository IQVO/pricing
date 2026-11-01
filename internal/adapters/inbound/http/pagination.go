package http

import (
	"fmt"
	"net/http"

	"github.com/IQVO/pricing/internal/adapters/inbound/http/page"
	"github.com/IQVO/pricing/internal/application/views"
)

// pageParams reads the query of a collection GET: it answers 400
// unknown-query-parameter for any parameter that is not limit, cursor or one of
// the declared filters (a consumer still sending the retired `after` must fail
// loudly), invalid-limit for a limit outside 1..500 and invalid-cursor for a
// cursor that does not decode. It returns the limit and the id the page starts
// strictly after ("" for the first page), or false after writing the problem.
//
// The list is ordered by catalogItemId, which is unique: the keyset is that id
// as both sort key and id, so the cursor is the opaque page.Position of the last
// returned item.
func pageParams(w http.ResponseWriter, r *http.Request, filters ...string) (limit int, after string, ok bool) {
	if name := page.Unknown(r, filters...); name != "" {
		writeProblem(w, r, unknownQueryParameter, fmt.Sprintf("query parameter %q is not supported; page with limit and cursor", name))
		return 0, "", false
	}
	limit, err := page.Limit(r)
	if err != nil {
		writeProblem(w, r, invalidLimit, err.Error())
		return 0, "", false
	}
	pos, err := page.After(r)
	if err == nil && pos != nil && pos.Sort != pos.ID {
		err = page.ErrInvalidCursor
	}
	if err != nil {
		writeProblem(w, r, invalidCursor, err.Error())
		return 0, "", false
	}
	if pos != nil {
		after = pos.ID
	}
	return limit, after, true
}

// itemPricePageOut renders one use case page as the {items,nextCursor} envelope.
func itemPricePageOut(v views.ItemPriceList) page.Page[itemPriceDTO] {
	out := page.Page[itemPriceDTO]{Items: make([]itemPriceDTO, 0, len(v.Items))}
	for _, p := range v.Items {
		out.Items = append(out.Items, itemPriceOut(p))
	}
	if v.Next != "" {
		out.NextCursor = page.Encode(page.Position{Sort: v.Next, ID: v.Next})
	}
	return out
}
