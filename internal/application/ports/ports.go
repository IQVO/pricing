// Package ports declares the application's OUT ports: interfaces only
// (enforced by internal/architecture). Their non-interface vocabulary is the
// sentinels of errors.go and the data package idempotency.
//
// Optimistic concurrency: an item price and a quote are saved under the rule
// "stored version == LoadedVersion()" (several commands may run between a load
// and a save); a create (LoadedVersion 0) expects that the id is not stored.
// After a successful Save the adapter calls MarkPersisted(). A schedule and a
// tax table are saved as a pair inside one unit of work (the superseded one,
// then the new CURRENT one); a second CURRENT row or a repeated version is
// ErrConcurrentUpdate. Any mismatch, including an update of a row that no
// longer exists, is ErrConcurrentUpdate. Inside a UnitOfWork every repository
// joins the transaction carried by the ctx the unit handed to its function.
package ports

import (
	"context"
	"time"

	"github.com/IQVO/pricing/internal/application/idempotency"
	"github.com/IQVO/pricing/internal/domain/itemprice"
	"github.com/IQVO/pricing/internal/domain/quote"
	"github.com/IQVO/pricing/internal/domain/schedule"
	"github.com/IQVO/pricing/internal/domain/shared"
	"github.com/IQVO/pricing/internal/domain/tax"
)

// ItemPriceRepository persists ItemPrice aggregates.
type ItemPriceRepository interface {
	// Get returns the item price, or ErrNotFound.
	Get(ctx context.Context, id itemprice.ID) (*itemprice.ItemPrice, error)
	// Save persists the item price under the concurrency rule of the package
	// comment.
	Save(ctx context.Context, p *itemprice.ItemPrice) error
	// List returns up to limit item prices ordered by catalogItemId,
	// starting strictly after the id (the empty id starts at the beginning).
	List(ctx context.Context, after itemprice.ID, limit int) ([]*itemprice.ItemPrice, error)
	// DueForBoundary returns the ids of up to limit ACTIVE item prices whose
	// next sale-window boundary (itemprice.NextBoundary) is at or before now,
	// earliest first.
	DueForBoundary(ctx context.Context, now time.Time, limit int) ([]itemprice.ID, error)
}

// ScheduleRepository persists DeliveryChargeSchedule aggregates.
type ScheduleRepository interface {
	// Current returns the CURRENT schedule, or ErrNotFound when none was
	// published yet.
	Current(ctx context.Context) (*schedule.DeliveryChargeSchedule, error)
	// Save inserts a schedule that is not stored yet, or stores the state of
	// one that is (CURRENT to SUPERSEDED). Inserting a second CURRENT schedule,
	// or a version that is already stored, is ErrConcurrentUpdate.
	Save(ctx context.Context, s *schedule.DeliveryChargeSchedule) error
}

// TaxTableRepository persists TaxTable aggregates.
type TaxTableRepository interface {
	// Current returns the CURRENT table, or ErrNotFound when none was
	// published yet.
	Current(ctx context.Context) (*tax.TaxTable, error)
	// Save inserts a table that is not stored yet, or stores the state of one
	// that is (CURRENT to SUPERSEDED). Inserting a second CURRENT table, or a
	// version that is already stored, is ErrConcurrentUpdate.
	Save(ctx context.Context, t *tax.TaxTable) error
}

// QuoteRepository persists PriceQuote aggregates.
type QuoteRepository interface {
	// Get returns the quote, or ErrNotFound.
	Get(ctx context.Context, id quote.ID) (*quote.PriceQuote, error)
	// Save persists the quote under the concurrency rule of the package
	// comment.
	Save(ctx context.Context, q *quote.PriceQuote) error
	// DueForExpiry returns the ids of up to limit ISSUED quotes whose expiresAt
	// is at or before now, earliest first.
	DueForExpiry(ctx context.Context, now time.Time, limit int) ([]quote.ID, error)
}

// CatalogView is the local read model CatalogItemView (fed by offer-availability
// events): that an item exists and whether it is discontinued, nothing more.
type CatalogView interface {
	// State returns what the view knows about the item; CatalogUnknown for an
	// item it never saw.
	State(ctx context.Context, id itemprice.ID) (itemprice.CatalogState, error)
	// MarkCreated records the item as ACTIVE. It never revives a DISCONTINUED
	// item: a replayed creation must not undo a discontinuation.
	MarkCreated(ctx context.Context, id itemprice.ID) error
	// MarkDiscontinued records the item as DISCONTINUED, creating the entry
	// when the item was never seen.
	MarkDiscontinued(ctx context.Context, id itemprice.ID) error
}

// Inbox makes consumers idempotent on the CloudEvents id.
type Inbox interface {
	// Claim records the event id and reports whether this is the first time
	// it is seen. Called with the ctx of a unit of work, the claim rolls back
	// with it.
	Claim(ctx context.Context, eventID, eventType string) (first bool, err error)
}

// EventOutbox is the write side of the transactional outbox. Called with the
// ctx handed to UnitOfWork.Do it joins the SAME transaction as the aggregate
// Save calls. Appending no events is a no-op.
type EventOutbox interface {
	Append(ctx context.Context, events ...shared.Event) error
}

// UnitOfWork runs fn as ONE atomic unit: every repository, inbox and outbox
// call fn makes with the ctx it is handed commits together when fn returns
// nil, or rolls back together when fn returns an error (or panics). Do is
// re-entrant; the error fn returns is returned unchanged.
type UnitOfWork interface {
	Do(ctx context.Context, fn func(ctx context.Context) error) error
}

// IdempotencyStore remembers the first response per (route, key) for 24 hours.
type IdempotencyStore interface {
	// Get returns the stored record, or ErrNotFound (expired records are
	// treated as absent).
	Get(ctx context.Context, route, key string) (idempotency.Record, error)
	// Put stores the record unless one exists; it reports whether it stored.
	Put(ctx context.Context, route, key string, rec idempotency.Record) (bool, error)
}

// Clock is the service's notion of "now"; use cases stamp aggregates with it.
type Clock interface {
	Now() time.Time
}

// IDGenerator mints the ids of new aggregates and sale windows.
type IDGenerator interface {
	// NewSaleID mints the id of a sale window.
	NewSaleID() string
	// NewScheduleID mints a scheduleId.
	NewScheduleID() schedule.ID
	// NewTaxTableID mints a taxTableId.
	NewTaxTableID() tax.ID
	// NewQuoteID mints a quoteId.
	NewQuoteID() quote.ID
}
