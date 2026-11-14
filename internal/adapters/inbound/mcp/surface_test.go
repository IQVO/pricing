// The MCP surface beyond tools (Agentic Standard v2, AS-3): ONE resource (the
// scoped current-price read model of one item) and ONE prompt (this context's
// operator procedure). Pinned through the same in-memory client the tool
// tests use.
package mcp_test

import (
	"encoding/json"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const priceURIPrefix = "warehouse://pricing/item-prices/"

// The resource template is advertised with the fleet URI shape and reads the
// same read model as the get_item_price tool.
func TestSurface_ResourceTemplateListedAndRead(t *testing.T) {
	h := newHarness(t)
	h.priced("sku-1", 1000)
	h.saleRunningNow("sku-1", 800)
	ctx := t.Context()

	listed, err := h.session.ListResourceTemplates(ctx, nil)
	if err != nil {
		t.Fatalf("list resource templates: %v", err)
	}
	if len(listed.ResourceTemplates) != 1 || listed.ResourceTemplates[0].URITemplate != priceURIPrefix+"{catalogItemId}" {
		t.Fatalf("resource templates = %+v, want exactly %s{catalogItemId}", listed.ResourceTemplates, priceURIPrefix)
	}
	if got := listed.ResourceTemplates[0].MIMEType; got != "application/json" {
		t.Errorf("template MIMEType = %q, want application/json", got)
	}

	res, err := h.session.ReadResource(ctx, &sdk.ReadResourceParams{URI: priceURIPrefix + "sku-1"})
	if err != nil {
		t.Fatalf("read resource: %v", err)
	}
	if len(res.Contents) != 1 || res.Contents[0].URI != priceURIPrefix+"sku-1" {
		t.Fatalf("contents = %+v, want one entry for the requested URI", res.Contents)
	}
	var item struct {
		CatalogItemID  string `json:"catalogItemId"`
		State          string `json:"state"`
		EffectivePrice struct {
			AmountMinor int64 `json:"amountMinor"`
		} `json:"effectivePrice"`
	}
	if err := json.Unmarshal([]byte(res.Contents[0].Text), &item); err != nil {
		t.Fatalf("resource text is not the item price JSON: %v (%s)", err, res.Contents[0].Text)
	}
	if item.CatalogItemID != "sku-1" || item.State != "ACTIVE" || item.EffectivePrice.AmountMinor != 800 {
		t.Errorf("item price = %+v, want ACTIVE sku-1 effective 800", item)
	}
}

// A malformed URI and an unknown item are errors, never silent defaults.
func TestSurface_ResourceRejectsMalformedAndUnknown(t *testing.T) {
	h := newHarness(t)
	h.priced("sku-1", 1000)
	for _, uri := range []string{
		"staffing://pricing/item-prices/sku-1",     // wrong scheme
		"warehouse://pricing/price-quotes/sku-1",   // wrong model
		"warehouse://pricing/item-prices/",         // empty catalogItemId
		"warehouse://pricing/item-prices/sku-1/x",  // trailing segment
		"warehouse://pricing/item-prices/ghost",    // unknown item
		"warehouse://pricing/item-prices/sku%00-1", // NUL, never a lookup
	} {
		if _, err := h.session.ReadResource(t.Context(), &sdk.ReadResourceParams{URI: uri}); err == nil {
			t.Errorf("read resource %q: expected an error", uri)
		}
	}
}

// The prompt is advertised with its typed argument and returns the procedure.
func TestSurface_PromptListedAndGet(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()

	listed, err := h.session.ListPrompts(ctx, nil)
	if err != nil {
		t.Fatalf("list prompts: %v", err)
	}
	if len(listed.Prompts) != 1 || listed.Prompts[0].Name != "explain_item_price" {
		t.Fatalf("prompts = %+v, want exactly explain_item_price", listed.Prompts)
	}
	if args := listed.Prompts[0].Arguments; len(args) != 1 || args[0].Name != "catalogItemId" || !args[0].Required {
		t.Fatalf("prompt arguments = %+v, want one required catalogItemId", args)
	}

	res, err := h.session.GetPrompt(ctx, &sdk.GetPromptParams{Name: "explain_item_price", Arguments: map[string]string{"catalogItemId": "sku-1"}})
	if err != nil {
		t.Fatalf("get prompt: %v", err)
	}
	if len(res.Messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(res.Messages))
	}
	body := res.Messages[0].Content.(*sdk.TextContent).Text
	for _, needle := range []string{"get_item_price", "get_price_quote", "sku-1", "effectivePrice"} {
		if !strings.Contains(body, needle) {
			t.Errorf("procedure text does not mention %q", needle)
		}
	}

	// A missing argument degrades to the placeholder, never to a hidden call.
	noArg, err := h.session.GetPrompt(ctx, &sdk.GetPromptParams{Name: "explain_item_price"})
	if err != nil {
		t.Fatalf("get prompt without arguments: %v", err)
	}
	if plain := noArg.Messages[0].Content.(*sdk.TextContent).Text; !strings.Contains(plain, "<catalogItemId>") {
		t.Errorf("procedure without an argument should carry the <catalogItemId> placeholder")
	}
}
