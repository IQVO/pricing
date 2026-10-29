package http

import (
	"time"

	"github.com/IQVO/pricing/internal/application/usecases"
	"github.com/IQVO/pricing/internal/application/views"
	"github.com/IQVO/pricing/internal/domain/schedule"
	"github.com/IQVO/pricing/internal/domain/shared"
	"github.com/IQVO/pricing/internal/domain/tax"
)

// Request bodies (apis/openapi.yaml *Request schemas).

type moneyDTO struct {
	AmountMinor int64  `json:"amountMinor"`
	Currency    string `json:"currency"`
}

func (m moneyDTO) money() shared.Money {
	return shared.Money{AmountMinor: m.AmountMinor, Currency: m.Currency}
}

func moneyOut(m shared.Money) moneyDTO {
	return moneyDTO{AmountMinor: m.AmountMinor, Currency: m.Currency}
}

type createItemPriceRequest struct {
	CatalogItemID string   `json:"catalogItemId"`
	ListPrice     moneyDTO `json:"listPrice"`
}

type setListPriceRequest struct {
	ListPrice moneyDTO `json:"listPrice"`
	Version   int64    `json:"version"`
}

type scheduleSaleRequest struct {
	Price moneyDTO  `json:"price"`
	From  time.Time `json:"from"`
	To    time.Time `json:"to"`
}

type tierChargeDTO struct {
	Tier     string    `json:"tier"`
	Charge   moneyDTO  `json:"charge"`
	FreeOver *moneyDTO `json:"freeOver,omitempty"`
}

type publishScheduleRequest struct {
	Version int64           `json:"version"`
	Tiers   []tierChargeDTO `json:"tiers"`
}

func (r publishScheduleRequest) input() usecases.PublishChargeScheduleInput {
	tiers := make([]schedule.TierCharge, 0, len(r.Tiers))
	for _, t := range r.Tiers {
		tc := schedule.TierCharge{Tier: shared.Tier(t.Tier), Charge: t.Charge.money()}
		if t.FreeOver != nil {
			tc.FreeOver = t.FreeOver.money()
		}
		tiers = append(tiers, tc)
	}
	return usecases.PublishChargeScheduleInput{Version: r.Version, Tiers: tiers}
}

type taxBandDTO struct {
	PostalPrefixFrom string `json:"postalPrefixFrom"`
	PostalPrefixTo   string `json:"postalPrefixTo"`
	RateBasisPoints  int    `json:"rateBasisPoints"`
}

type publishTaxRequest struct {
	Version int64        `json:"version"`
	Bands   []taxBandDTO `json:"bands"`
}

func (r publishTaxRequest) input() usecases.PublishTaxTableInput {
	bands := make([]tax.TaxBand, 0, len(r.Bands))
	for _, b := range r.Bands {
		bands = append(bands, tax.TaxBand(b))
	}
	return usecases.PublishTaxTableInput{Version: r.Version, Bands: bands}
}

type quoteLineRequest struct {
	CatalogItemID string `json:"catalogItemId"`
	Quantity      int    `json:"quantity"`
}

type quoteGroupRequest struct {
	GroupNo int    `json:"groupNo"`
	OfferID string `json:"offerId"`
	Tier    string `json:"tier"`
	LineNos []int  `json:"lineNos"`
}

type issueQuoteRequest struct {
	CartID                  string              `json:"cartId"`
	Lines                   []quoteLineRequest  `json:"lines"`
	Groups                  []quoteGroupRequest `json:"groups"`
	DestinationPostalPrefix string              `json:"destinationPostalPrefix"`
}

func (r issueQuoteRequest) input() usecases.IssuePriceQuoteInput {
	in := usecases.IssuePriceQuoteInput{CartID: r.CartID, DestinationPostalPrefix: r.DestinationPostalPrefix}
	for _, l := range r.Lines {
		in.Lines = append(in.Lines, usecases.QuoteLine{CatalogItemID: l.CatalogItemID, Quantity: l.Quantity})
	}
	for _, g := range r.Groups {
		in.Groups = append(in.Groups, usecases.QuoteGroup{GroupNo: g.GroupNo, OfferID: g.OfferID, Tier: shared.Tier(g.Tier), LineNos: g.LineNos})
	}
	return in
}

// Responses.

type saleWindowDTO struct {
	SaleID string    `json:"saleId"`
	Price  moneyDTO  `json:"price"`
	From   time.Time `json:"from"`
	To     time.Time `json:"to"`
}

func saleOut(w views.SaleWindow) saleWindowDTO {
	return saleWindowDTO{SaleID: w.SaleID, Price: moneyOut(w.Price), From: w.From.UTC(), To: w.To.UTC()}
}

type itemPriceDTO struct {
	CatalogItemID  string          `json:"catalogItemId"`
	State          string          `json:"state"`
	ListPrice      moneyDTO        `json:"listPrice"`
	EffectivePrice moneyDTO        `json:"effectivePrice"`
	Sales          []saleWindowDTO `json:"sales"`
	Version        int64           `json:"version"`
}

func itemPriceOut(v views.ItemPrice) itemPriceDTO {
	sales := make([]saleWindowDTO, 0, len(v.Sales))
	for _, w := range v.Sales {
		sales = append(sales, saleOut(w))
	}
	return itemPriceDTO{
		CatalogItemID: v.CatalogItemID, State: v.State, ListPrice: moneyOut(v.ListPrice),
		EffectivePrice: moneyOut(v.EffectivePrice), Sales: sales, Version: v.Version,
	}
}

type itemPriceListDTO struct {
	Items []itemPriceDTO `json:"items"`
	Next  *string        `json:"next"`
}

func itemPriceListOut(v views.ItemPriceList) itemPriceListDTO {
	out := itemPriceListDTO{Items: make([]itemPriceDTO, 0, len(v.Items))}
	for _, p := range v.Items {
		out.Items = append(out.Items, itemPriceOut(p))
	}
	if v.Next != "" {
		out.Next = &v.Next
	}
	return out
}

type scheduleDTO struct {
	ScheduleID string          `json:"scheduleId"`
	Version    int64           `json:"version"`
	State      string          `json:"state"`
	Tiers      []tierChargeDTO `json:"tiers"`
}

func scheduleOut(v views.ChargeSchedule) scheduleDTO {
	tiers := make([]tierChargeDTO, 0, len(v.Tiers))
	for _, t := range v.Tiers {
		d := tierChargeDTO{Tier: string(t.Tier), Charge: moneyOut(t.Charge)}
		if !t.FreeOver.IsZero() {
			f := moneyOut(t.FreeOver)
			d.FreeOver = &f
		}
		tiers = append(tiers, d)
	}
	return scheduleDTO{ScheduleID: v.ScheduleID, Version: v.Version, State: v.State, Tiers: tiers}
}

type taxTableDTO struct {
	TaxTableID string       `json:"taxTableId"`
	Version    int64        `json:"version"`
	State      string       `json:"state"`
	Bands      []taxBandDTO `json:"bands"`
}

func taxTableOut(v views.TaxTable) taxTableDTO {
	bands := make([]taxBandDTO, 0, len(v.Bands))
	for _, b := range v.Bands {
		bands = append(bands, taxBandDTO(b))
	}
	return taxTableDTO{TaxTableID: v.TaxTableID, Version: v.Version, State: v.State, Bands: bands}
}

type quoteLineDTO struct {
	CatalogItemID string   `json:"catalogItemId"`
	Quantity      int      `json:"quantity"`
	UnitPrice     moneyDTO `json:"unitPrice"`
	LineTotal     moneyDTO `json:"lineTotal"`
}

type quoteGroupDTO struct {
	GroupNo        int      `json:"groupNo"`
	Tier           string   `json:"tier"`
	DeliveryCharge moneyDTO `json:"deliveryCharge"`
}

type priceQuoteDTO struct {
	QuoteID    string           `json:"quoteId"`
	CartID     string           `json:"cartId"`
	State      string           `json:"state"`
	Currency   string           `json:"currency"`
	Lines      []quoteLineDTO   `json:"lines"`
	Groups     []quoteGroupDTO  `json:"groups"`
	TaxMinor   int64            `json:"taxMinor"`
	TotalMinor int64            `json:"totalMinor"`
	Versions   map[string]int64 `json:"versions"`
	IssuedAt   time.Time        `json:"issuedAt"`
	ExpiresAt  time.Time        `json:"expiresAt"`
}

func quoteOut(v views.PriceQuote) priceQuoteDTO {
	lines := make([]quoteLineDTO, 0, len(v.Lines))
	for _, l := range v.Lines {
		lines = append(lines, quoteLineDTO{CatalogItemID: l.CatalogItemID, Quantity: l.Quantity, UnitPrice: moneyOut(l.UnitPrice), LineTotal: moneyOut(l.LineTotal)})
	}
	groups := make([]quoteGroupDTO, 0, len(v.Groups))
	for _, g := range v.Groups {
		groups = append(groups, quoteGroupDTO{GroupNo: g.GroupNo, Tier: string(g.Tier), DeliveryCharge: moneyOut(g.DeliveryCharge)})
	}
	return priceQuoteDTO{
		QuoteID: v.QuoteID, CartID: v.CartID, State: v.State, Currency: v.Currency, Lines: lines, Groups: groups,
		TaxMinor: v.TaxMinor, TotalMinor: v.TotalMinor, Versions: v.Versions, IssuedAt: v.IssuedAt.UTC(), ExpiresAt: v.ExpiresAt.UTC(),
	}
}
