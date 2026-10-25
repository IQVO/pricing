package usecases

import (
	"context"
	"errors"
	"time"

	"github.com/IQVO/pricing/internal/application/ports"
	"github.com/IQVO/pricing/internal/application/views"
	"github.com/IQVO/pricing/internal/domain/itemprice"
	"github.com/IQVO/pricing/internal/domain/shared"
)

// CreateItemPrice is the command SetListPrice on an item without a price
// (POST /item-prices): it creates the ItemPrice of a catalogue item the local
// CatalogItemView knows. A second create for the same item is
// ports.ErrConcurrentUpdate.
type CreateItemPrice struct{ Deps }

// CreateItemPriceInput is the body of POST /item-prices.
type CreateItemPriceInput struct {
	CatalogItemID string
	ListPrice     shared.Money
}

// Execute creates the item price and publishes ItemPriceChanged.
func (u CreateItemPrice) Execute(ctx context.Context, in CreateItemPriceInput) (views.ItemPrice, error) {
	return transact(ctx, u.UoW, commandAttempts, func(ctx context.Context) (views.ItemPrice, error) {
		id := itemprice.ID(in.CatalogItemID)
		catalog, err := u.Catalog.State(ctx, id)
		if err != nil {
			return views.ItemPrice{}, err
		}
		now := u.now()
		p, events, err := itemprice.Create(id, in.ListPrice, itemprice.Inputs{Currency: u.Currency, Catalog: catalog, Now: now})
		if err != nil {
			return views.ItemPrice{}, err
		}
		return u.persist(ctx, p, events, now)
	})
}

// persist saves p, appends its events and returns its view.
func (d Deps) persist(ctx context.Context, p *itemprice.ItemPrice, events []shared.Event, now time.Time) (views.ItemPrice, error) {
	if err := d.ItemPrices.Save(ctx, p); err != nil {
		return views.ItemPrice{}, err
	}
	if err := d.publish(ctx, events); err != nil {
		return views.ItemPrice{}, err
	}
	return views.ItemPriceFrom(p, now), nil
}

// SetListPrice is the command SetListPrice on a priced item
// (POST /item-prices/{catalogItemId}:set-list-price).
type SetListPrice struct{ Deps }

// SetListPriceInput carries the new list price and the optional optimistic
// guard: a ExpectedVersion above zero must equal the stored version.
type SetListPriceInput struct {
	CatalogItemID   string
	ListPrice       shared.Money
	ExpectedVersion int64
}

// Execute changes the list price and publishes ItemPriceChanged when the
// published view moved.
func (u SetListPrice) Execute(ctx context.Context, in SetListPriceInput) (views.ItemPrice, error) {
	return transact(ctx, u.UoW, commandAttempts, func(ctx context.Context) (views.ItemPrice, error) {
		id := itemprice.ID(in.CatalogItemID)
		p, err := u.ItemPrices.Get(ctx, id)
		if err != nil {
			return views.ItemPrice{}, err
		}
		if in.ExpectedVersion > 0 && in.ExpectedVersion != p.Version() {
			return views.ItemPrice{}, ports.ErrConcurrentUpdate
		}
		catalog, err := u.Catalog.State(ctx, id)
		if err != nil {
			return views.ItemPrice{}, err
		}
		now := u.now()
		events, err := p.SetListPrice(in.ListPrice, itemprice.Inputs{Currency: u.Currency, Catalog: catalog, Now: now})
		if err != nil {
			return views.ItemPrice{}, err
		}
		return u.persist(ctx, p, events, now)
	})
}

// ScheduleSale is the command ScheduleSale
// (POST /item-prices/{catalogItemId}/sales).
type ScheduleSale struct{ Deps }

// ScheduleSaleInput is the body of POST /item-prices/{catalogItemId}/sales.
type ScheduleSaleInput struct {
	CatalogItemID string
	Price         shared.Money
	From          time.Time
	To            time.Time
}

// Execute adds the window and returns it; ItemPriceChanged is published when
// the window is already open.
func (u ScheduleSale) Execute(ctx context.Context, in ScheduleSaleInput) (views.SaleWindow, error) {
	return transact(ctx, u.UoW, commandAttempts, func(ctx context.Context) (views.SaleWindow, error) {
		id := itemprice.ID(in.CatalogItemID)
		p, err := u.ItemPrices.Get(ctx, id)
		if err != nil {
			return views.SaleWindow{}, err
		}
		catalog, err := u.Catalog.State(ctx, id)
		if err != nil {
			return views.SaleWindow{}, err
		}
		now := u.now()
		w := itemprice.SaleWindow{SaleID: u.IDs.NewSaleID(), Price: in.Price, From: in.From, To: in.To}
		events, err := p.ScheduleSale(w, itemprice.Inputs{Currency: u.Currency, Catalog: catalog, Now: now})
		if err != nil {
			return views.SaleWindow{}, err
		}
		if _, err := u.persist(ctx, p, events, now); err != nil {
			return views.SaleWindow{}, err
		}
		for _, s := range p.Sales() {
			if s.SaleID == w.SaleID {
				return views.SaleWindowFrom(s), nil
			}
		}
		return views.SaleWindow{}, errors.New("scheduled sale window is missing from the item price")
	})
}

// CancelSale is the command CancelSale
// (POST /item-prices/{catalogItemId}/sales/{saleId}:cancel). It has no event of
// its own: cancelling the live sale reverts the effective price, which is
// published as ItemPriceChanged.
type CancelSale struct{ Deps }

// Execute removes the window.
func (u CancelSale) Execute(ctx context.Context, catalogItemID, saleID string) (views.ItemPrice, error) {
	return transact(ctx, u.UoW, commandAttempts, func(ctx context.Context) (views.ItemPrice, error) {
		p, err := u.ItemPrices.Get(ctx, itemprice.ID(catalogItemID))
		if err != nil {
			return views.ItemPrice{}, err
		}
		now := u.now()
		events, err := p.CancelSale(saleID, now)
		if err != nil {
			return views.ItemPrice{}, err
		}
		return u.persist(ctx, p, events, now)
	})
}

// WithdrawPrice is the command WithdrawPrice
// (POST /item-prices/{catalogItemId}:withdraw).
type WithdrawPrice struct{ Deps }

// Execute withdraws the price and publishes ItemPriceWithdrawn.
func (u WithdrawPrice) Execute(ctx context.Context, catalogItemID string) (views.ItemPrice, error) {
	return transact(ctx, u.UoW, commandAttempts, func(ctx context.Context) (views.ItemPrice, error) {
		p, err := u.ItemPrices.Get(ctx, itemprice.ID(catalogItemID))
		if err != nil {
			return views.ItemPrice{}, err
		}
		now := u.now()
		events, err := p.WithdrawPrice(now)
		if err != nil {
			return views.ItemPrice{}, err
		}
		return u.persist(ctx, p, events, now)
	})
}
