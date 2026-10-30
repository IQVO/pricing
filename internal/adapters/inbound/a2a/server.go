// Package a2a is the inbound Agent2Agent adapter: it exposes this bounded
// context to the fleet's agent ecosystem as another driving adapter over the
// same application-layer use cases the REST adapter uses. It is built on the
// official A2A Go SDK (a2aproject/a2a-go) and speaks JSON-RPC 2.0 over HTTP,
// with the Agent Card served at the well-known path.
//
// Per ADR 0007, A2A is the DELEGATION channel between bounded contexts: an
// agent client sends a goal-shaped skill request as a structured message part
// and this agent answers from its own state through its own use cases. It is
// not a data-sync channel: facts between contexts still travel exclusively as
// CloudEvents on Kafka (ADR 0004).
//
// This agent is READ-ONLY. pricing has no MCP surface, so both skills are
// backed by a query use case that a REST GET already exposes, and they return
// exactly the fields that GET returns. No command use case is reachable from
// this package (Deps holds queries only; the architecture fitness test pins
// that), so no A2A call can change state or write an outbox row.
//
// Like the REST adapter, this package depends inward on the application layer
// (use cases) only: never on an outbound adapter. The composition root
// (cmd/a2a) wires concrete repositories into the use cases. Domain structs
// never leak across the message boundary.
package a2a

import (
	"context"
	"iter"
	"net/http"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/IQVO/pricing/internal/application/usecases"
)

// Config carries the deployment-specific identity of this agent: the absolute
// URL clients use to reach its JSON-RPC endpoint (advertised on the Agent
// Card) and the card version. It is supplied by the composition root from
// A2A_AGENT_URL; the card MUST advertise a URL the fleet's agent clients can
// actually dial (in-cluster: http://<release>-a2a.<ns>.svc.cluster.local:8093/).
type Config struct {
	AgentURL string
	Version  string
}

// Deps is everything the A2A skills need, injected by the composition root.
// It carries query use cases only, the same ones the REST GET routes use; the
// adapter never constructs an outbound adapter itself and offers no command.
type Deps struct {
	// GetItemPrice is the existing query behind GET /item-prices/{catalogItemId}.
	GetItemPrice usecases.GetItemPrice
	// GetPriceQuote is the existing query behind GET /price-quotes/{quoteId}.
	GetPriceQuote usecases.GetPriceQuote
}

// Card builds this agent's Agent Card (ADR 0007). The surface is two curated,
// goal-shaped READ skills, not one skill per endpoint: item_price answers
// "what does this catalogue item sell for right now, and which sales are
// planned", quote_status answers "what did this quote price and is it still
// valid". The card declares no security schemes on purpose: this surface is
// unauthenticated by the same fleet-wide decision as REST; schemes are added
// here, not in a redesign, if that decision is ever reversed.
func Card(cfg Config) *a2a.AgentCard {
	return &a2a.AgentCard{
		Name:        "pricing",
		Description: "Pricing agent for the retail network: answers what a catalogue item sells for right now (state, list price, effective price at this instant, planned and running sale windows) and what a previously issued price quote priced (lines, delivery charges, tax, total, and whether it is still ISSUED or already EXPIRED). It is read-only: it returns what the REST GET routes return and never changes a price, a sale or a quote, and it exposes no cost or margin data.",
		Version:     cfg.Version,
		Provider: &a2a.AgentProvider{
			Org: "IQVO",
			URL: "https://github.com/IQVO/pricing",
		},
		SupportedInterfaces: []*a2a.AgentInterface{
			a2a.NewAgentInterface(cfg.AgentURL, a2a.TransportProtocolJSONRPC),
		},
		DefaultInputModes:  []string{"application/json", "text/plain"},
		DefaultOutputModes: []string{"application/json", "text/plain"},
		Capabilities:       a2a.AgentCapabilities{Streaming: false},
		Skills: []a2a.AgentSkill{
			{
				ID:          skillItemPrice,
				Name:        "Current price of a catalogue item",
				Description: "Read of one item price by catalogItemId: its state (ACTIVE or WITHDRAWN), list price, the effective price at this instant (the running sale price, else the list price), every sale window with its saleId, price and from/to instants, and the version. Amounts are minor units of the stated currency. Answers a 404 problem when the item has no price.",
				Tags:        []string{"pricing", "item-price", "read"},
				InputModes:  []string{"application/json"},
				Examples: []string{
					`{"skill":"item_price","arguments":{"catalogItemId":"sku-1"}}`,
				},
			},
			{
				ID:          skillQuoteStatus,
				Name:        "Status and breakdown of an issued price quote",
				Description: "Read of one price quote by quoteId: ISSUED or EXPIRED at this instant (a quote past its expiresAt is EXPIRED whether or not the policy stored it yet), the cart it priced, its lines (unit price and line total), the delivery charge per shipment group, tax, total, the price/schedule/tax-table versions it used, and issuedAt/expiresAt. Amounts are minor units of the quote currency. Answers a 404 problem for an unknown quote. It never issues a quote.",
				Tags:        []string{"pricing", "price-quote", "read"},
				InputModes:  []string{"application/json"},
				Examples: []string{
					`{"skill":"quote_status","arguments":{"quoteId":"quote-1"}}`,
				},
			},
		},
	}
}

// NewExecutor returns the AgentExecutor that maps an incoming message to a
// skill and answers with a single agent Message (a message-only agent in A2A
// terms): both curated skills are request/response reads, so there is no
// long-running Task lifecycle to track. The response carries the caller's
// contextId (and taskId when the client referenced one) so a delegating saga
// can group its interactions.
func NewExecutor(deps Deps) a2asrv.AgentExecutor {
	return a2asrv.AgentExecutorFunc(func(ctx context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
		return func(yield func(a2a.Event, error) bool) {
			yield(answer(ctx, deps, ec), nil)
		}
	})
}

// Handler wires the executor into the transport-agnostic A2A request handler
// and wraps it in the JSON-RPC 2.0 HTTP handler: the only transport this
// agent ships (ADR 0007).
func Handler(executor a2asrv.AgentExecutor) http.Handler {
	return a2asrv.NewJSONRPCHandler(a2asrv.NewHandler(executor))
}
