# ADR 0009: A read-only MCP server with authority levels and a complete surface

## Status

Accepted (2026-11-14). Follows the fleet Agentic Standard v2 (AS-1 authority,
AS-3 complete surface, AS-4 stateless transport; warehouse-docs
`docs/architecture/agentic-standard.md`) and the fleet decision of one MCP server per
bounded context (warehouse-docs ADR 0008). Builds on [ADR 0007](./0007-a2a-inbound-adapter.md) (the
A2A adapter, whose two read skills this server mirrors) and
[ADR 0005](./0005-fleet-observability-standard.md) (observability).

## Context

pricing has a REST API and an A2A agent (ADR 0007) but no MCP server, so an agent that wants this
context's tools has to speak REST or delegate over A2A. The Agentic Standard v2 (AS-9) asks the
storefront contexts that have an A2A agent but no MCP server to gain one. The fleet rules for it:
one MCP server per bounded context, an additive inbound adapter over the existing use cases, the
official MCP Go SDK, Streamable HTTP only, a separate `cmd/mcp` binary, no authentication of any kind
(auth is reverted fleet-wide, `TestNoAuthMiddlewareReintroduced` pins that for the REST side), and an
MCP surface never wider than what the context already exposes.

## Decision

- **A third driving adapter, a separate binary.** `internal/adapters/inbound/mcp` is built on the
  official MCP Go SDK (`github.com/modelcontextprotocol/go-sdk`) and served over Streamable HTTP only
  (no stdio, no SSE) by `cmd/mcp`, mounted at `/mcp` and `/` with an open `GET /healthz`, port **8090**
  (`MCP_ADDR`). It ships in the same image as `/app/mcp`. The Helm chart gets an `mcp.*` component
  (`<release>-mcp` Deployment and Service, default **off**) and an optional gateway route
  `/api/mcp/pricing`.
- **Read-only charter: two tools, one per read the A2A agent already offers.** Each tool is a thin
  wrapper over a query use case that already backs a REST `GET`, and returns the fields that `GET`
  returns (the same field sets ADR 0007 pins for the A2A answers).

  | Tool | Input | Authority | Backing use case | REST counterpart | A2A counterpart |
  |---|---|---|---|---|---|
  | `get_item_price` | `catalogItemId` (required) | L1 | `GetItemPrice` | `GET /item-prices/{catalogItemId}` | skill `item_price` |
  | `get_price_quote` | `quoteId` (required) | L1 | `GetPriceQuote` | `GET /price-quotes/{quoteId}` | skill `quote_status` |

  `get_item_price` answers "what does this item sell for right now": state, list price, the effective
  price at this instant and every sale window. `get_price_quote` answers "what did this quote price and
  is it still valid": `ISSUED` or `EXPIRED` at this instant (derived from `expiresAt` as the REST read
  does), lines, delivery charge per group, tax, total and the versions used. An unknown id, an id longer
  than the REST path bound of 128 bytes, or one carrying U+0000 is `not-found` (the same classification
  as REST and the A2A agent; a NUL must never reach PostgreSQL), any other error is logged and reported
  as `internal-error` without its message. Tool errors are `isError` results whose text starts with the
  REST problem slug.
- **Authority (AS-1).** Every tool declares `Meta: authorityMeta(authorityL1)` under
  `com.warehouse/authority` and is annotated read-only, idempotent and closed-world. There is no L2
  (nothing recommends) and no L3/L4 (nothing writes), so the adapter declares only the `authorityL1`
  constant. `internal/architecture/mcp_authority_fitness_test.go` fails the build when a tool misses its
  declaration, when a level contradicts the read/write annotations, or when the server offers no
  resource or no prompt.
- **Complete surface (AS-3).** Exactly one resource and one prompt join the tools, both read-only:
  - `warehouse://pricing/item-prices/{catalogItemId}` (resource template, `application/json`): the
    current price of ONE item, the same `GetItemPrice` use case as the tool, never a table dump.
  - `explain_item_price` (prompt, one required argument `catalogItemId`): this context's operator
    procedure to read an item's price, interpret list versus effective price and sale windows, read a
    quote that references the item, and know what to hand to a human. It names tools and never calls
    them.
- **Stateless transport (AS-4).** The Streamable HTTP handler is built with
  `&mcp.StreamableHTTPOptions{Stateless: true}`: no `Mcp-Session-Id` is issued and the server answers the
  2026-07-28 revision's per-request handshake while a legacy `initialize` still answers 200.
- **Read-only is structural.** The adapter holds `Reads`, an interface of exactly the two query use
  cases; `cmd/mcp` wires only the item-price and quote repositories and the clock into the two queries,
  never a unit of work, outbox, inbox, Kafka or policy loop, and reads no Kafka variable.
  `TestToolSurface` pins the tool set, the read-only annotations and the absence of write verbs;
  `TestWriteSeamSensorFailsOnWriteSeams` fails the build if the adapter names a write use case or a
  transactional seam; `TestToolRegistryGolden` pins the whole advertised registry including `_meta`;
  `TestMCPAdapterDependencyRule` (already in `fitness_test.go`) keeps the adapter inward-only and
  imported by nothing; `TestMCPOptionalToolInputsCarryOmitempty` keeps optional inputs optional (this
  surface has none today). The integration test proves the binary reads what `cmd/api`'s use cases wrote
  in a real Postgres.
- **Observability.** `cmd/mcp` has the same `telemetry.go` shape as `cmd/api` and `cmd/a2a` (fleet OTel
  standard, ADR 0005): `service.name` `pricing-mcp`, the same request-logger and otelchi middleware
  order. No new metric is introduced.
- **No auth, no sibling calls.** The server reads this context's own tables only and has no auth layer;
  access control is the in-cluster ClusterIP boundary, as for REST and A2A.
- **Boot.** `DATABASE_URL` is mandatory; the binary runs the idempotent embedded migrations on start
  (direct DSN `MIGRATIONS_DATABASE_URL` when given) under the same `bootretry` budget as the other
  binaries.

### Left out on purpose

- `GET /item-prices` (the bootstrap list), `GET /delivery-charge-schedules/current` and
  `GET /tax-tables/current`: REST reads that ADR 0007 already declined as agent goals (projection
  rebuild input and quote inputs). They stay REST-only; adding one as a tool is a reviewed amendment of
  this ADR (golden, tool list and tests move together).
- `IssuePriceQuote` mints and holds a quote: a state change. It would be an L3 tool and needs its own
  ADR (with `initiated_by` attribution and an integration test proving the outbox row). Not exposed.
  The price-setting, sale and policy publication operations stay REST-only too.

## Consequences

- Agents can read a price and a quote with typed tools, one resource and one prompt, without REST glue.
- A third deployable (`/app/mcp`, port 8090) in the same image and an `mcp.*` chart block, default off:
  nothing changes in a running cluster until the infra switch-on.
- The MCP server holds a Postgres pool of its own (`postgres.MaxConns` = 10); a deployment that scales it
  should size Postgres connections for api, a2a and mcp replicas together.
- Adding any tool is a reviewed act: the curated list, the golden file and the tests change together.
