package itemprice

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/IQVO/pricing/internal/domain/shared"
)

// Snapshot is the persisted form of an ItemPrice. Repositories store it and
// Restore rebuilds the aggregate from it, validating every invariant.
type Snapshot struct {
	ID          ID
	State       State
	ListPrice   shared.Money
	Sales       []SaleWindow
	Version     int64
	EvaluatedAt time.Time
	Published   View
}

// NextBoundary is NextBoundary of the aggregate the snapshot restores to; the
// boundary index of a repository is computed from it.
func (s Snapshot) NextBoundary() time.Time { return nextBoundary(s.State, s.Sales, s.EvaluatedAt) }

// Snapshot returns the persisted form of the aggregate.
func (p *ItemPrice) Snapshot() Snapshot {
	return Snapshot{
		ID: p.id, State: p.state, ListPrice: p.list, Sales: slices.Clone(p.sales),
		Version: p.version, EvaluatedAt: p.evaluatedAt, Published: p.published,
	}
}

// Restore rebuilds a persisted item price, validating every invariant that
// does not depend on the clock or the catalogue.
func Restore(s Snapshot) (*ItemPrice, error) {
	if err := checkSnapshot(s); err != nil {
		return nil, shared.Corrupt("item price %q: %v", s.ID, err)
	}
	sales := slices.Clone(s.Sales)
	for i := range sales {
		sales[i].From, sales[i].To = shared.UTC(sales[i].From), shared.UTC(sales[i].To)
	}
	pub := s.Published
	pub.SaleEndsAt = shared.UTC(pub.SaleEndsAt)
	return &ItemPrice{
		id: s.ID, state: s.State, list: s.ListPrice, sales: sales, version: s.Version, loaded: s.Version,
		evaluatedAt: shared.UTC(s.EvaluatedAt), published: pub,
	}, nil
}

func checkSnapshot(s Snapshot) error {
	if !shared.ValidID(string(s.ID)) {
		return errors.New("catalogItemId must be a valid id")
	}
	if !s.State.Valid() {
		return fmt.Errorf("unknown state %q", s.State)
	}
	if s.Version < 1 {
		return errors.New("a version of at least 1 is required")
	}
	if !shared.ValidCurrency(s.ListPrice.Currency) {
		return fmt.Errorf("currency %q is not an ISO 4217 code", s.ListPrice.Currency)
	}
	if err := s.ListPrice.Check(s.ListPrice.Currency); err != nil {
		return err
	}
	if len(s.Sales) > MaxSales {
		return fmt.Errorf("more than %d sale windows", MaxSales)
	}
	return checkSales(s)
}

func checkSales(s Snapshot) error {
	for i, w := range s.Sales {
		if !shared.ValidID(w.SaleID) {
			return fmt.Errorf("sale %d: saleId must be a valid id", i)
		}
		if err := w.Price.Check(s.ListPrice.Currency); err != nil {
			return fmt.Errorf("sale %q: %w", w.SaleID, err)
		}
		if w.Price.AmountMinor >= s.ListPrice.AmountMinor {
			return fmt.Errorf("sale %q: price %d is not below the list price %d", w.SaleID, w.Price.AmountMinor, s.ListPrice.AmountMinor)
		}
		if !w.To.After(w.From) {
			return fmt.Errorf("sale %q: ends before it starts", w.SaleID)
		}
		for _, o := range s.Sales[:i] {
			if w.From.Before(o.To) && o.From.Before(w.To) {
				return fmt.Errorf("sales %q and %q overlap", o.SaleID, w.SaleID)
			}
			if o.SaleID == w.SaleID {
				return fmt.Errorf("saleId %q is used twice", w.SaleID)
			}
		}
	}
	return nil
}
