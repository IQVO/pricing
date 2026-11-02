---
id: integration
title: Integration
sidebar_label: Integration
description: "Every upstream and downstream edge of Pricing: protocol, contract, failure behaviour, and the edges that do not exist."
---

# Integration

Pricing has exactly one upstream feed (offer-availability events), publishes to
two topics, serves REST and A2A, and makes **no synchronous call to any other
service**. It consumes the retail network's published events and never calls a
restricted network route. REST and A2A are unauthenticated fleet-wide.

Source: `cmd/api/main.go`, `internal/adapters/inbound/kafka/`,
`internal/adapters/outbound/kafka/`, `apis/asyncapi.yaml`, `apis/openapi.yaml`,
`warehouse-infra` `helm-values/pricing.yaml`. The neighbours below are those of
the aggregator's [context map](https://iqvo.github.io/warehouse-docs/storefront/contexts/pricing/context-map).

## Edges at a glance

| Neighbour | Direction | Protocol | Contract | Pattern |
| --- | --- | --- | --- | --- |
| `offer-availability` (retail network) | upstream of pricing | Kafka `retailnetwork.offer-availability.events` | `CatalogItemCreated`, `CatalogItemDiscontinued` | Conformist |
| `product-discovery` | downstream | Kafka `retailnetwork.pricing.events`; REST `GET /item-prices` (bootstrap, paged) | `ItemPriceChanged`, `ItemPriceWithdrawn` | Conformist (events), Customer/Supplier (query) |
| `shopping-cart` | downstream | Kafka `retailnetwork.pricing.events` | `ItemPriceChanged`, `ItemPriceWithdrawn`, `DeliveryChargeScheduleChanged`, `TaxTableChanged` | Customer/Supplier (cart is the Customer) |
| `checkout` | downstream | REST `POST /price-quotes`, `GET /price-quotes/{quoteId}` | OpenAPI `apis/openapi.yaml` | Customer/Supplier (checkout is the Customer) |
| Storefront merchandiser (human or tool) | downstream | REST `/item-prices`, `/delivery-charge-schedules`, `/tax-tables` | OpenAPI | UI |
| Agent clients | downstream | A2A JSON-RPC on `cmd/a2a` | Agent Card skills `item_price`, `quote_status` | Delegation, read-only |
| Analytics consumers | downstream | Kafka `retailnetwork.pricing.analytics` | `PriceQuoteIssued` | Fire and forget |

## Upstream: offer-availability

- **Channel:** `retailnetwork.offer-availability.events`, consumer group from
  `OFFER_AVAILABILITY_CONSUMER_GROUP`.
- **Types handled:** `com.retailnetwork.commerce.offer-availability.catalogitem.CatalogItemCreated`
  and `...CatalogItemDiscontinued`. Dispatch is on the full `type`; other types
  and non-CloudEvents messages are skipped with a WARN. Only `data.catalogItemId`
  is read, unknown fields are ignored.
- **Effect:** the local `catalog_items` view (needed before a price can be
  created) and, on discontinuation, `WithdrawOnDiscontinue`.
- **Failure behaviour:** at-least-once with an atomic effect (inbox claim on the
  CloudEvents `id` and the effect in one transaction, offset committed after).
  Transient failures retry with a capped backoff, five attempts, then the message
  goes to `retailnetwork.offer-availability.events.dlq`; a payload that can never
  apply is dead-lettered at once. A dead broker makes the consumer return after
  ten consecutive fetch failures and the process exits for a restart. While the
  feed is down new items cannot be priced (`unknown-catalog-item`) and
  discontinuations are not applied; existing prices and quotes keep working.
- **Boundary:** pricing never calls offer-availability; the item master stays
  there ([ADR 0003](/docs/adr/0003-consumed-contracts-and-local-read-model)).

## Downstream: events

All published events are CloudEvents 1.0 in structured mode, written to
`outbox_events` in the same transaction as the aggregate and shipped by the
relay ([Events](/docs/api-reference/events) has the full catalogue):

| Type suffix | Topic | Key |
| --- | --- | --- |
| `itemprice.ItemPriceChanged`, `itemprice.ItemPriceWithdrawn` | `retailnetwork.pricing.events` | `catalogItemId` |
| `deliverychargeschedule.DeliveryChargeScheduleChanged` | `retailnetwork.pricing.events` | `scheduleId` |
| `taxtable.TaxTableChanged` | `retailnetwork.pricing.events` | `taxTableId` |
| `pricequote.PriceQuoteIssued` | `retailnetwork.pricing.analytics` | `quoteId` |

Delivery is at-least-once; the CloudEvents `id` is minted once at encode time and
persisted with the row, so a retry republishes the same `id` and consumers dedupe
on it. Item events carry a monotonic `version`; apply a version only above the
last one applied. With Kafka down the rows accumulate in `outbox_events`; the process keeps serving
(writes still commit) but `/readyz` turns `503`, so Kubernetes stops routing
Service traffic to the pod until a broker answers.

## Downstream: REST

`cmd/api` serves the routes of [API reference](/docs/api-reference). Through the
kind cluster's Kong edge they sit under `/api/pricing` (the path is stripped
before the service). `checkout` calls `POST /price-quotes` when a session opens
and reads the quote back at confirm; `product-discovery` pages `GET /item-prices`
only to bootstrap or rebuild its projection, never on a shopper request, following
`nextCursor` until it is absent. Every `POST` takes an `Idempotency-Key`, so a
retried caller gets the stored answer (`Idempotency-Replayed: true`); a key reused
with another request is `409 idempotency-key-reused`.
Failures are RFC 9457 problems; callers should treat `409 concurrent-update` as
retryable and `422` as a business refusal.

## Downstream: A2A

`cmd/a2a` (off by default, enabled in the kind cluster) answers two read-only
skills over JSON-RPC 2.0 on `/` and `/a2a`, through Kong under
`/api/a2a/pricing`. It carries delegation only: facts between contexts still
travel as CloudEvents ([ADR 0007](/docs/adr/0007-a2a-inbound-adapter)). Domain
rejections come back in the message as problems, never as JSON-RPC errors.

## Edges that do not exist

- **No outbound HTTP, gRPC or A2A call.** `TestNoOutboundA2AClient` and the
  hexagonal rules keep it so; `sync_calls` is empty in the model.
- **No `product-content` edge.** `product-discovery` joins content and price;
  pricing never sees content.
- **No `customer-payments` edge.** Payments learns totals from `checkout`, not
  from pricing.
- **No `delivery-promise` edge.** Pricing prices a tier enum; it never asks for
  a promise, a date or a capacity.
- **No `warehouse.*` topic.** Pricing neither produces nor consumes any topic of
  the warehouse (WMS/WES) area.
- **No MCP surface.** There is no `cmd/mcp`.
- **No authentication** on REST, A2A or Kafka.
- **No `PriceQuoteExpired` event.** Expiry is a state, readable on the quote and
  inferable from `expiresAt`; `checkout` enforces it at confirm.

## Deployment wiring

`warehouse-infra` `helm-values/pricing.yaml` enables the Gateway API route
`/api/pricing` (path stripped) and the A2A route `/api/a2a/pricing`, points the
database at the `pricing-db` Secret (`DATABASE_URL` through PgBouncer,
`MIGRATIONS_DATABASE_URL` direct), sets the consumer group
`pricing-offer-availability` and the currency `USD` (the kind cluster's retail
network is a US one; the binary's own default is `BRL`).
