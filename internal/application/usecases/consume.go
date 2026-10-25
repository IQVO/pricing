package usecases

import (
	"context"
	"errors"

	"github.com/IQVO/pricing/internal/application/ports"
	"github.com/IQVO/pricing/internal/domain/itemprice"
	"github.com/IQVO/pricing/internal/domain/shared"
)

// ErrInvalidEvent marks a consumed event that can never be applied (a
// catalogItemId that is not a valid id): deterministic, never retried.
var ErrInvalidEvent = errors.New("invalid event")

// Consumer applies the events pricing consumes from offer-availability. Every
// method claims the CloudEvents id and applies its effect in ONE unit of work,
// so a rollback un-claims the id and a redelivery converges.
type Consumer struct{ Deps }

// CatalogItemCreated projects CatalogItemView: the item is known and ACTIVE.
// It returns ErrInvalidEvent for an id the domain cannot hold, and a
// non-nil error otherwise only for a transient failure.
func (c Consumer) CatalogItemCreated(ctx context.Context, eventID, eventType, catalogItemID string) error {
	if !shared.ValidID(catalogItemID) {
		return ErrInvalidEvent
	}
	_, err := transact(ctx, c.UoW, consumerAttempts, func(ctx context.Context) (struct{}, error) {
		first, err := c.Inbox.Claim(ctx, eventID, eventType)
		if err != nil || !first {
			return struct{}{}, err
		}
		return struct{}{}, c.Catalog.MarkCreated(ctx, itemprice.ID(catalogItemID))
	})
	return err
}

// CatalogItemDiscontinued projects CatalogItemView (the item is DISCONTINUED)
// and runs the policy WithdrawOnDiscontinue: the item's price, when it has an
// ACTIVE one, is withdrawn and ItemPriceWithdrawn is published. An item that
// was never priced, or whose price is already withdrawn, only updates the view.
func (c Consumer) CatalogItemDiscontinued(ctx context.Context, eventID, eventType, catalogItemID string) error {
	if !shared.ValidID(catalogItemID) {
		return ErrInvalidEvent
	}
	_, err := transact(ctx, c.UoW, consumerAttempts, func(ctx context.Context) (struct{}, error) {
		first, err := c.Inbox.Claim(ctx, eventID, eventType)
		if err != nil || !first {
			return struct{}{}, err
		}
		id := itemprice.ID(catalogItemID)
		if err := c.Catalog.MarkDiscontinued(ctx, id); err != nil {
			return struct{}{}, err
		}
		p, err := c.ItemPrices.Get(ctx, id)
		if errors.Is(err, ports.ErrNotFound) {
			return struct{}{}, nil
		}
		if err != nil {
			return struct{}{}, err
		}
		if p.State() != itemprice.StateActive {
			return struct{}{}, nil
		}
		events, err := p.WithdrawPrice(c.now())
		if err != nil {
			return struct{}{}, err
		}
		if err := c.ItemPrices.Save(ctx, p); err != nil {
			return struct{}{}, err
		}
		return struct{}{}, c.publish(ctx, events)
	})
	return err
}

// PublishBoundaries is the policy PublishEffectivePriceOnWindowBoundary: for
// every item price with a sale window that started or ended since it was last
// evaluated it publishes ItemPriceChanged when the effective price moved,
// idempotent per item and boundary.
type PublishBoundaries struct{ Deps }

// Execute handles one batch and returns how many item prices it evaluated.
// An item another writer changed in between is skipped; the next pass sees it
// again.
func (u PublishBoundaries) Execute(ctx context.Context) (int, error) {
	ids, err := u.ItemPrices.DueForBoundary(ctx, u.now(), batchSize)
	if err != nil {
		return 0, err
	}
	handled := 0
	for _, id := range ids {
		_, err := transact(ctx, u.UoW, commandAttempts, func(ctx context.Context) (struct{}, error) {
			p, err := u.ItemPrices.Get(ctx, id)
			if err != nil {
				return struct{}{}, err
			}
			events, err := p.Reevaluate(u.now())
			if err != nil {
				return struct{}{}, err
			}
			if err := u.ItemPrices.Save(ctx, p); err != nil {
				return struct{}{}, err
			}
			return struct{}{}, u.publish(ctx, events)
		})
		switch {
		case err == nil:
			handled++
		case errors.Is(err, ports.ErrNotFound), errors.Is(err, ports.ErrConcurrentUpdate), errors.Is(err, shared.ErrIllegalTransition):
			u.logger().WarnContext(ctx, "boundary publication skipped", "catalog_item_id", string(id), "error", err)
		default:
			return handled, err
		}
	}
	return handled, nil
}
