package mcp

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// explainItemPriceSOP is the operator procedure the explain_item_price prompt
// hands to the model (Agentic Standard v2, AS-3). Per the charter, prompts
// encode how to interpret results, when to escalate and what "done" means;
// this one names tools but NEVER calls them, and it cannot write: every tool
// it names is read-only (ADR 0009) and every write of this context is a REST
// operation.
const explainItemPriceSOP = `You are explaining what one catalogue item of the retail network sells for. Use only the MCP tools of pricing; never assume a price — read it.

Procedure:
1. Call get_item_price(catalogItemId = ITEM) (or load the resource warehouse://pricing/item-prices/ITEM). The answer carries state (ACTIVE or WITHDRAWN), listPrice, effectivePrice at this instant, every sale window (saleId, price, from, to) and the version. Amounts are minor units of the stated currency: 1999 BRL is R$ 19,99.
2. When the customer or another agent holds a price quote that mentions the item, call get_price_quote(quoteId) to read what was actually priced: the lines (unitPrice, lineTotal), the delivery charge per shipment group, taxMinor, totalMinor and the versions of the price, the charge schedule and the tax table it used.

Interpretation:
- effectivePrice is the running sale price when a sale window covers this instant, else the listPrice. A sale window that starts later or already ended does not change the price now; say when it starts or ended.
- A WITHDRAWN item is no longer sold at that price: report it as withdrawn, never quote its listPrice as current.
- A quote is a price held until expiresAt. EXPIRED means the held price is gone: it does not mean the item price changed, and it is not a current price. Compare the quote's price version (versions["price:<catalogItemId>"]) with the item's version to tell whether the price moved since.
- A not-found answer for an id means pricing holds nothing under that id; it is not evidence that the item is free or unavailable.

Escalate to a human pricing operator when: the effective price looks wrong for the sale windows you read, an ACTIVE item has no list price, or a customer disputes a quote. Setting or changing a price, scheduling or cancelling a sale, withdrawing a price, issuing a quote and publishing the delivery charges or the tax table are human/REST actions: nothing in this server can perform them, so do not promise them.

Done means: you have reported the item's state, listPrice, effectivePrice and any sale window with its dates, and, when a quote was in play, its state, total and expiry, and named the escalation when one applies.`

// registerPrompts adds the workflow prompt (the operator procedure of this
// context). Read-only by construction: a prompt returns text.
func (d Deps) registerPrompts(server *mcp.Server) {
	server.AddPrompt(&mcp.Prompt{
		Name:        "explain_item_price",
		Description: "Operator procedure for explaining what one catalogue item sells for: read the item price, interpret list versus effective price and sale windows, read a quote that references the item, and know when to hand over to a human pricing operator.",
		Arguments: []*mcp.PromptArgument{{
			Name:        "catalogItemId",
			Description: "the catalogue item id whose price to explain, e.g. sku-1",
			Required:    true,
		}},
	}, func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		itemID := strings.TrimSpace(req.Params.Arguments["catalogItemId"])
		if itemID == "" {
			itemID = "<catalogItemId>"
		}
		return &mcp.GetPromptResult{
			Description: "How to explain an item's price: read it, interpret list versus effective price and sale windows, read the quote when one is in play, and hand over to a human pricing operator when needed.",
			Messages: []*mcp.PromptMessage{{
				Role:    "user",
				Content: &mcp.TextContent{Text: strings.ReplaceAll(explainItemPriceSOP, "ITEM", itemID)},
			}},
		}, nil
	})
}
