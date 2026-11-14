package mcp

import (
	"time"

	"github.com/IQVO/pricing/internal/application/views"
	"github.com/IQVO/pricing/internal/domain/shared"
)

// --- outputs (the REST bodies, camelCase like the REST contract) --------------
//
// They carry exactly the fields GET /item-prices/{catalogItemId} and
// GET /price-quotes/{quoteId} return, nothing more: no cost, no margin (this
// context holds none).

// moneyView mirrors the REST money shape: minor units plus ISO 4217.
type moneyView struct {
	AmountMinor int64  `json:"amountMinor"`
	Currency    string `json:"currency"`
}

func moneyOut(m shared.Money) moneyView {
	return moneyView{AmountMinor: m.AmountMinor, Currency: m.Currency}
}

type saleWindowView struct {
	SaleID string    `json:"saleId"`
	Price  moneyView `json:"price"`
	From   time.Time `json:"from"`
	To     time.Time `json:"to"`
}

// itemPriceView is the wire shape of one item price.
type itemPriceView struct {
	CatalogItemID  string           `json:"catalogItemId"`
	State          string           `json:"state"`
	ListPrice      moneyView        `json:"listPrice"`
	EffectivePrice moneyView        `json:"effectivePrice"`
	Sales          []saleWindowView `json:"sales"`
	Version        int64            `json:"version"`
}

func itemPriceOut(v views.ItemPrice) itemPriceView {
	sales := make([]saleWindowView, 0, len(v.Sales))
	for _, w := range v.Sales {
		sales = append(sales, saleWindowView{SaleID: w.SaleID, Price: moneyOut(w.Price), From: w.From.UTC(), To: w.To.UTC()})
	}
	return itemPriceView{
		CatalogItemID: v.CatalogItemID, State: v.State, ListPrice: moneyOut(v.ListPrice),
		EffectivePrice: moneyOut(v.EffectivePrice), Sales: sales, Version: v.Version,
	}
}

type quoteLineView struct {
	CatalogItemID string    `json:"catalogItemId"`
	Quantity      int       `json:"quantity"`
	UnitPrice     moneyView `json:"unitPrice"`
	LineTotal     moneyView `json:"lineTotal"`
}

type quoteGroupView struct {
	GroupNo        int       `json:"groupNo"`
	Tier           string    `json:"tier"`
	DeliveryCharge moneyView `json:"deliveryCharge"`
}

// priceQuoteView is the wire shape of one price quote. State is EXPIRED once
// expiresAt has passed, whether or not the clock-driven policy stored it yet.
type priceQuoteView struct {
	QuoteID    string           `json:"quoteId"`
	CartID     string           `json:"cartId"`
	State      string           `json:"state"`
	Currency   string           `json:"currency"`
	Lines      []quoteLineView  `json:"lines"`
	Groups     []quoteGroupView `json:"groups"`
	TaxMinor   int64            `json:"taxMinor"`
	TotalMinor int64            `json:"totalMinor"`
	Versions   map[string]int64 `json:"versions"`
	IssuedAt   time.Time        `json:"issuedAt"`
	ExpiresAt  time.Time        `json:"expiresAt"`
}

func priceQuoteOut(v views.PriceQuote) priceQuoteView {
	lines := make([]quoteLineView, 0, len(v.Lines))
	for _, l := range v.Lines {
		lines = append(lines, quoteLineView{CatalogItemID: l.CatalogItemID, Quantity: l.Quantity, UnitPrice: moneyOut(l.UnitPrice), LineTotal: moneyOut(l.LineTotal)})
	}
	groups := make([]quoteGroupView, 0, len(v.Groups))
	for _, g := range v.Groups {
		groups = append(groups, quoteGroupView{GroupNo: g.GroupNo, Tier: string(g.Tier), DeliveryCharge: moneyOut(g.DeliveryCharge)})
	}
	return priceQuoteView{
		QuoteID: v.QuoteID, CartID: v.CartID, State: v.State, Currency: v.Currency, Lines: lines, Groups: groups,
		TaxMinor: v.TaxMinor, TotalMinor: v.TotalMinor, Versions: v.Versions, IssuedAt: v.IssuedAt.UTC(), ExpiresAt: v.ExpiresAt.UTC(),
	}
}
