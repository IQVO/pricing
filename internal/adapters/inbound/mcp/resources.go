package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// mimeTypeJSON is the MIME type of every resource this server serves.
const mimeTypeJSON = "application/json"

// itemPriceURIScheme is the fixed prefix of the item price resource URI.
const itemPriceURIScheme = "warehouse://pricing/item-prices/"

// itemPriceURITemplate is the ONE resource this server offers (Agentic
// Standard v2, AS-3): a scoped read model this context owns, not a dump of a
// table. It answers "what does this item sell for right now?" for ONE item,
// built from the same GetItemPrice read use case the get_item_price tool and
// GET /item-prices/{catalogItemId} use. Bounded by construction: one item, the
// price's fixed fields, JSON text. The bootstrap list of every item price is
// deliberately not a resource.
const itemPriceURITemplate = itemPriceURIScheme + "{catalogItemId}"

// registerResources adds the resource surface (read-only by nature, L1; the
// charter keeps resources context data an agent can load without a tool
// call). No resource ever writes: Deps holds read use cases only (ADR 0009).
func (d Deps) registerResources(server *mcp.Server) {
	server.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: itemPriceURITemplate,
		Name:        "item price",
		Description: "The current price of one catalogue item: state (ACTIVE or WITHDRAWN), list price, the effective price at this instant, every sale window and the version. Amounts are minor units. An unknown item is an error.",
		MIMEType:    mimeTypeJSON,
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		catalogItemID, err := parseItemPriceURI(req.Params.URI)
		if err != nil {
			return nil, err
		}
		v, err := d.readItemPrice(ctx, catalogItemID)
		if err != nil {
			return nil, err
		}
		return resourceResult(req.Params.URI, v)
	})
}

// resourceResult marshals one read model into the resource's text contents.
func resourceResult(uri string, view any) (*mcp.ReadResourceResult, error) {
	body, err := json.Marshal(view)
	if err != nil {
		return nil, err
	}
	return &mcp.ReadResourceResult{
		Contents: []*mcp.ResourceContents{{
			URI:      uri,
			MIMEType: mimeTypeJSON,
			Text:     string(body),
		}},
	}, nil
}

// parseItemPriceURI extracts the catalogItemId from a
// warehouse://pricing/item-prices/{catalogItemId} URI. The id is
// model-supplied and therefore untrusted: a malformed URI or an empty id is
// rejected rather than silently defaulted, and a percent-encoded id is
// decoded so that an encoded NUL is caught by the same identifier checks as a
// tool argument.
func parseItemPriceURI(uri string) (string, error) {
	rest, ok := strings.CutPrefix(uri, itemPriceURIScheme)
	if !ok {
		return "", fmt.Errorf("resource uri %q must start with %q", uri, itemPriceURIScheme)
	}
	if rest == "" || strings.Contains(rest, "/") {
		return "", fmt.Errorf("resource uri %q must carry exactly one non-empty catalogItemId after %q", uri, itemPriceURIScheme)
	}
	id, err := url.PathUnescape(rest)
	if err != nil {
		return "", fmt.Errorf("resource uri %q carries an invalid percent-encoding: %w", uri, err)
	}
	return id, nil
}
