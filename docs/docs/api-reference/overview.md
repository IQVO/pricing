---
id: overview
title: API overview
slug: /api-reference
sidebar_position: 1
description: "Pricing's interfaces (REST, events, A2A), the endpoint table and the contract conventions."
---

# API overview

`cmd/api` serves every route below (`internal/adapters/inbound/http`) on
`HTTP_ADDR` (default `:8080`); `cmd/a2a` serves the A2A agent. The contracts are
`apis/openapi.yaml` and `apis/asyncapi.yaml`. The router registers exactly the
thirteen API operations of the spec plus `GET /healthz` and `GET /readyz`, which
the spec does not list. There is no auth: the whole surface is unauthenticated.
See the [Quickstart](/docs/overview/quickstart) for example calls.

## Interfaces

| Interface | Source of truth | Notes |
| --- | --- | --- |
| REST | `apis/openapi.yaml` (OpenAPI 3.0.3, version 1.0.0) | The pages under *Pricing API* in this section are generated from the spec with `npm run gen-api-docs`; do not edit them by hand. The spec names `http://localhost:8080`; through Kong on the kind cluster the base path is `/api/pricing`. |
| Events | `apis/asyncapi.yaml` (AsyncAPI 3.0.0, version 1.0.0) | Summarised on the [event catalogue](/docs/api-reference/events); every field on [Domain events](/docs/ddd/domain-events). Five published types on `retailnetwork.pricing.events` and `retailnetwork.pricing.analytics`; two consumed from `retailnetwork.offer-availability.events`. |
| MCP | none | There is no `cmd/mcp`; this context has no MCP surface. |
| A2A | [ADR 0007](/docs/adr/0007-a2a-inbound-adapter), `internal/adapters/inbound/a2a` | `cmd/a2a` serves JSON-RPC 2.0 on `/` and `/a2a` (port 8093; through Kong `/api/a2a/pricing`) and the Agent Card at `/.well-known/agent-card.json`. Two read-only skills, no write skill: `item_price` (`catalogItemId`) and `quote_status` (`quoteId`), each returning what the matching REST `GET` returns. The skill request is the first message part, `{"skill": "<id>", "arguments": {...}}`, strict about unknown fields. Domain rejections are answered in the message envelope as problems (types under `https://errors.pricing.warehouse-systems.dev`), never as JSON-RPC errors. No auth, by fleet decision. `cmd/a2a` `GET /healthz` is `{"status":"ok"}`. |

## Endpoints

| Tag | Endpoint | Operation id | Success |
| --- | --- | --- | --- |
| ItemPrices | `POST /item-prices` | `createItemPrice` | `201`, the item price at version 1 |
| ItemPrices | `GET /item-prices` | `listItemPrices` | `200 {items, nextCursor}`, keyset-paged, ordered by `catalogItemId`, `limit` 1 to 500 (default 100) and `cursor` |
| ItemPrices | `GET /item-prices/{catalogItemId}` | `getItemPrice` | `200`, state, list price, effective price now, sales, version |
| ItemPrices | `PUT /item-prices/{catalogItemId}/list-price` | `setListPrice` | `200`, body `listPrice` and optional `version` guard; idempotent by method, no `Idempotency-Key` |
| ItemPrices | `POST /item-prices/{catalogItemId}/withdrawals` | `withdrawPrice` | `200`, state `WITHDRAWN` |
| ItemPrices | `POST /item-prices/{catalogItemId}/sales` | `scheduleSale` | `201`, the sale window with its `saleId` |
| ItemPrices | `POST /item-prices/{catalogItemId}/sales/{saleId}/cancellations` | `cancelSale` | `200`, the item price |
| DeliveryCharges | `POST /delivery-charge-schedules` | `publishChargeSchedule` | `201`, the schedule `CURRENT` |
| DeliveryCharges | `GET /delivery-charge-schedules/current` | `getCurrentChargeSchedule` | `200`, or `404 not-found` before the first publish |
| TaxTables | `POST /tax-tables` | `publishTaxTable` | `201`, the table `CURRENT` |
| TaxTables | `GET /tax-tables/current` | `getCurrentTaxTable` | `200`, or `404 not-found` |
| PriceQuotes | `POST /price-quotes` | `issuePriceQuote` | `201`, the quote `ISSUED` with `expiresAt` |
| PriceQuotes | `GET /price-quotes/{quoteId}` | `getPriceQuote` | `200`, `ISSUED` or `EXPIRED` now |
| Health | `GET /healthz`, `GET /readyz` | none (not in the spec) | `200 {"status":"ok"}` / `200 {"status":"ready"}`; `/readyz` is `503` while draining or while Postgres or Kafka is unreachable. Neither is logged. |
| A2A | `POST /` and `POST /a2a` on `cmd/a2a` | none (not in the spec) | JSON-RPC 2.0 `SendMessage`, see above |

## Conventions in the contract

- **Money** is `{amountMinor, currency}` with integer minor units; the currency is
  always `PRICING_CURRENCY`. Instants are RFC 3339 UTC.
- **`Idempotency-Key`.** Required on all seven `POST` routes (`400
  idempotency-key-required` when absent, `400 idempotency-key-invalid` when not 1
  to 128 printable ASCII characters); `PUT` is idempotent by method and takes none.
  The key is global, not per route. The first response below 500 for a key is
  stored for 24 hours; the same key with the same method, path and body replays
  it with `Idempotency-Replayed: true`; the same key with a different request is
  `409 idempotency-key-reused`. A 5xx is never stored. The key row commits in the
  same transaction as the aggregate change and the outbox rows
  ([ADR 0008](/docs/adr/0008-adopt-the-fleet-rest-api-guideline)).
- **Optimistic concurrency.** Aggregates carry a `version` that grows with every
  change. `PUT .../list-price` takes an optional `version` that must equal the stored
  one when above zero; a lost race is `409 concurrent-update` and is safe to retry.
- **Versioned publications.** A delivery charge schedule or tax table is published
  with a `version` that must exceed the current one (`409 non-increasing-version`);
  the previous one is superseded, never edited.
- **Paging.** `GET /item-prices` takes `limit` and the opaque `cursor` (the
  `nextCursor` of the previous page) and answers `{items, nextCursor}`; no
  `nextCursor` marks the last page. A bad `limit` is `400 invalid-limit`, a bad
  cursor `400 invalid-cursor`, any other query parameter `400
  unknown-query-parameter`. It exists for projection bootstrap and rebuild, not for
  shopper requests.
- **Errors** are RFC 9457 `application/problem+json` with `type`
  `/problems/<slug>`, `title`, `status`, `detail`, `instance`. The slugs, their
  causes and fixes are on
  [Troubleshooting](/docs/operations/troubleshooting#rest-problem-types) and the
  errors per use case on [Use cases](/docs/ddd/use-cases#errors-and-the-problem-types-they-become).
- **Limits.** Request bodies are capped at 64 KiB; a NUL character in the path,
  query or body is `400 malformed-request`; ids are 1 to 64 characters of
  `A-Z a-z 0-9 . _ : -`.
- **Auth:** none. The fleet removed REST, MCP and A2A auth, and
  `TestNoAuthMiddlewareReintroduced` fails CI if auth middleware returns under
  `internal/adapters/inbound`.
