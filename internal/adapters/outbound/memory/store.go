// Package memory holds in-memory TEST DOUBLES of the application's out ports.
// They are for unit, BDD and contract tests only: cmd/** never imports this
// package (enforced by internal/architecture) because the service runs on real
// Postgres and Kafka.
//
// Every repository round-trips its aggregate through Snapshot and Restore, so
// a loaded aggregate never aliases stored state, a corrupt row can not become a
// live aggregate and the doubles keep the persistence contract of the real
// adapters: a miss is ports.ErrNotFound, a stale or duplicate save is
// ports.ErrConcurrentUpdate (also a second CURRENT schedule or tax table, and a
// repeated version: the unique indexes of the Postgres schema).
//
// One Store backs every double. Its UnitOfWork is atomic: it holds the store
// for the whole unit and restores the state taken at the start when the unit
// fails or panics, outbox, inbox and read model included.
package memory

import (
	"context"
	"sync"
	"time"

	"github.com/IQVO/pricing/internal/application/idempotency"
	"github.com/IQVO/pricing/internal/application/ports"
	"github.com/IQVO/pricing/internal/domain/itemprice"
	"github.com/IQVO/pricing/internal/domain/quote"
	"github.com/IQVO/pricing/internal/domain/schedule"
	"github.com/IQVO/pricing/internal/domain/shared"
	"github.com/IQVO/pricing/internal/domain/tax"
)

type idemKey struct{ route, key string }

// Store is the shared state of the in-memory doubles.
type Store struct {
	mu          sync.Mutex
	prices      map[itemprice.ID]itemprice.Snapshot
	schedules   map[schedule.ID]schedule.Snapshot
	taxTables   map[tax.ID]tax.Snapshot
	quotes      map[quote.ID]quote.Snapshot
	catalog     map[itemprice.ID]itemprice.CatalogState
	inbox       map[string]string
	outbox      []shared.Event
	idempotency map[idemKey]idempotency.Record
	clock       ports.Clock
}

// NewStore returns an empty store. The clock is the authority the
// IdempotencyStore judges record TTL against: the doubles exist to test a
// domain whose clock is frozen, so TTL must be measured on that same clock,
// never on the wall.
func NewStore(clock ports.Clock) *Store {
	return &Store{
		prices: map[itemprice.ID]itemprice.Snapshot{}, schedules: map[schedule.ID]schedule.Snapshot{},
		taxTables: map[tax.ID]tax.Snapshot{}, quotes: map[quote.ID]quote.Snapshot{},
		catalog: map[itemprice.ID]itemprice.CatalogState{}, inbox: map[string]string{},
		idempotency: map[idemKey]idempotency.Record{}, clock: clock,
	}
}

// now is the store's time: the injected clock.
func (s *Store) now() time.Time { return s.clock.Now() }

// state is a copy of everything a unit of work can change. Stored values are
// replaced, never edited in place, so copying the maps is enough.
type state struct {
	prices    map[itemprice.ID]itemprice.Snapshot
	schedules map[schedule.ID]schedule.Snapshot
	taxTables map[tax.ID]tax.Snapshot
	quotes    map[quote.ID]quote.Snapshot
	catalog   map[itemprice.ID]itemprice.CatalogState
	inbox     map[string]string
	outbox    []shared.Event
}

func cloneMap[K comparable, V any](m map[K]V) map[K]V {
	out := make(map[K]V, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func (s *Store) capture() state {
	return state{
		prices: cloneMap(s.prices), schedules: cloneMap(s.schedules), taxTables: cloneMap(s.taxTables), quotes: cloneMap(s.quotes),
		catalog: cloneMap(s.catalog), inbox: cloneMap(s.inbox), outbox: append([]shared.Event(nil), s.outbox...),
	}
}

func (s *Store) restore(st state) {
	s.prices, s.schedules, s.taxTables, s.quotes, s.catalog, s.inbox, s.outbox =
		st.prices, st.schedules, st.taxTables, st.quotes, st.catalog, st.inbox, st.outbox
}

// txKey marks a ctx that carries the open unit of work of one store.
type txKey struct{ store *Store }

// enter locks the store for one repository call, unless ctx already carries the
// unit of work that holds it. The returned func releases the lock.
func (s *Store) enter(ctx context.Context) func() {
	if ctx.Value(txKey{s}) != nil {
		return func() {}
	}
	s.mu.Lock()
	return s.mu.Unlock
}

// ItemPrices returns the ItemPriceRepository double.
func (s *Store) ItemPrices() *ItemPriceRepository { return &ItemPriceRepository{s} }

// Schedules returns the ScheduleRepository double.
func (s *Store) Schedules() *ScheduleRepository { return &ScheduleRepository{s} }

// TaxTables returns the TaxTableRepository double.
func (s *Store) TaxTables() *TaxTableRepository { return &TaxTableRepository{s} }

// Quotes returns the QuoteRepository double.
func (s *Store) Quotes() *QuoteRepository { return &QuoteRepository{s} }

// Catalog returns the CatalogView double.
func (s *Store) Catalog() *CatalogView { return &CatalogView{s} }

// Inbox returns the Inbox double.
func (s *Store) Inbox() *Inbox { return &Inbox{s} }

// Outbox returns the EventOutbox double.
func (s *Store) Outbox() *Outbox { return &Outbox{s} }

// UnitOfWork returns the UnitOfWork double.
func (s *Store) UnitOfWork() *UnitOfWork { return &UnitOfWork{s} }

// Idempotency returns the IdempotencyStore double.
func (s *Store) Idempotency() *IdempotencyStore { return &IdempotencyStore{s} }

// Counts is how many rows each table of the store holds.
type Counts struct {
	ItemPrices, Schedules, TaxTables, Quotes, Catalog, Inbox, OutboxEvents int
	// VersionSum is the sum of every item price and quote version: any saved
	// change moves it.
	VersionSum int64
}

// Counts returns the size of every table and the sum of the aggregate
// versions, so a test can prove that a failed use case stored and changed
// nothing.
func (s *Store) Counts() Counts {
	s.mu.Lock()
	defer s.mu.Unlock()
	var versions int64
	for _, p := range s.prices {
		versions += p.Version
	}
	for _, q := range s.quotes {
		versions += q.Version
	}
	return Counts{
		ItemPrices: len(s.prices), Schedules: len(s.schedules), TaxTables: len(s.taxTables), Quotes: len(s.quotes),
		Catalog: len(s.catalog), Inbox: len(s.inbox), OutboxEvents: len(s.outbox), VersionSum: versions,
	}
}

// Events returns a copy of the events appended to the outbox, oldest first.
func (s *Store) Events() []shared.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]shared.Event(nil), s.outbox...)
}
