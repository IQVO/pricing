package usecases

import (
	"context"

	"github.com/IQVO/pricing/internal/application/views"
	"github.com/IQVO/pricing/internal/domain/itemprice"
	"github.com/IQVO/pricing/internal/domain/quote"
	"github.com/IQVO/pricing/internal/domain/shared"
)

// MaxPageSize is the largest page of GET /item-prices; DefaultPageSize is the
// page when limit is absent.
const (
	MaxPageSize     = 500
	DefaultPageSize = 100
)

// GetItemPrice is the query GetItemPrice (GET /item-prices/{catalogItemId}).
type GetItemPrice struct{ Deps }

// Execute returns the item price, or ports.ErrNotFound.
func (u GetItemPrice) Execute(ctx context.Context, catalogItemID string) (views.ItemPrice, error) {
	p, err := u.ItemPrices.Get(ctx, itemprice.ID(catalogItemID))
	if err != nil {
		return views.ItemPrice{}, err
	}
	return views.ItemPriceFrom(p, u.now()), nil
}

// ListItemPrices is the query ListItemPrices (GET /item-prices): bootstrap and
// rebuild of the projections only, unpaged-friendly (model-gap decision 6).
type ListItemPrices struct{ Deps }

// Execute returns one page ordered by catalogItemId, after the cursor. A
// limit of 0 is DefaultPageSize; one outside 1..MaxPageSize is
// shared.ErrValidation.
func (u ListItemPrices) Execute(ctx context.Context, after string, limit int) (views.ItemPriceList, error) {
	if limit == 0 {
		limit = DefaultPageSize
	}
	if limit < 1 || limit > MaxPageSize {
		return views.ItemPriceList{}, shared.Invalid("limit must be between 1 and %d", MaxPageSize)
	}
	// One more than the page tells whether another page follows.
	found, err := u.ItemPrices.List(ctx, itemprice.ID(after), limit+1)
	if err != nil {
		return views.ItemPriceList{}, err
	}
	out := views.ItemPriceList{Items: make([]views.ItemPrice, 0, limit)}
	now := u.now()
	for i, p := range found {
		if i == limit {
			out.Next = string(found[i-1].ID())
			break
		}
		out.Items = append(out.Items, views.ItemPriceFrom(p, now))
	}
	return out, nil
}

// GetCurrentChargeSchedule is the query GetCurrentChargeSchedule
// (GET /delivery-charge-schedules/current).
type GetCurrentChargeSchedule struct{ Deps }

// Execute returns the CURRENT schedule, or ports.ErrNotFound.
func (u GetCurrentChargeSchedule) Execute(ctx context.Context) (views.ChargeSchedule, error) {
	s, err := u.Schedules.Current(ctx)
	if err != nil {
		return views.ChargeSchedule{}, err
	}
	return views.ChargeScheduleFrom(s), nil
}

// GetCurrentTaxTable is the query GetCurrentTaxTable (GET /tax-tables/current).
type GetCurrentTaxTable struct{ Deps }

// Execute returns the CURRENT tax table, or ports.ErrNotFound.
func (u GetCurrentTaxTable) Execute(ctx context.Context) (views.TaxTable, error) {
	t, err := u.TaxTables.Current(ctx)
	if err != nil {
		return views.TaxTable{}, err
	}
	return views.TaxTableFrom(t), nil
}

// GetPriceQuote is the query GetPriceQuote (GET /price-quotes/{quoteId}).
type GetPriceQuote struct{ Deps }

// Execute returns the quote, ISSUED or EXPIRED at now, or ports.ErrNotFound.
func (u GetPriceQuote) Execute(ctx context.Context, quoteID string) (views.PriceQuote, error) {
	q, err := u.Quotes.Get(ctx, quote.ID(quoteID))
	if err != nil {
		return views.PriceQuote{}, err
	}
	return views.PriceQuoteFrom(q, u.now()), nil
}
