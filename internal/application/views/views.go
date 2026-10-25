// Package views holds the read shapes the use cases return: plain data the
// HTTP adapter maps to its DTOs. They never expose an aggregate.
package views

import (
	"time"

	"github.com/IQVO/pricing/internal/domain/itemprice"
	"github.com/IQVO/pricing/internal/domain/quote"
	"github.com/IQVO/pricing/internal/domain/schedule"
	"github.com/IQVO/pricing/internal/domain/shared"
	"github.com/IQVO/pricing/internal/domain/tax"
)

// SaleWindow is a sale window of an item price.
type SaleWindow struct {
	SaleID string
	Price  shared.Money
	From   time.Time
	To     time.Time
}

// ItemPrice is the list price, the sale windows and the effective price of one
// catalogue item.
type ItemPrice struct {
	CatalogItemID  string
	State          string
	ListPrice      shared.Money
	EffectivePrice shared.Money
	Sales          []SaleWindow
	Version        int64
}

// ItemPriceList is one page of item prices; Next is the cursor of the next
// page, empty on the last.
type ItemPriceList struct {
	Items []ItemPrice
	Next  string
}

// SaleWindowFrom maps a domain sale window.
func SaleWindowFrom(w itemprice.SaleWindow) SaleWindow {
	return SaleWindow{SaleID: w.SaleID, Price: w.Price, From: w.From, To: w.To}
}

// ItemPriceFrom maps an item price, computing the effective price at now.
func ItemPriceFrom(p *itemprice.ItemPrice, now time.Time) ItemPrice {
	sales := make([]SaleWindow, 0, len(p.Sales()))
	for _, w := range p.Sales() {
		sales = append(sales, SaleWindowFrom(w))
	}
	return ItemPrice{
		CatalogItemID: string(p.ID()), State: string(p.State()), ListPrice: p.ListPrice(),
		EffectivePrice: p.EffectivePrice(now), Sales: sales, Version: p.Version(),
	}
}

// TierCharge is the charge of one tier; FreeOver is the zero Money when the
// tier has no threshold.
type TierCharge struct {
	Tier     shared.Tier
	Charge   shared.Money
	FreeOver shared.Money
}

// ChargeSchedule is a delivery charge schedule.
type ChargeSchedule struct {
	ScheduleID string
	Version    int64
	State      string
	Tiers      []TierCharge
}

// ChargeScheduleFrom maps a schedule.
func ChargeScheduleFrom(s *schedule.DeliveryChargeSchedule) ChargeSchedule {
	tiers := make([]TierCharge, 0, len(s.Tiers()))
	for _, t := range s.Tiers() {
		tiers = append(tiers, TierCharge{Tier: t.Tier, Charge: t.Charge, FreeOver: t.FreeOver})
	}
	return ChargeSchedule{ScheduleID: string(s.ID()), Version: s.Version(), State: string(s.State()), Tiers: tiers}
}

// TaxBand is a tax band.
type TaxBand struct {
	PostalPrefixFrom string
	PostalPrefixTo   string
	RateBasisPoints  int
}

// TaxTable is a tax table.
type TaxTable struct {
	TaxTableID string
	Version    int64
	State      string
	Bands      []TaxBand
}

// TaxTableFrom maps a tax table.
func TaxTableFrom(t *tax.TaxTable) TaxTable {
	bands := make([]TaxBand, 0, len(t.Bands()))
	for _, b := range t.Bands() {
		bands = append(bands, TaxBand(b))
	}
	return TaxTable{TaxTableID: string(t.ID()), Version: t.Version(), State: string(t.State()), Bands: bands}
}

// QuoteLine is a priced line of a quote.
type QuoteLine struct {
	CatalogItemID string
	Quantity      int
	UnitPrice     shared.Money
	LineTotal     shared.Money
}

// QuoteGroup is a shipment group of a quote.
type QuoteGroup struct {
	GroupNo        int
	Tier           shared.Tier
	DeliveryCharge shared.Money
}

// Version keys of PriceQuote.Versions besides the per-item "price:<id>" ones.
const (
	VersionChargeSchedule = "chargeSchedule"
	VersionTaxTable       = "taxTable"
	VersionPricePrefix    = "price:"
)

// PriceQuote is a price quote. State is EXPIRED once expiresAt has passed,
// whether or not the clock-driven policy stored it yet.
type PriceQuote struct {
	QuoteID    string
	CartID     string
	State      string
	Currency   string
	Lines      []QuoteLine
	Groups     []QuoteGroup
	TaxMinor   int64
	TotalMinor int64
	// Versions holds the charge schedule, the tax table and every item price
	// version the quote used.
	Versions map[string]int64
	IssuedAt time.Time
	// ExpiresAt is issuedAt plus the QuoteTtl.
	ExpiresAt time.Time
}

// PriceQuoteFrom maps a quote, deriving the state at now.
func PriceQuoteFrom(q *quote.PriceQuote, now time.Time) PriceQuote {
	state := quote.StateIssued
	if q.ExpiredAt(now) {
		state = quote.StateExpired
	}
	lines := make([]QuoteLine, 0, len(q.Lines()))
	versions := map[string]int64{
		VersionChargeSchedule: q.ScheduleVersion(),
		VersionTaxTable:       q.TaxTableVersion(),
	}
	for _, l := range q.Lines() {
		lines = append(lines, QuoteLine{CatalogItemID: l.CatalogItemID, Quantity: l.Quantity, UnitPrice: l.UnitPrice, LineTotal: l.LineTotal})
		versions[VersionPricePrefix+l.CatalogItemID] = l.PriceVersion
	}
	groups := make([]QuoteGroup, 0, len(q.Groups()))
	for _, g := range q.Groups() {
		groups = append(groups, QuoteGroup{GroupNo: g.GroupNo, Tier: g.Tier, DeliveryCharge: g.DeliveryCharge})
	}
	return PriceQuote{
		QuoteID: string(q.ID()), CartID: q.CartID(), State: string(state), Currency: q.Currency(), Lines: lines, Groups: groups,
		TaxMinor: q.TaxMinor(), TotalMinor: q.TotalMinor(), Versions: versions, IssuedAt: q.IssuedAt(), ExpiresAt: q.ExpiresAt(),
	}
}
