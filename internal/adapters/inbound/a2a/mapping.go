package a2a

import (
	"fmt"
	"time"

	"github.com/IQVO/pricing/internal/application/views"
	"github.com/IQVO/pricing/internal/domain/shared"
)

// problemTypeBase namespaces this context's problem types, mirroring the
// RFC 7807 practice of the REST adapter.
const problemTypeBase = "https://errors.pricing.warehouse-systems.dev"

// problem is the RFC 7807-shaped rejection an agent client receives instead
// of a result: domain rejections and request-shape failures are business
// answers, not transport errors, so they travel in the message envelope.
type problem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Status int    `json:"status"`
}

// summary renders the one-line form used for the reply's text part.
func (p *problem) summary() string {
	return fmt.Sprintf("%s (%d): %s", p.Title, p.Status, p.Detail)
}

func problemMalformedRequest(detail string) *problem {
	return &problem{Type: problemTypeBase + "/malformed-skill-request", Title: "Malformed skill request", Detail: detail, Status: 400}
}

func problemUnknownSkill(skill string) *problem {
	return &problem{Type: problemTypeBase + "/unknown-skill", Title: "Unknown skill", Detail: fmt.Sprintf("skill %q is not offered by this agent; the Agent Card lists the supported skills", skill), Status: 400}
}

func problemInvalidArgument(err error) *problem {
	return &problem{Type: problemTypeBase + "/invalid-argument", Title: "Invalid skill argument", Detail: err.Error(), Status: 400}
}

// envelope is the structured payload of every skill answer: exactly one of
// Data (a skill DTO) or Problem (why there is no result), plus the skill id
// the answer belongs to.
type envelope struct {
	Skill   string     `json:"skill"`
	Data    *skillData `json:"data,omitempty"`
	Problem *problem   `json:"problem,omitempty"`
}

// skillData is the union of the skill result DTOs: each skill fills its own
// field, keeping the envelope flat and versioned as one contract.
type skillData struct {
	ItemPrice  *itemPriceDTO  `json:"itemPrice,omitempty"`
	PriceQuote *priceQuoteDTO `json:"priceQuote,omitempty"`
}

// moneyDTO mirrors the REST adapter's money shape: minor units plus ISO 4217.
type moneyDTO struct {
	AmountMinor int64  `json:"amountMinor"`
	Currency    string `json:"currency"`
}

func moneyOut(m shared.Money) moneyDTO {
	return moneyDTO{AmountMinor: m.AmountMinor, Currency: m.Currency}
}

type saleWindowDTO struct {
	SaleID string    `json:"saleId"`
	Price  moneyDTO  `json:"price"`
	From   time.Time `json:"from"`
	To     time.Time `json:"to"`
}

// itemPriceDTO carries exactly the fields GET /item-prices/{catalogItemId}
// returns, nothing more.
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
		sales = append(sales, saleWindowDTO{SaleID: w.SaleID, Price: moneyOut(w.Price), From: w.From.UTC(), To: w.To.UTC()})
	}
	return itemPriceDTO{
		CatalogItemID: v.CatalogItemID, State: v.State, ListPrice: moneyOut(v.ListPrice),
		EffectivePrice: moneyOut(v.EffectivePrice), Sales: sales, Version: v.Version,
	}
}

func (d itemPriceDTO) summary() string {
	return fmt.Sprintf("%s %s: effective %d %s (list %d %s, minor units), %d sale window(s), version %d",
		d.CatalogItemID, d.State, d.EffectivePrice.AmountMinor, d.EffectivePrice.Currency,
		d.ListPrice.AmountMinor, d.ListPrice.Currency, len(d.Sales), d.Version)
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

// priceQuoteDTO carries exactly the fields GET /price-quotes/{quoteId}
// returns, nothing more.
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

func priceQuoteOut(v views.PriceQuote) priceQuoteDTO {
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

func (d priceQuoteDTO) summary() string {
	return fmt.Sprintf("quote %s %s: total %d %s (minor units), %d line(s), expires %s",
		d.QuoteID, d.State, d.TotalMinor, d.Currency, len(d.Lines), d.ExpiresAt.Format(time.RFC3339))
}
