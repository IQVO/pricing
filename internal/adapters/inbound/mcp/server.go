// Package mcp is the inbound Model Context Protocol adapter: it exposes this
// bounded context to the AI ecosystem as a third driving adapter over the same
// application-layer read use cases the REST adapter and the A2A agent use. It
// is built on the official MCP Go SDK and served over Streamable HTTP only.
//
// Per docs/adr/0009-read-only-mcp-server.md the surface is READ-ONLY: every
// tool reads pricing data (get_item_price, get_price_quote); setting prices,
// scheduling sales, issuing quotes and publishing the policy tables stay on
// REST, and governance_test.go fails the build on a write-verb tool name and
// on a write seam referenced from this package. This package depends inward on
// the application layer and the domain only, never on an outbound adapter or
// the REST adapter. The composition root (cmd/mcp) wires concrete repositories
// into the use cases. There is no auth of any kind (fleet-wide revert
// 2026-09-11): access control is the in-cluster ClusterIP boundary.
package mcp

import (
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// serverName and serverVersion identify this server in the MCP initialize
// handshake.
const (
	serverName    = "pricing-mcp"
	serverVersion = "1.0.0"
)

// instructions is what an MCP client is told about this server at initialize.
const instructions = "Pricing of the retail network storefront (read-only): the price book and the price quotes. " +
	"Read what one catalogue item sells for right now with get_item_price (state ACTIVE or WITHDRAWN, list price, the effective price at this " +
	"instant, which is the running sale price or else the list price, and every sale window), and read one price quote with get_price_quote " +
	"(ISSUED or EXPIRED at this instant, its lines, the delivery charge per shipment group, tax, total and the versions it used). Amounts " +
	"are minor units of the stated currency. The item price resource (warehouse://pricing/item-prices/{catalogItemId}) loads the same item " +
	"price without a tool call, and the explain_item_price prompt hands you this context's operator procedure. Nothing here changes data: " +
	"setting list prices, scheduling and cancelling sales, withdrawing prices, issuing quotes and publishing the delivery charge schedule and " +
	"the tax table are REST operations of pricing."

// NewServer builds the MCP server for this bounded context with every tool,
// resource and workflow prompt registered.
func NewServer(deps Deps) *mcp.Server {
	server := mcp.NewServer(
		&mcp.Implementation{Name: serverName, Version: serverVersion},
		&mcp.ServerOptions{Instructions: instructions},
	)
	deps.registerTools(server)
	deps.registerResources(server)
	deps.registerPrompts(server)
	return server
}

// Handler returns the Streamable HTTP handler for the MCP server.
func Handler(server *mcp.Server) http.Handler {
	// Stateless (Agentic Standard v2, AS-4): no Mcp-Session-Id is issued, so
	// the server also answers the 2026-07-28 revision's per-request
	// handshake (server/discover) while legacy initialize clients keep
	// working. Nothing here relies on cross-call session state.
	return mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{Stateless: true},
	)
}
