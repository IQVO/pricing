// Package usecases holds the application's use cases: one per operation of
// apis/openapi.yaml, one per consumed event and one per clock-driven policy.
// Every command follows one shape inside ONE ports.UnitOfWork: load the
// aggregate, call the domain, save it and append the raised events to the
// outbox, then return a view. Domain, shared and port errors pass through
// unchanged (errors.Is keeps working): the HTTP adapter maps them to problem
// types.
package usecases

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/IQVO/pricing/internal/application/ports"
	"github.com/IQVO/pricing/internal/domain/shared"
)

// Deps bundles the ports and the policy values the use cases need.
type Deps struct {
	ItemPrices ports.ItemPriceRepository
	Schedules  ports.ScheduleRepository
	TaxTables  ports.TaxTableRepository
	Quotes     ports.QuoteRepository
	Catalog    ports.CatalogView
	Inbox      ports.Inbox
	Outbox     ports.EventOutbox
	UoW        ports.UnitOfWork
	Clock      ports.Clock
	IDs        ports.IDGenerator

	// Currency is the one configured ISO 4217 currency.
	Currency string
	// QuoteTTL is the policy QuoteTtl (default DefaultQuoteTTL).
	QuoteTTL time.Duration
	// Log receives the events a consumer skipped; nil discards them.
	Log *slog.Logger
}

const (
	// commandAttempts is how often a command runs when another writer got in
	// between its load and its save.
	commandAttempts = 2
	// consumerAttempts is the same for consumed events.
	consumerAttempts = 5
	// batchSize bounds one pass of a clock-driven policy.
	batchSize = 200
)

// DefaultQuoteTTL is the policy QuoteTtl default: 15 minutes.
const DefaultQuoteTTL = 15 * time.Minute

func (d Deps) now() time.Time { return d.Clock.Now().UTC() }

func (d Deps) logger() *slog.Logger {
	if d.Log != nil {
		return d.Log
	}
	return slog.New(slog.DiscardHandler)
}

func (d Deps) quoteTTL() time.Duration {
	if d.QuoteTTL > 0 {
		return d.QuoteTTL
	}
	return DefaultQuoteTTL
}

func (d Deps) publish(ctx context.Context, events []shared.Event) error {
	if err := d.Outbox.Append(ctx, events...); err != nil {
		return fmt.Errorf("append events to outbox: %w", err)
	}
	return nil
}

// transact runs attempt in a unit of work. A ports.ErrConcurrentUpdate means
// another writer got in between the load and the save, so the whole attempt
// (load, domain call, save, outbox) runs again on fresh state, up to attempts
// times, before the conflict is reported.
func transact[T any](ctx context.Context, uow ports.UnitOfWork, attempts int, attempt func(ctx context.Context) (T, error)) (T, error) {
	var out T
	run := func(ctx context.Context) error {
		v, err := attempt(ctx)
		if err != nil {
			return err
		}
		out = v
		return nil
	}
	var err error
	for i := 0; i < attempts; i++ {
		if err = uow.Do(ctx, run); !errors.Is(err, ports.ErrConcurrentUpdate) {
			break
		}
	}
	if err != nil {
		var zero T
		return zero, err
	}
	return out, nil
}
