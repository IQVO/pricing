package memory

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/IQVO/pricing/internal/application/ports"
	"github.com/IQVO/pricing/internal/domain/itemprice"
	"github.com/IQVO/pricing/internal/domain/quote"
	"github.com/IQVO/pricing/internal/domain/schedule"
	"github.com/IQVO/pricing/internal/domain/shared"
	"github.com/IQVO/pricing/internal/domain/tax"
)

var (
	_ ports.ItemPriceRepository = (*ItemPriceRepository)(nil)
	_ ports.ScheduleRepository  = (*ScheduleRepository)(nil)
	_ ports.TaxTableRepository  = (*TaxTableRepository)(nil)
	_ ports.QuoteRepository     = (*QuoteRepository)(nil)
	_ ports.CatalogView         = (*CatalogView)(nil)
	_ ports.Inbox               = (*Inbox)(nil)
	_ ports.EventOutbox         = (*Outbox)(nil)
	_ ports.UnitOfWork          = (*UnitOfWork)(nil)
	_ ports.IdempotencyStore    = (*IdempotencyStore)(nil)
)

// ItemPriceRepository is the in-memory ports.ItemPriceRepository.
type ItemPriceRepository struct{ s *Store }

// Get implements ports.ItemPriceRepository.
func (r *ItemPriceRepository) Get(ctx context.Context, id itemprice.ID) (*itemprice.ItemPrice, error) {
	defer r.s.enter(ctx)()
	snap, ok := r.s.prices[id]
	if !ok {
		return nil, fmt.Errorf("item price %s: %w", id, ports.ErrNotFound)
	}
	return itemprice.Restore(snap)
}

// Save implements ports.ItemPriceRepository.
func (r *ItemPriceRepository) Save(ctx context.Context, p *itemprice.ItemPrice) error {
	defer r.s.enter(ctx)()
	stored, exists := r.s.prices[p.ID()]
	if p.LoadedVersion() == 0 && exists || p.LoadedVersion() > 0 && (!exists || stored.Version != p.LoadedVersion()) {
		return fmt.Errorf("item price %s loaded at version %d: %w", p.ID(), p.LoadedVersion(), ports.ErrConcurrentUpdate)
	}
	r.s.prices[p.ID()] = p.Snapshot()
	p.MarkPersisted()
	return nil
}

// List implements ports.ItemPriceRepository.
func (r *ItemPriceRepository) List(ctx context.Context, after itemprice.ID, limit int) ([]*itemprice.ItemPrice, error) {
	defer r.s.enter(ctx)()
	ids := make([]itemprice.ID, 0, len(r.s.prices))
	for id := range r.s.prices {
		if id > after {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	if len(ids) > limit {
		ids = ids[:limit]
	}
	out := make([]*itemprice.ItemPrice, 0, len(ids))
	for _, id := range ids {
		p, err := itemprice.Restore(r.s.prices[id])
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// DueForBoundary implements ports.ItemPriceRepository.
func (r *ItemPriceRepository) DueForBoundary(ctx context.Context, now time.Time, limit int) ([]itemprice.ID, error) {
	defer r.s.enter(ctx)()
	type due struct {
		id itemprice.ID
		at time.Time
	}
	var found []due
	for id, snap := range r.s.prices {
		if b := snap.NextBoundary(); !b.IsZero() && !b.After(now) {
			found = append(found, due{id, b})
		}
	}
	sort.Slice(found, func(i, j int) bool {
		if found[i].at.Equal(found[j].at) {
			return found[i].id < found[j].id
		}
		return found[i].at.Before(found[j].at)
	})
	if len(found) > limit {
		found = found[:limit]
	}
	out := make([]itemprice.ID, len(found))
	for i, d := range found {
		out[i] = d.id
	}
	return out, nil
}

// ScheduleRepository is the in-memory ports.ScheduleRepository.
type ScheduleRepository struct{ s *Store }

// Current implements ports.ScheduleRepository.
func (r *ScheduleRepository) Current(ctx context.Context) (*schedule.DeliveryChargeSchedule, error) {
	defer r.s.enter(ctx)()
	for _, snap := range r.s.schedules {
		if snap.State == schedule.StateCurrent {
			return schedule.Restore(snap)
		}
	}
	return nil, fmt.Errorf("current delivery charge schedule: %w", ports.ErrNotFound)
}

// Save implements ports.ScheduleRepository.
func (r *ScheduleRepository) Save(ctx context.Context, sc *schedule.DeliveryChargeSchedule) error {
	defer r.s.enter(ctx)()
	snap := sc.Snapshot()
	if _, exists := r.s.schedules[snap.ID]; !exists {
		for _, o := range r.s.schedules {
			if o.Version == snap.Version || o.State == schedule.StateCurrent && snap.State == schedule.StateCurrent {
				return fmt.Errorf("schedule %s version %d: %w", snap.ID, snap.Version, ports.ErrConcurrentUpdate)
			}
		}
	}
	r.s.schedules[snap.ID] = snap
	return nil
}

// TaxTableRepository is the in-memory ports.TaxTableRepository.
type TaxTableRepository struct{ s *Store }

// Current implements ports.TaxTableRepository.
func (r *TaxTableRepository) Current(ctx context.Context) (*tax.TaxTable, error) {
	defer r.s.enter(ctx)()
	for _, snap := range r.s.taxTables {
		if snap.State == tax.StateCurrent {
			return tax.Restore(snap)
		}
	}
	return nil, fmt.Errorf("current tax table: %w", ports.ErrNotFound)
}

// Save implements ports.TaxTableRepository.
func (r *TaxTableRepository) Save(ctx context.Context, t *tax.TaxTable) error {
	defer r.s.enter(ctx)()
	snap := t.Snapshot()
	if _, exists := r.s.taxTables[snap.ID]; !exists {
		for _, o := range r.s.taxTables {
			if o.Version == snap.Version || o.State == tax.StateCurrent && snap.State == tax.StateCurrent {
				return fmt.Errorf("tax table %s version %d: %w", snap.ID, snap.Version, ports.ErrConcurrentUpdate)
			}
		}
	}
	r.s.taxTables[snap.ID] = snap
	return nil
}

// QuoteRepository is the in-memory ports.QuoteRepository.
type QuoteRepository struct{ s *Store }

// Get implements ports.QuoteRepository.
func (r *QuoteRepository) Get(ctx context.Context, id quote.ID) (*quote.PriceQuote, error) {
	defer r.s.enter(ctx)()
	snap, ok := r.s.quotes[id]
	if !ok {
		return nil, fmt.Errorf("quote %s: %w", id, ports.ErrNotFound)
	}
	return quote.Restore(snap)
}

// Save implements ports.QuoteRepository.
func (r *QuoteRepository) Save(ctx context.Context, q *quote.PriceQuote) error {
	defer r.s.enter(ctx)()
	stored, exists := r.s.quotes[q.ID()]
	if q.LoadedVersion() == 0 && exists || q.LoadedVersion() > 0 && (!exists || stored.Version != q.LoadedVersion()) {
		return fmt.Errorf("quote %s loaded at version %d: %w", q.ID(), q.LoadedVersion(), ports.ErrConcurrentUpdate)
	}
	r.s.quotes[q.ID()] = q.Snapshot()
	q.MarkPersisted()
	return nil
}

// DueForExpiry implements ports.QuoteRepository.
func (r *QuoteRepository) DueForExpiry(ctx context.Context, now time.Time, limit int) ([]quote.ID, error) {
	defer r.s.enter(ctx)()
	var found []quote.Snapshot
	for _, snap := range r.s.quotes {
		if snap.State == quote.StateIssued && !snap.ExpiresAt.After(now) {
			found = append(found, snap)
		}
	}
	sort.Slice(found, func(i, j int) bool {
		if found[i].ExpiresAt.Equal(found[j].ExpiresAt) {
			return found[i].ID < found[j].ID
		}
		return found[i].ExpiresAt.Before(found[j].ExpiresAt)
	})
	if len(found) > limit {
		found = found[:limit]
	}
	out := make([]quote.ID, len(found))
	for i, snap := range found {
		out[i] = snap.ID
	}
	return out, nil
}

// CatalogView is the in-memory ports.CatalogView.
type CatalogView struct{ s *Store }

// State implements ports.CatalogView.
func (c *CatalogView) State(ctx context.Context, id itemprice.ID) (itemprice.CatalogState, error) {
	defer c.s.enter(ctx)()
	if st, ok := c.s.catalog[id]; ok {
		return st, nil
	}
	return itemprice.CatalogUnknown, nil
}

// MarkCreated implements ports.CatalogView.
func (c *CatalogView) MarkCreated(ctx context.Context, id itemprice.ID) error {
	defer c.s.enter(ctx)()
	if _, ok := c.s.catalog[id]; !ok {
		c.s.catalog[id] = itemprice.CatalogActive
	}
	return nil
}

// MarkDiscontinued implements ports.CatalogView.
func (c *CatalogView) MarkDiscontinued(ctx context.Context, id itemprice.ID) error {
	defer c.s.enter(ctx)()
	c.s.catalog[id] = itemprice.CatalogDiscontinued
	return nil
}

// Inbox is the in-memory ports.Inbox.
type Inbox struct{ s *Store }

// Claim implements ports.Inbox.
func (i *Inbox) Claim(ctx context.Context, eventID, eventType string) (bool, error) {
	defer i.s.enter(ctx)()
	if _, seen := i.s.inbox[eventID]; seen {
		return false, nil
	}
	i.s.inbox[eventID] = eventType
	return true, nil
}

// Outbox is the in-memory ports.EventOutbox: it keeps the appended events.
type Outbox struct{ s *Store }

// Append implements ports.EventOutbox.
func (o *Outbox) Append(ctx context.Context, events ...shared.Event) error {
	defer o.s.enter(ctx)()
	o.s.outbox = append(o.s.outbox, events...)
	return nil
}
