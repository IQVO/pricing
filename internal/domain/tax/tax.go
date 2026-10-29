// Package tax is the TaxTable aggregate: a synthetic tax table (decision D10),
// basis-point rates by destination postal-prefix band. Exactly one table is
// CURRENT; publishing a new one supersedes the previous in the same
// transaction (states CURRENT -> SUPERSEDED, model-gap decision 4) and its
// version must strictly increase. Bands never overlap (ErrOverlappingTaxBand)
// and a rate is within 0..MaxRateBasisPoints (ErrRateOutOfRange). A
// destination that falls in no band is untaxed (ADR 0002, A9).
package tax

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/IQVO/pricing/internal/domain/shared"
)

const entity = "taxtable"

var (
	// ErrOverlappingTaxBand: bands never overlap (422 overlapping-tax-band).
	ErrOverlappingTaxBand = errors.New("overlapping tax band")
	// ErrRateOutOfRange: a rate is within 0..MaxRateBasisPoints (422
	// rate-out-of-range).
	ErrRateOutOfRange = errors.New("rate out of range")
	// ErrNonIncreasingVersion: a new version is higher than the current one
	// (409 non-increasing-version).
	ErrNonIncreasingVersion = errors.New("non-increasing version")
)

// Bounds (ADR 0002, A9). Plain literals on purpose.
const (
	// PrefixLength is the length of a destination postal prefix and of both
	// ends of a band.
	PrefixLength = 3
	// MaxRateBasisPoints is the highest rate: 100.00 percent.
	MaxRateBasisPoints = 10000
	// MaxBands is the most bands one table holds.
	MaxBands = 500
)

// ID identifies a tax table (taxTableId).
type ID string

// State is the lifecycle state of a table.
type State string

// The two states.
const (
	StateCurrent    State = "CURRENT"
	StateSuperseded State = "SUPERSEDED"
)

// Valid reports whether s is one of the two states.
func (s State) Valid() bool { return s == StateCurrent || s == StateSuperseded }

// TaxBand is the rate of the destination prefixes from PostalPrefixFrom to
// PostalPrefixTo, both inclusive, compared as text.
type TaxBand struct {
	PostalPrefixFrom string
	PostalPrefixTo   string
	RateBasisPoints  int
}

// ValidPrefix reports whether p is a destination postal prefix: exactly
// PrefixLength upper-case ASCII letters or digits.
func ValidPrefix(p string) bool {
	if len(p) != PrefixLength {
		return false
	}
	for i := 0; i < len(p); i++ {
		c := p[i]
		if (c < '0' || c > '9') && (c < 'A' || c > 'Z') {
			return false
		}
	}
	return true
}

// TaxTable is the aggregate root.
type TaxTable struct {
	id          ID
	version     int64
	state       State
	bands       []TaxBand
	publishedAt time.Time
}

// ID returns the taxTableId.
func (t *TaxTable) ID() ID { return t.id }

// Version returns the table version published by the merchandiser.
func (t *TaxTable) Version() int64 { return t.version }

// State returns the lifecycle state.
func (t *TaxTable) State() State { return t.state }

// Bands returns a copy of the bands, lowest first.
func (t *TaxTable) Bands() []TaxBand { return slices.Clone(t.bands) }

// PublishedAt returns when the table was published.
func (t *TaxTable) PublishedAt() time.Time { return t.publishedAt }

// Publish creates the CURRENT table of the given version. currentVersion is the
// version of the table being replaced, 0 when there is none; the caller
// supersedes that table (Supersede) in the same unit of work. It raises
// TaxTableChanged.
func Publish(id ID, version int64, bands []TaxBand, currentVersion int64, now time.Time) (*TaxTable, []shared.Event, error) {
	if !shared.ValidID(string(id)) {
		return nil, nil, shared.Invalid("taxTableId %q is not a valid id", id)
	}
	if version < 1 || version > shared.MaxInt32 {
		return nil, nil, shared.Invalid("version must be between 1 and %d", shared.MaxInt32)
	}
	ordered, err := orderBands(bands)
	if err != nil {
		return nil, nil, err
	}
	if version <= currentVersion {
		return nil, nil, fmt.Errorf("%w: version %d does not exceed the current version %d", ErrNonIncreasingVersion, version, currentVersion)
	}
	t := &TaxTable{id: id, version: version, state: StateCurrent, bands: ordered, publishedAt: shared.UTC(now)}
	return t, []shared.Event{TaxTableChanged{
		Header:     shared.Header{Version: version, At: t.publishedAt},
		TaxTableID: id,
		Bands:      slices.Clone(ordered),
	}}, nil
}

// orderBands validates the bands and returns them lowest first.
func orderBands(bands []TaxBand) ([]TaxBand, error) {
	if len(bands) == 0 || len(bands) > MaxBands {
		return nil, shared.Invalid("a table holds 1 to %d bands", MaxBands)
	}
	for _, b := range bands {
		if !ValidPrefix(b.PostalPrefixFrom) || !ValidPrefix(b.PostalPrefixTo) {
			return nil, shared.Invalid("band %q-%q: a postal prefix is %d upper-case letters or digits", b.PostalPrefixFrom, b.PostalPrefixTo, PrefixLength)
		}
		if b.PostalPrefixFrom > b.PostalPrefixTo {
			return nil, shared.Invalid("band %q-%q ends before it starts", b.PostalPrefixFrom, b.PostalPrefixTo)
		}
		if b.RateBasisPoints < 0 || b.RateBasisPoints > MaxRateBasisPoints {
			return nil, fmt.Errorf("%w: band %s-%s rate %d is outside 0..%d", ErrRateOutOfRange, b.PostalPrefixFrom, b.PostalPrefixTo, b.RateBasisPoints, MaxRateBasisPoints)
		}
	}
	out := slices.Clone(bands)
	slices.SortFunc(out, func(a, b TaxBand) int {
		if a.PostalPrefixFrom < b.PostalPrefixFrom {
			return -1
		}
		if a.PostalPrefixFrom > b.PostalPrefixFrom {
			return 1
		}
		return 0
	})
	for i := 1; i < len(out); i++ {
		if out[i].PostalPrefixFrom <= out[i-1].PostalPrefixTo {
			return nil, fmt.Errorf("%w: %s-%s and %s-%s", ErrOverlappingTaxBand,
				out[i-1].PostalPrefixFrom, out[i-1].PostalPrefixTo, out[i].PostalPrefixFrom, out[i].PostalPrefixTo)
		}
	}
	return out, nil
}

// Supersede moves CURRENT to SUPERSEDED. A superseded table cannot be
// superseded again.
func (t *TaxTable) Supersede() error {
	if t.state != StateCurrent {
		return fmt.Errorf("%w: tax table %s is already %s", shared.ErrIllegalTransition, t.id, t.state)
	}
	t.state = StateSuperseded
	return nil
}

// RateFor returns the rate in basis points of the destination prefix; 0 when
// no band holds it.
func (t *TaxTable) RateFor(prefix string) int {
	for _, b := range t.bands {
		if prefix >= b.PostalPrefixFrom && prefix <= b.PostalPrefixTo {
			return b.RateBasisPoints
		}
	}
	return 0
}

// Snapshot is the persisted form of a tax table.
type Snapshot struct {
	ID          ID
	Version     int64
	State       State
	Bands       []TaxBand
	PublishedAt time.Time
}

// Snapshot returns the persisted form of the table.
func (t *TaxTable) Snapshot() Snapshot {
	return Snapshot{ID: t.id, Version: t.version, State: t.state, Bands: slices.Clone(t.bands), PublishedAt: t.publishedAt}
}

// Restore rebuilds a persisted table, validating every invariant.
func Restore(s Snapshot) (*TaxTable, error) {
	if !shared.ValidID(string(s.ID)) {
		return nil, shared.Corrupt("tax table %q: taxTableId must be a valid id", s.ID)
	}
	if s.Version < 1 || s.Version > shared.MaxInt32 || !s.State.Valid() || s.PublishedAt.IsZero() {
		return nil, shared.Corrupt("tax table %q: version, state and publishedAt are required", s.ID)
	}
	ordered, err := orderBands(s.Bands)
	if err != nil {
		return nil, shared.Corrupt("tax table %q: %v", s.ID, err)
	}
	return &TaxTable{id: s.ID, version: s.Version, state: s.State, bands: ordered, publishedAt: shared.UTC(s.PublishedAt)}, nil
}

// TaxTableChanged is raised by Publish. Version is the table version, strictly
// increasing.
type TaxTableChanged struct {
	shared.Header
	TaxTableID ID
	Bands      []TaxBand
}

// EventName returns "TaxTableChanged".
func (TaxTableChanged) EventName() string { return "TaxTableChanged" }

// Entity returns "taxtable".
func (TaxTableChanged) Entity() string { return entity }

// SubjectID returns the taxTableId.
func (e TaxTableChanged) SubjectID() string { return string(e.TaxTableID) }
