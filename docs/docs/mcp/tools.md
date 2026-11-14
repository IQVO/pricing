---
id: tools
title: MCP tools
sidebar_label: MCP tools
sidebar_position: 2
description: "The two read-only MCP tools of pricing, every one declaring authority L1, plus the item price resource and the explain_item_price prompt, from the pinned tool registry."
---

# MCP tools

`cmd/mcp` serves a read-only [Model Context Protocol](https://modelcontextprotocol.io) server over **Streamable HTTP
only** ([ADR 0009](/docs/adr/0009-read-only-mcp-server)). It is unauthenticated like the rest of the fleet: the
in-cluster ClusterIP boundary is the access control.

| | |
| --- | --- |
| Listen address | `MCP_ADDR`, default `:8090` |
| Endpoint | `/mcp` (also mounted at `/`) |
| Probe | `GET /healthz` returns `200 {"status":"ok"}` |
| Needs | `DATABASE_URL` only; it dials no Kafka and registers no write seam |
| Gateway | `/api/mcp/pricing` (chart value `mcp.route.path`, rewritten to `/mcp`), when `mcp.route.enabled` is set |
| Deployed by | the chart behind `mcp.enabled` (default off) |

Source: `cmd/mcp/main.go`, `cmd/mcp/router.go`, `internal/adapters/inbound/mcp/{server,tools,tools_views,errors}.go`.

**How this page was produced.** The list below comes from the pinned tool registry
`internal/adapters/inbound/mcp/testdata/tool_registry.golden.json`, which `TestToolRegistryGolden` compares with the
server's `tools/list` on every test run; it was not captured from a running server. Run `tools/list` against a live
`cmd/mcp` to see the same two tools.

## Annotations and authority

Both tools carry `readOnlyHint: true`, `idempotentHint: true` and `openWorldHint: false`. There is no write tool: the
governance tests in `internal/adapters/inbound/mcp/governance_test.go` (`TestToolSurface` with its write-verb and
write-seam sensors) fail on a write-named tool or a write seam, `TestMCPAdapterDependencyRule` limits the adapter to the
application and domain layers, and `TestMCPServerIsReadOnly` limits the use cases the adapter and `cmd/mcp` may name to
`GetItemPrice` and `GetPriceQuote`. A tool call adds no `outbox_events` row (the integration test in
`cmd/mcp/main_integration_test.go` counts them).

Every tool also declares its **agent authority level** `L1` (Agentic Standard v2, [ADR 0009](/docs/adr/0009-read-only-mcp-server)):
a pure read of this context's own state. The declaration travels in `tools/list` as `_meta` under the key
`com.warehouse/authority`; the same level is pinned per tool in the golden registry and by the static sensor
`internal/architecture/mcp_authority_fitness_test.go`. Both A2A skills carry the equivalent tag `authority:L1`.

| Tool | Required input | Optional input | Annotation | Authority | Backing use case | REST counterpart | A2A counterpart |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `get_item_price` | `catalogItemId` | none | read-only | `L1` | `GetItemPrice` | `GET /item-prices/{catalogItemId}` | skill `item_price` |
| `get_price_quote` | `quoteId` | none | read-only | `L1` | `GetPriceQuote` | `GET /price-quotes/{quoteId}` | skill `quote_status` |

Every input schema is `additionalProperties: false`. A failure is an `isError` tool result whose text is
`<slug>: <detail>` with the REST slug (`not-found`, `validation-failed`); an untyped error is logged
(`mcp tool failed with an unexpected error`) and answered as `internal-error: an unexpected internal error occurred`,
never with the internal message. An identifier longer than 128 bytes or carrying U+0000 is `not-found`, as in REST and
the A2A agent, and never reaches a repository.

## `get_item_price`

Reads what one catalogue item sells for.

- **Input:** `catalogItemId` (string, required, for example `sku-1`).
- **Output:** `catalogItemId`, `state` (`ACTIVE` or `WITHDRAWN`), `listPrice` and `effectivePrice` (each
  `{amountMinor, currency}`: minor units and ISO 4217), `sales` (each `saleId`, `price`, `from`, `to`) and `version`.
  The effective price is the running sale price, else the list price. A withdrawn price is still answered, as the REST
  `GET` does.
- **Errors:** an item with no price is `not-found`; a blank id is `validation-failed`.

## `get_price_quote`

Reads one price quote.

- **Input:** `quoteId` (string, required).
- **Output:** `quoteId`, `cartId`, `state` (`ISSUED`, or `EXPIRED` once `expiresAt` has passed, whether or not the
  clock-driven policy stored it yet), `currency`, `lines` (each `catalogItemId`, `quantity`, `unitPrice`, `lineTotal`),
  `groups` (each `groupNo`, `tier`, `deliveryCharge`), `taxMinor`, `totalMinor`, `versions` (the charge schedule, the tax
  table and every `price:<catalogItemId>` version the quote used), `issuedAt` and `expiresAt`.
- **Errors:** an unknown id is `not-found`; a blank id is `validation-failed`. The tool never issues a quote.

## Transport

The Streamable HTTP handler is **stateless** ([ADR 0009](/docs/adr/0009-read-only-mcp-server)): no `Mcp-Session-Id` is
issued, the server answers the 2026-07-28 revision's per-request handshake (`server/discover`, no `initialize` needed)
and a legacy `initialize` still answers `200`. Nothing in the server relies on cross-call session state; pinned by the
raw-POST tests in `internal/adapters/inbound/mcp/stateless_test.go`.

## Resource: item price

`warehouse://pricing/item-prices/{catalogItemId}` (`application/json`) is the one resource of this server: the current
price of ONE item, built from the same `GetItemPrice` read use case as the `get_item_price` tool and the REST
`GET /item-prices/{catalogItemId}`. Bounded by construction: one item, the price's fixed fields, JSON text, never a dump
of the price book. A malformed URI, an unknown item or an identifier that cannot name an item is an error.

## Prompt: `explain_item_price`

The one prompt of this server is the operator procedure for this context: read the item price, interpret list versus
effective price and sale windows, read a quote that references the item, and hand over to a human pricing operator when
the price looks wrong. It takes one typed, required argument (`catalogItemId`); it names tools and never calls them, and
nothing it names can write (the write operations of this context are REST-only). `prompts/get` returns the text with the
item id interpolated; a missing argument degrades to a `<catalogItemId>` placeholder, never to a hidden call.

## Left out on purpose

These are not tools of this server (ADR 0009, "Left out on purpose"):

- `GET /item-prices` (bootstrap list for projections), `GET /delivery-charge-schedules/current` and
  `GET /tax-tables/current`: REST-only reads, as for the A2A agent.
- `POST /price-quotes` (`IssuePriceQuote`) mints and holds a quote, so it is a state change: an L3 candidate that needs
  its own ADR. The price, sale and policy-publication operations stay REST-only.
