// Package quote is the PriceQuote aggregate: a binding, immutable, expiring
// price snapshot for one checkout. It holds the lines with unit and line
// totals, the groups with tier and delivery charge, the tax, the total, the
// versions of price, schedule and tax table it used, issuedAt and expiresAt.
//
// States: ISSUED, EXPIRED (ExpireQuote, once expiresAt passed; the clock-driven
// policy QuoteTtl). Expiry raises no event (model-gap decision 3). Invariants,
// by the model's names: every line names an ACTIVE price (ErrItemNotPriced); a
// quote has at least one line (ErrEmptyQuote); every amount uses the
// configured currency (shared.ErrCurrencyMismatch).
//
// Arithmetic (ADR 0002, A10): a line total is unit price times quantity; a
// group is charged its tier from the current schedule, free when its goods
// reach the tier's free-over threshold; tax is the destination band's rate
// applied to goods plus delivery, rounded half up to a minor unit; the total is
// goods plus delivery plus tax.
package quote

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/IQVO/pricing/internal/domain/schedule"
	"github.com/IQVO/pricing/internal/domain/shared"
	"github.com/IQVO/pricing/internal/domain/tax"
)

const entity = "pricequote"

var (
	// ErrItemNotPriced: every line names an ACTIVE price (422 item-not-priced).
	ErrItemNotPriced = errors.New("item not priced")
	// ErrEmptyQuote: a quote has at least one line (422 empty-quote).
	ErrEmptyQuote = errors.New("empty quote")
	// ErrNoChargeSchedule: no delivery charge schedule is CURRENT, so no
	// delivery charge can be quoted (422 no-charge-schedule, ADR 0002 A11).
	ErrNoChargeSchedule = errors.New("no current charge schedule")
	// ErrNoTaxTable: no tax table is CURRENT, so no tax can be quoted (422
	// no-tax-table, ADR 0002 A11).
	ErrNoTaxTable = errors.New("no current tax table")
)

// Bounds (ADR 0002, A6). Plain literals on purpose.
const (
	// MaxLines is the most lines a quote holds.
	MaxLines = 100
	// MaxGroups is the most shipment groups a quote holds.
	MaxGroups = 50
	// MaxQuantity is the most units of one line.
	MaxQuantity = 1000
)

// basisPointsDivisor converts a rate in basis points to a fraction.
const basisPointsDivisor = 10000

// ID identifies a quote (quoteId).
type ID string

// State is the lifecycle state of a quote.
type State string

// The two states.
const (
	StateIssued  State = "ISSUED"
	StateExpired State = "EXPIRED"
)

// Valid reports whether s is one of the two states.
func (s State) Valid() bool { return s == StateIssued || s == StateExpired }

// LineInput is one cart line as the checkout session froze it, with the price
// the use case found for the item. Priced is false when the item has no ACTIVE
// price.
type LineInput struct {
	CatalogItemID string
	Quantity      int
	Unit          shared.Money
	Priced        bool
	PriceVersion  int64
}

// GroupInput is one shipment group the checkout session froze: the tier the
// shopper chose and the 1-based numbers of its lines.
type GroupInput struct {
	GroupNo int
	OfferID string
	Tier    shared.Tier
	LineNos []int
}

// IssueInput is the command IssuePriceQuote.
type IssueInput struct {
	ID                      ID
	CartID                  string
	DestinationPostalPrefix string
	Lines                   []LineInput
	Groups                  []GroupInput
	// Currency is the one configured currency.
	Currency string
	// TTL is the policy QuoteTtl.
	TTL time.Duration
	Now time.Time
}

// Line is a priced line of a quote; LineNo is its 1-based position in the cart
// line order.
type Line struct {
	LineNo        int
	CatalogItemID string
	Quantity      int
	UnitPrice     shared.Money
	LineTotal     shared.Money
	PriceVersion  int64
}

// Group is a shipment group of a quote with its delivery charge.
type Group struct {
	GroupNo        int
	Tier           shared.Tier
	DeliveryCharge shared.Money
}

// PriceQuote is the aggregate root.
type PriceQuote struct {
	id              ID
	cartID          string
	state           State
	currency        string
	lines           []Line
	groups          []Group
	tax             int64
	total           int64
	scheduleVersion int64
	taxVersion      int64
	issuedAt        time.Time
	expiresAt       time.Time
	version         int64
	loaded          int64
}

// Issue prices exactly what the checkout session froze against the CURRENT
// schedule and tax table. It raises PriceQuoteIssued.
func Issue(in IssueInput, sched *schedule.DeliveryChargeSchedule, table *tax.TaxTable) (*PriceQuote, []shared.Event, error) {
	if err := checkShape(in); err != nil {
		return nil, nil, err
	}
	if sched == nil {
		return nil, nil, ErrNoChargeSchedule
	}
	if table == nil {
		return nil, nil, ErrNoTaxTable
	}
	lines, goods, err := priceLines(in)
	if err != nil {
		return nil, nil, err
	}
	groups, err := chargeGroups(in, lines, sched)
	if err != nil {
		return nil, nil, err
	}
	base := goods
	for _, g := range groups {
		base += g.DeliveryCharge.AmountMinor
	}
	taxMinor := (base*int64(table.RateFor(in.DestinationPostalPrefix)) + basisPointsDivisor/2) / basisPointsDivisor
	now := shared.UTC(in.Now)
	q := &PriceQuote{
		id: in.ID, cartID: in.CartID, state: StateIssued, currency: in.Currency, lines: lines, groups: groups,
		tax: taxMinor, total: base + taxMinor, scheduleVersion: sched.Version(), taxVersion: table.Version(),
		issuedAt: now, expiresAt: now.Add(in.TTL), version: 1,
	}
	return q, []shared.Event{PriceQuoteIssued{
		Header:  shared.Header{Version: 1, At: now},
		QuoteID: q.id, CartID: q.cartID, Currency: q.currency, TotalMinor: q.total, ExpiresAt: q.expiresAt,
	}}, nil
}

func checkShape(in IssueInput) error {
	if !shared.ValidID(string(in.ID)) || !shared.ValidID(in.CartID) {
		return shared.Invalid("quoteId and cartId must be valid ids")
	}
	if len(in.Lines) == 0 {
		return ErrEmptyQuote
	}
	if len(in.Lines) > MaxLines {
		return shared.Invalid("a quote holds at most %d lines", MaxLines)
	}
	if len(in.Groups) == 0 || len(in.Groups) > MaxGroups {
		return shared.Invalid("a quote holds 1 to %d groups", MaxGroups)
	}
	if !tax.ValidPrefix(in.DestinationPostalPrefix) {
		return shared.Invalid("destinationPostalPrefix must be %d upper-case letters or digits", tax.PrefixLength)
	}
	if in.TTL <= 0 {
		return shared.Invalid("the quote time to live must be positive")
	}
	return nil
}

func priceLines(in IssueInput) ([]Line, int64, error) {
	seen := make(map[string]bool, len(in.Lines))
	lines := make([]Line, len(in.Lines))
	var goods int64
	for i, l := range in.Lines {
		no := i + 1
		if !shared.ValidID(l.CatalogItemID) {
			return nil, 0, shared.Invalid("line %d: catalogItemId %q is not a valid id", no, l.CatalogItemID)
		}
		if seen[l.CatalogItemID] {
			return nil, 0, shared.Invalid("line %d: catalog item %q is on two lines", no, l.CatalogItemID)
		}
		seen[l.CatalogItemID] = true
		if l.Quantity < 1 || l.Quantity > MaxQuantity {
			return nil, 0, shared.Invalid("line %d: quantity must be between 1 and %d", no, MaxQuantity)
		}
		if !l.Priced {
			return nil, 0, fmt.Errorf("%w: catalog item %q has no active price", ErrItemNotPriced, l.CatalogItemID)
		}
		if err := l.Unit.Check(in.Currency); err != nil {
			return nil, 0, fmt.Errorf("line %d: %w", no, err)
		}
		total := l.Unit.AmountMinor * int64(l.Quantity)
		lines[i] = Line{
			LineNo: no, CatalogItemID: l.CatalogItemID, Quantity: l.Quantity, UnitPrice: l.Unit,
			LineTotal: shared.Money{AmountMinor: total, Currency: in.Currency}, PriceVersion: l.PriceVersion,
		}
		goods += total
	}
	return lines, goods, nil
}

func chargeGroups(in IssueInput, lines []Line, sched *schedule.DeliveryChargeSchedule) ([]Group, error) {
	grouped := make([]bool, len(lines))
	seen := make(map[int]bool, len(in.Groups))
	groups := make([]Group, len(in.Groups))
	for i, g := range in.Groups {
		if g.GroupNo < 1 || g.GroupNo > shared.MaxInt32 || seen[g.GroupNo] {
			return nil, shared.Invalid("group numbers are unique and between 1 and %d", shared.MaxInt32)
		}
		seen[g.GroupNo] = true
		if !shared.ValidID(g.OfferID) {
			return nil, shared.Invalid("group %d: offerId %q is not a valid id", g.GroupNo, g.OfferID)
		}
		if len(g.LineNos) == 0 {
			return nil, shared.Invalid("group %d names no line", g.GroupNo)
		}
		var goods int64
		for _, no := range g.LineNos {
			if no < 1 || no > len(lines) || grouped[no-1] {
				return nil, shared.Invalid("group %d: line %d does not exist or is in another group", g.GroupNo, no)
			}
			grouped[no-1] = true
			goods += lines[no-1].LineTotal.AmountMinor
		}
		charge, err := sched.ChargeFor(g.Tier, shared.Money{AmountMinor: goods, Currency: in.Currency})
		if err != nil {
			return nil, fmt.Errorf("group %d: %w", g.GroupNo, err)
		}
		groups[i] = Group{GroupNo: g.GroupNo, Tier: g.Tier, DeliveryCharge: charge}
	}
	for i, ok := range grouped {
		if !ok {
			return nil, shared.Invalid("line %d is in no group", i+1)
		}
	}
	return groups, nil
}

// ID returns the quoteId.
func (q *PriceQuote) ID() ID { return q.id }

// CartID returns the cart the quote prices.
func (q *PriceQuote) CartID() string { return q.cartID }

// State returns the stored lifecycle state; see ExpiredAt for the state at an
// instant.
func (q *PriceQuote) State() State { return q.state }

// Currency returns the quote currency.
func (q *PriceQuote) Currency() string { return q.currency }

// Lines returns a copy of the priced lines in line order.
func (q *PriceQuote) Lines() []Line { return slices.Clone(q.lines) }

// Groups returns a copy of the groups.
func (q *PriceQuote) Groups() []Group { return slices.Clone(q.groups) }

// TaxMinor returns the tax in minor units.
func (q *PriceQuote) TaxMinor() int64 { return q.tax }

// TotalMinor returns the binding total in minor units.
func (q *PriceQuote) TotalMinor() int64 { return q.total }

// ScheduleVersion returns the version of the charge schedule used.
func (q *PriceQuote) ScheduleVersion() int64 { return q.scheduleVersion }

// TaxTableVersion returns the version of the tax table used.
func (q *PriceQuote) TaxTableVersion() int64 { return q.taxVersion }

// IssuedAt returns when the quote was issued.
func (q *PriceQuote) IssuedAt() time.Time { return q.issuedAt }

// ExpiresAt returns when the quote stops being binding.
func (q *PriceQuote) ExpiresAt() time.Time { return q.expiresAt }

// Version returns the aggregate version: 1 when issued, 2 once expired.
func (q *PriceQuote) Version() int64 { return q.version }

// LoadedVersion returns the version the aggregate had when it was loaded (0
// for a new one).
func (q *PriceQuote) LoadedVersion() int64 { return q.loaded }

// MarkPersisted records that the current version is stored.
func (q *PriceQuote) MarkPersisted() { q.loaded = q.version }

// ExpiredAt reports whether the quote is no longer binding at the instant:
// it is stored EXPIRED, or its expiresAt has passed.
func (q *PriceQuote) ExpiredAt(at time.Time) bool {
	return q.state == StateExpired || !at.Before(q.expiresAt)
}

// Expire is ExpireQuote: ISSUED to EXPIRED once expiresAt has passed. Expiring
// an EXPIRED quote is a no-op; expiring one that is still binding is an
// illegal transition. It raises no event.
func (q *PriceQuote) Expire(now time.Time) error {
	if q.state == StateExpired {
		return nil
	}
	if now.Before(q.expiresAt) {
		return fmt.Errorf("%w: quote %s is binding until %s", shared.ErrIllegalTransition, q.id, q.expiresAt.Format(time.RFC3339))
	}
	q.state = StateExpired
	q.version++
	return nil
}

// PriceQuoteIssued is raised by Issue; it is analytics-only.
type PriceQuoteIssued struct {
	shared.Header
	QuoteID    ID
	CartID     string
	Currency   string
	TotalMinor int64
	ExpiresAt  time.Time
}

// EventName returns "PriceQuoteIssued".
func (PriceQuoteIssued) EventName() string { return "PriceQuoteIssued" }

// Entity returns "pricequote".
func (PriceQuoteIssued) Entity() string { return entity }

// SubjectID returns the quoteId.
func (e PriceQuoteIssued) SubjectID() string { return string(e.QuoteID) }
