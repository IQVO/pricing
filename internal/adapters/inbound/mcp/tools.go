package mcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/IQVO/pricing/internal/application/usecases"
)

// Deps is everything the MCP surface needs, injected by the composition
// root: EXACTLY the two query use cases that back a REST GET and an A2A skill
// (GET /item-prices/{catalogItemId} and GET /price-quotes/{quoteId}). The
// adapter holds these and nothing that can write (docs/adr/0009): the
// composition root (cmd/mcp) wires only the item-price and quote repositories
// and the clock into them.
type Deps struct {
	GetItemPrice  usecases.GetItemPrice
	GetPriceQuote usecases.GetPriceQuote
}

// --- inputs -------------------------------------------------------------------

// itemPriceInput mirrors the path parameter of GET /item-prices/{catalogItemId}.
type itemPriceInput struct {
	CatalogItemID string `json:"catalogItemId" jsonschema:"the catalogue item id whose price is read, e.g. sku-1"`
}

// priceQuoteInput mirrors the path parameter of GET /price-quotes/{quoteId}.
type priceQuoteInput struct {
	QuoteID string `json:"quoteId" jsonschema:"the price quote id, as returned when the quote was issued"`
}

const (
	itemNotFound  = "no item price exists for that catalogItemId"
	quoteNotFound = "no price quote exists for that quoteId"
)

// --- registry -----------------------------------------------------------------

// registerTools adds the two read-only tools, one per read the A2A agent
// already offers. Every tool is annotated read-only, idempotent and
// closed-world (the annotation literal is inlined per tool so the fleet's
// static authority sensor can prove ReadOnlyHint) and declares its authority
// level L1 under com.warehouse/authority (Agentic Standard v2, ADR 0009): each
// is a pure read of this context's own state. TestToolSurface pins the set and
// fails the build on any write-verb name.
func (d Deps) registerTools(server *mcp.Server) {
	closedWorld := false

	addTool(server, &mcp.Tool{
		Name: "get_item_price",
		Description: "Read what one catalogue item sells for: its state (ACTIVE or WITHDRAWN), the list price, the effective price at this instant " +
			"(the running sale price, else the list price), every sale window (saleId, price, from and to instants) and the version. Amounts are " +
			"minor units of the stated currency. Read-only; an item with no price fails with not-found.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closedWorld},
		Meta:        mcp.Meta{authorityMetaKey: authorityL1},
	}, d.getItemPrice)

	addTool(server, &mcp.Tool{
		Name: "get_price_quote",
		Description: "Read one price quote: ISSUED or EXPIRED at this instant (a quote past its expiresAt is EXPIRED whether or not the policy stored it " +
			"yet), the cart it priced, its lines (unit price and line total), the delivery charge per shipment group, tax, total, the price, charge " +
			"schedule and tax table versions it used, and issuedAt and expiresAt. Amounts are minor units of the quote currency. Read-only: it never " +
			"issues a quote; an unknown id fails with not-found.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closedWorld},
		Meta:        mcp.Meta{authorityMetaKey: authorityL1},
	}, d.getPriceQuote)
}

// addTool registers one tool. A handler error is returned to the SDK as the
// handler's error, which the SDK turns into an isError tool result (never a
// transport/protocol failure), carrying the error text.
func addTool[In, Out any](
	server *mcp.Server,
	tool *mcp.Tool,
	handle func(context.Context, In) (Out, error),
) {
	mcp.AddTool(server, tool, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		out, err := handle(ctx, in)
		if err != nil {
			var zero Out
			return nil, zero, err
		}
		return nil, out, nil
	})
}

// --- handlers -----------------------------------------------------------------

func (d Deps) getItemPrice(ctx context.Context, in itemPriceInput) (itemPriceView, error) {
	return d.readItemPrice(ctx, in.CatalogItemID)
}

// readItemPrice is the one read behind the get_item_price tool and the item
// price resource.
func (d Deps) readItemPrice(ctx context.Context, catalogItemID string) (itemPriceView, error) {
	if err := checkID("catalogItemId", catalogItemID, itemNotFound); err != nil {
		return itemPriceView{}, err
	}
	v, err := d.GetItemPrice.Execute(ctx, catalogItemID)
	if err != nil {
		return itemPriceView{}, mapError(ctx, err)
	}
	return itemPriceOut(v), nil
}

func (d Deps) getPriceQuote(ctx context.Context, in priceQuoteInput) (priceQuoteView, error) {
	if err := checkID("quoteId", in.QuoteID, quoteNotFound); err != nil {
		return priceQuoteView{}, err
	}
	v, err := d.GetPriceQuote.Execute(ctx, in.QuoteID)
	if err != nil {
		return priceQuoteView{}, mapError(ctx, err)
	}
	return priceQuoteOut(v), nil
}
