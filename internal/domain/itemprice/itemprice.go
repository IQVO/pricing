// Package itemprice is the ItemPrice aggregate: the list price and the sale
// windows of one catalogue item, versioned. The effective price is the lowest
// active sale price, else the list price.
//
// States: ACTIVE, WITHDRAWN (WithdrawPrice; a withdrawn price is never
// revived, ADR 0002 A3). Invariants, by the model's names: every amount is
// zero or more (shared.ErrNegativeAmount); every price uses the configured
// currency (shared.ErrCurrencyMismatch); a sale price is below the list price
// (ErrSaleNotBelowList); the sale windows of one item never overlap
// (ErrOverlappingSaleWindow); the item is known and not discontinued in the
// local CatalogItemView (ErrUnknownCatalogItem).
//
// Publication: every command ends in settle, which raises ItemPriceChanged
// exactly when the published view (list price, effective price, sale end)
// differs from the one last published. That single rule is also the policy
// PublishEffectivePriceOnWindowBoundary (Reevaluate): re-running it for the
// same boundary raises nothing the second time (ADR 0002 A4).
package itemprice

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/IQVO/pricing/internal/domain/shared"
)

const entity = "itemprice"

// The invariants of the model, by the model's names.
var (
	// ErrSaleNotBelowList: a sale price is below the list price (422
	// sale-not-below-list).
	ErrSaleNotBelowList = errors.New("sale price not below list price")
	// ErrOverlappingSaleWindow: the sale windows of one item never overlap
	// (409 overlapping-sale-window, as apis/openapi.yaml declares).
	ErrOverlappingSaleWindow = errors.New("overlapping sale window")
	// ErrUnknownCatalogItem: the item is unknown or discontinued in the local
	// CatalogItemView (422 unknown-catalog-item).
	ErrUnknownCatalogItem = errors.New("unknown catalog item")
	// ErrUnknownSale: the item has no such sale window (404 not-found).
	ErrUnknownSale = errors.New("unknown sale")
)

// MaxSales is the most sale windows one item holds (ADR 0002, A6). A plain
// literal on purpose.
const MaxSales = 50

// ID identifies an item price: it is the catalogItemId.
type ID string

// State is the lifecycle state of an item price.
type State string

// The two states.
const (
	StateActive    State = "ACTIVE"
	StateWithdrawn State = "WITHDRAWN"
)

// Valid reports whether s is one of the two states.
func (s State) Valid() bool { return s == StateActive || s == StateWithdrawn }

// CatalogState is what the local CatalogItemView says about an item.
type CatalogState string

// The three answers of the view.
const (
	CatalogUnknown      CatalogState = "UNKNOWN"
	CatalogActive       CatalogState = "ACTIVE"
	CatalogDiscontinued CatalogState = "DISCONTINUED"
)

// Inputs is what SetListPrice and ScheduleSale check a command against.
type Inputs struct {
	// Currency is the one configured currency.
	Currency string
	// Catalog is the CatalogItemView entry of the item.
	Catalog CatalogState
	// Now is the caller's clock.
	Now time.Time
}

// SaleWindow is a time-bounded sale price: active from From (inclusive) until
// To (exclusive).
type SaleWindow struct {
	SaleID string
	Price  shared.Money
	From   time.Time
	To     time.Time
}

func (w SaleWindow) activeAt(at time.Time) bool { return !at.Before(w.From) && at.Before(w.To) }

// View is the published face of an item price: what ItemPriceChanged carries.
type View struct {
	ListMinor      int64
	EffectiveMinor int64
	// SaleEndsAt is the end of the active sale; zero at list price.
	SaleEndsAt time.Time
}

// ItemPrice is the aggregate root.
type ItemPrice struct {
	id          ID
	state       State
	list        shared.Money
	sales       []SaleWindow
	version     int64
	loaded      int64
	evaluatedAt time.Time
	published   View
}

// ID returns the catalogItemId.
func (p *ItemPrice) ID() ID { return p.id }

// State returns the lifecycle state.
func (p *ItemPrice) State() State { return p.state }

// ListPrice returns the list price.
func (p *ItemPrice) ListPrice() shared.Money { return p.list }

// Sales returns a copy of the sale windows, earliest first.
func (p *ItemPrice) Sales() []SaleWindow { return slices.Clone(p.sales) }

// Version returns the aggregate version; every change adds one.
func (p *ItemPrice) Version() int64 { return p.version }

// LoadedVersion returns the version the aggregate had when it was loaded (0
// for a new one): the version a repository expects to find stored.
func (p *ItemPrice) LoadedVersion() int64 { return p.loaded }

// MarkPersisted records that the current version is stored.
func (p *ItemPrice) MarkPersisted() { p.loaded = p.version }

// EvaluatedAt returns the instant up to which window boundaries were
// published.
func (p *ItemPrice) EvaluatedAt() time.Time { return p.evaluatedAt }

// EffectivePrice returns the lowest active sale price at the instant, else the
// list price.
func (p *ItemPrice) EffectivePrice(at time.Time) shared.Money {
	return effective(p.list, p.sales, at)
}

// NextBoundary returns the earliest instant after EvaluatedAt at which a sale
// window starts or ends; zero when there is none or the price is withdrawn.
func (p *ItemPrice) NextBoundary() time.Time { return nextBoundary(p.state, p.sales, p.evaluatedAt) }

func effective(list shared.Money, sales []SaleWindow, at time.Time) shared.Money {
	best := list
	for _, w := range sales {
		if w.activeAt(at) && w.Price.AmountMinor < best.AmountMinor {
			best = w.Price
		}
	}
	return best
}

func saleEnd(list shared.Money, sales []SaleWindow, at time.Time) time.Time {
	best, end := list.AmountMinor, time.Time{}
	for _, w := range sales {
		if w.activeAt(at) && w.Price.AmountMinor < best {
			best, end = w.Price.AmountMinor, w.To
		}
	}
	return end
}

func nextBoundary(state State, sales []SaleWindow, after time.Time) time.Time {
	if state != StateActive {
		return time.Time{}
	}
	var next time.Time
	consider := func(t time.Time) {
		if t.After(after) && (next.IsZero() || t.Before(next)) {
			next = t
		}
	}
	for _, w := range sales {
		consider(w.From)
		consider(w.To)
	}
	return next
}

// Create prices a catalogue item for the first time (SetListPrice on an item
// without a price). It raises ItemPriceChanged.
func Create(id ID, list shared.Money, in Inputs) (*ItemPrice, []shared.Event, error) {
	if !shared.ValidID(string(id)) {
		return nil, nil, shared.Invalid("catalogItemId %q is not a valid id", id)
	}
	if err := checkList(list, nil, in); err != nil {
		return nil, nil, err
	}
	p := &ItemPrice{id: id, state: StateActive, list: list}
	return p, p.settle(in.Now), nil
}

// SetListPrice changes the list price of an ACTIVE item price. It raises
// ItemPriceChanged when the list or the effective price moved; an active sale
// keeps winning over the new list price.
func (p *ItemPrice) SetListPrice(list shared.Money, in Inputs) ([]shared.Event, error) {
	if p.state != StateActive {
		return nil, illegal("set the list price", p.state)
	}
	if err := checkList(list, p.sales, in); err != nil {
		return nil, err
	}
	p.list = list
	return p.settle(in.Now), nil
}

func checkList(list shared.Money, sales []SaleWindow, in Inputs) error {
	if err := checkCatalog(in.Catalog); err != nil {
		return err
	}
	if err := list.Check(in.Currency); err != nil {
		return err
	}
	for _, w := range sales {
		if w.Price.AmountMinor >= list.AmountMinor {
			return saleNotBelow(w.Price, list)
		}
	}
	return nil
}

func checkCatalog(c CatalogState) error {
	if c != CatalogActive {
		return fmt.Errorf("%w: the item is %s in the catalogue view", ErrUnknownCatalogItem, c)
	}
	return nil
}

func saleNotBelow(sale, list shared.Money) error {
	return fmt.Errorf("%w: sale price %d, list price %d", ErrSaleNotBelowList, sale.AmountMinor, list.AmountMinor)
}

// ScheduleSale adds a sale window. A window that is already open changes the
// effective price and raises ItemPriceChanged; a future one raises nothing
// now, the boundary policy publishes it when it opens.
func (p *ItemPrice) ScheduleSale(w SaleWindow, in Inputs) ([]shared.Event, error) {
	if p.state != StateActive {
		return nil, illegal("schedule a sale", p.state)
	}
	if err := checkCatalog(in.Catalog); err != nil {
		return nil, err
	}
	if !shared.ValidID(w.SaleID) {
		return nil, shared.Invalid("saleId %q is not a valid id", w.SaleID)
	}
	if err := w.Price.Check(in.Currency); err != nil {
		return nil, err
	}
	w.From, w.To = shared.UTC(w.From), shared.UTC(w.To)
	if w.From.IsZero() || !w.To.After(w.From) {
		return nil, shared.Invalid("a sale window must end after it starts")
	}
	if !w.To.After(in.Now) {
		return nil, shared.Invalid("a sale window must end in the future")
	}
	if w.Price.AmountMinor >= p.list.AmountMinor {
		return nil, saleNotBelow(w.Price, p.list)
	}
	live := p.liveSales(in.Now)
	if len(live) >= MaxSales {
		return nil, shared.Invalid("an item holds at most %d sale windows", MaxSales)
	}
	for _, o := range live {
		if w.From.Before(o.To) && o.From.Before(w.To) {
			return nil, fmt.Errorf("%w: the window overlaps sale %q", ErrOverlappingSaleWindow, o.SaleID)
		}
	}
	live = append(live, w)
	p.sales = live
	slices.SortFunc(p.sales, func(a, b SaleWindow) int { return a.From.Compare(b.From) })
	return p.settle(in.Now), nil
}

// liveSales returns the windows that have not ended yet.
func (p *ItemPrice) liveSales(now time.Time) []SaleWindow {
	live := make([]SaleWindow, 0, len(p.sales)+1)
	for _, w := range p.sales {
		if w.To.After(now) {
			live = append(live, w)
		}
	}
	return live
}

// CancelSale removes a sale window. Cancelling the sale that is live reverts
// the effective price to the list price and raises ItemPriceChanged (there is
// no event of its own, model-gap decision 2).
func (p *ItemPrice) CancelSale(saleID string, now time.Time) ([]shared.Event, error) {
	if p.state != StateActive {
		return nil, illegal("cancel a sale", p.state)
	}
	i := slices.IndexFunc(p.sales, func(w SaleWindow) bool { return w.SaleID == saleID })
	if i < 0 {
		return nil, fmt.Errorf("%w: item %q has no sale %q", ErrUnknownSale, p.id, saleID)
	}
	p.sales = slices.Delete(p.sales, i, i+1)
	return p.settle(now), nil
}

// WithdrawPrice moves ACTIVE to WITHDRAWN and raises ItemPriceWithdrawn.
// Withdrawing a withdrawn price is an illegal transition.
func (p *ItemPrice) WithdrawPrice(now time.Time) ([]shared.Event, error) {
	if p.state != StateActive {
		return nil, illegal("withdraw the price", p.state)
	}
	p.state = StateWithdrawn
	p.version++
	p.evaluatedAt = shared.UTC(now)
	return []shared.Event{ItemPriceWithdrawn{
		Header:        shared.Header{Version: p.version, At: shared.UTC(now)},
		CatalogItemID: p.id,
	}}, nil
}

// Reevaluate is the policy PublishEffectivePriceOnWindowBoundary: it publishes
// the effective price at the instant when it differs from the one last
// published, and records the boundaries up to the instant as handled.
func (p *ItemPrice) Reevaluate(now time.Time) ([]shared.Event, error) {
	if p.state != StateActive {
		return nil, illegal("re-evaluate the price", p.state)
	}
	return p.settle(now), nil
}

// settle ends every command: it bumps the version, notes the evaluation
// instant and raises ItemPriceChanged when the published view moved.
func (p *ItemPrice) settle(now time.Time) []shared.Event {
	now = shared.UTC(now)
	p.version++
	p.evaluatedAt = now
	view := View{
		ListMinor:      p.list.AmountMinor,
		EffectiveMinor: effective(p.list, p.sales, now).AmountMinor,
		SaleEndsAt:     saleEnd(p.list, p.sales, now),
	}
	if view == p.published {
		return nil
	}
	p.published = view
	return []shared.Event{ItemPriceChanged{
		Header:              shared.Header{Version: p.version, At: now},
		CatalogItemID:       p.id,
		Currency:            p.list.Currency,
		ListPriceMinor:      view.ListMinor,
		EffectivePriceMinor: view.EffectiveMinor,
		SaleEndsAt:          view.SaleEndsAt,
	}}
}

func illegal(what string, s State) error {
	return fmt.Errorf("%w: cannot %s while the price is %s", shared.ErrIllegalTransition, what, s)
}
