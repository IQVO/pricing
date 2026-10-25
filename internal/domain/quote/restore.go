package quote

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/IQVO/pricing/internal/domain/shared"
)

// Snapshot is the persisted form of a PriceQuote.
type Snapshot struct {
	ID              ID
	CartID          string
	State           State
	Currency        string
	Lines           []Line
	Groups          []Group
	TaxMinor        int64
	TotalMinor      int64
	ScheduleVersion int64
	TaxTableVersion int64
	IssuedAt        time.Time
	ExpiresAt       time.Time
	Version         int64
}

// Snapshot returns the persisted form of the quote.
func (q *PriceQuote) Snapshot() Snapshot {
	return Snapshot{
		ID: q.id, CartID: q.cartID, State: q.state, Currency: q.currency, Lines: slices.Clone(q.lines), Groups: slices.Clone(q.groups),
		TaxMinor: q.tax, TotalMinor: q.total, ScheduleVersion: q.scheduleVersion, TaxTableVersion: q.taxVersion,
		IssuedAt: q.issuedAt, ExpiresAt: q.expiresAt, Version: q.version,
	}
}

// Restore rebuilds a persisted quote, validating every invariant.
func Restore(s Snapshot) (*PriceQuote, error) {
	if err := checkSnapshot(s); err != nil {
		return nil, shared.Corrupt("quote %q: %v", s.ID, err)
	}
	return &PriceQuote{
		id: s.ID, cartID: s.CartID, state: s.State, currency: s.Currency, lines: slices.Clone(s.Lines), groups: slices.Clone(s.Groups),
		tax: s.TaxMinor, total: s.TotalMinor, scheduleVersion: s.ScheduleVersion, taxVersion: s.TaxTableVersion,
		issuedAt: shared.UTC(s.IssuedAt), expiresAt: shared.UTC(s.ExpiresAt), version: s.Version, loaded: s.Version,
	}, nil
}

func checkSnapshot(s Snapshot) error {
	if !shared.ValidID(string(s.ID)) || !shared.ValidID(s.CartID) {
		return errors.New("quoteId and cartId must be valid ids")
	}
	if !s.State.Valid() || !shared.ValidCurrency(s.Currency) {
		return errors.New("a valid state and currency are required")
	}
	if s.ScheduleVersion < 1 || s.TaxTableVersion < 1 {
		return errors.New("the schedule and tax table versions are required")
	}
	if s.IssuedAt.IsZero() || !s.ExpiresAt.After(s.IssuedAt) {
		return errors.New("expiresAt must be after issuedAt")
	}
	if (s.State == StateIssued) != (s.Version == 1) || s.Version > 2 {
		return fmt.Errorf("version %d does not match state %s", s.Version, s.State)
	}
	goods, err := checkSnapshotLines(s)
	if err != nil {
		return err
	}
	delivery, err := checkSnapshotGroups(s)
	if err != nil {
		return err
	}
	if s.TaxMinor < 0 || s.TotalMinor != goods+delivery+s.TaxMinor {
		return errors.New("the total is not goods plus delivery plus tax")
	}
	return nil
}

func checkSnapshotLines(s Snapshot) (int64, error) {
	if len(s.Lines) == 0 || len(s.Lines) > MaxLines {
		return 0, fmt.Errorf("a quote holds 1 to %d lines", MaxLines)
	}
	var goods int64
	seen := make(map[string]bool, len(s.Lines))
	for i, l := range s.Lines {
		if l.LineNo != i+1 || !shared.ValidID(l.CatalogItemID) || seen[l.CatalogItemID] {
			return 0, fmt.Errorf("line %d: lineNo, catalogItemId or its uniqueness is wrong", i+1)
		}
		seen[l.CatalogItemID] = true
		if l.Quantity < 1 || l.Quantity > MaxQuantity || l.PriceVersion < 1 {
			return 0, fmt.Errorf("line %d: quantity or price version out of range", l.LineNo)
		}
		if err := l.UnitPrice.Check(s.Currency); err != nil {
			return 0, fmt.Errorf("line %d: %w", l.LineNo, err)
		}
		if l.LineTotal != (shared.Money{AmountMinor: l.UnitPrice.AmountMinor * int64(l.Quantity), Currency: s.Currency}) {
			return 0, fmt.Errorf("line %d: the line total is not unit price times quantity", l.LineNo)
		}
		goods += l.LineTotal.AmountMinor
	}
	return goods, nil
}

func checkSnapshotGroups(s Snapshot) (int64, error) {
	if len(s.Groups) == 0 || len(s.Groups) > MaxGroups {
		return 0, fmt.Errorf("a quote holds 1 to %d groups", MaxGroups)
	}
	var delivery int64
	seen := make(map[int]bool, len(s.Groups))
	for _, g := range s.Groups {
		if g.GroupNo < 1 || g.GroupNo > shared.MaxInt32 || seen[g.GroupNo] || !g.Tier.Valid() {
			return 0, fmt.Errorf("group %d: groupNo, uniqueness or tier is wrong", g.GroupNo)
		}
		seen[g.GroupNo] = true
		if err := g.DeliveryCharge.Check(s.Currency); err != nil {
			return 0, fmt.Errorf("group %d: %w", g.GroupNo, err)
		}
		delivery += g.DeliveryCharge.AmountMinor
	}
	return delivery, nil
}
