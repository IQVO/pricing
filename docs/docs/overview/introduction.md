---
id: introduction
title: Introduction
sidebar_label: Introduction
sidebar_position: 1
description: "What the Pricing bounded context owns and does not own, its Core classification in the Storefront area, its capabilities and a map of this site."
---

# Pricing

`pricing` is the **Core** bounded context of the **Storefront** area of the
warehouse-systems fleet (`IQVO/pricing`). It answers what a shopper pays: the
list price and sale windows of each catalogue item, the delivery charge per
service tier, a synthetic tax table, and the binding, immutable, expiring
**price quote** a checkout session is confirmed against. Money is integer minor
units in one configured currency.

:::info What runs on develop
One image, two binaries. `cmd/api` serves the REST API on `:8080` and runs, in
the same process, the transactional outbox relay, the offer-availability
consumer and the clock-driven policy loop. `cmd/a2a` is an optional read-only
A2A agent on `:8093`. Both run on real Postgres only, and `cmd/api` also on
real Kafka only: there is no in-memory mode. `warehouse-infra` deploys the chart
(`helm-values/pricing.yaml`) with the A2A agent enabled and the currency set to
`USD`. There is no MCP binary.
:::

## What the context owns

| Concept | Where it lives | Rule in one line |
| --- | --- | --- |
| **ItemPrice** (one per catalogue item) | `internal/domain/itemprice` | A list price plus sale windows. A sale price is below the list price, windows of one item never overlap (at most 50), the effective price is the lowest active sale price else the list price, and the item must be known and not discontinued in the local catalogue view. `WITHDRAWN` is final. |
| **DeliveryChargeSchedule** | `internal/domain/schedule` | A charge for every tier (`SAME_DAY`, `NEXT_DAY`, `TWO_DAY`, `STANDARD`) with an optional free-over threshold. Exactly one is `CURRENT`; a new version supersedes and must exceed the current one. |
| **TaxTable** | `internal/domain/tax` | Basis-point rates by three-character destination postal-prefix bands (at most 500, never overlapping, 0 to 10000 bp). One `CURRENT`. A synthetic table, not a tax engine. |
| **PriceQuote** | `internal/domain/quote` | A snapshot of lines, shipment groups, delivery charges, tax and total, pinning the price, schedule and tax-table versions. `ISSUED` for 15 minutes, then `EXPIRED`. At most 100 lines, 50 groups, 1000 units per line. |
| **CatalogItemView** | table `catalog_items`, `internal/application/views` | A local read model of which items exist and whether they are discontinued, fed by offer-availability events ([ADR 0003](/docs/adr/0003-consumed-contracts-and-local-read-model)). Not aggregate state. |

## What it does not own

| Out of scope | Where it belongs |
| --- | --- |
| The item master (title, dimensions, flags) | `offer-availability` (retail network): pricing only references `catalogItemId` |
| Descriptions, media, categories | `product-content` |
| Search, browse, purchasability | `product-discovery`, which projects `ItemPriceChanged` and `ItemPriceWithdrawn` |
| The non-binding cart estimate | `shopping-cart`, which follows the price, schedule and tax-table events |
| The purchase saga and quote confirmation | `checkout` |
| Authorize, capture, refund | `customer-payments` |
| Delivery dates, capacity, promises | `delivery-promise` (retail network): pricing prices a tier, it never sees a promise |
| Coupons and promotions | a later phase |
| Real tax calculation | a bought tax engine, later |
| Authentication | none, fleet-wide: REST and A2A are unauthenticated |

## Classification and area

- **Core subdomain.** The price of speed per tier is where the network's
  delivery promise becomes revenue, and cost is the top abandonment reason for
  shoppers. See [Subdomain classification](/docs/ddd/subdomain-classification).
- **Storefront area**, CloudEvents type prefix
  `com.retailnetwork.storefront.pricing.<entity>.<EventName>`. The context
  consumes the retail network's published events and never calls a restricted
  network route; it makes no outbound synchronous call at all.

## Main capabilities

1. Create, change and withdraw item prices (`POST /item-prices`,
   `:set-list-price`, `:withdraw`), with an optimistic `version` guard.
2. Schedule and cancel sale windows (`POST /item-prices/{catalogItemId}/sales`,
   `.../sales/{saleId}:cancel`); the effective price is computed at read time and
   `ItemPriceChanged` is published when a window opens or closes.
3. Publish delivery charge schedules and tax tables as new versions and read the
   current ones.
4. Issue a binding price quote for a frozen cart (`POST /price-quotes`) and read
   it back (`GET /price-quotes/{quoteId}`), `ISSUED` or `EXPIRED` now.
5. Keep the local catalogue view current from `CatalogItemCreated` and
   `CatalogItemDiscontinued`, withdrawing the price of a discontinued item.
6. Publish `ItemPriceChanged`, `ItemPriceWithdrawn`,
   `DeliveryChargeScheduleChanged` and `TaxTableChanged` on
   `retailnetwork.pricing.events`, and `PriceQuoteIssued` on
   `retailnetwork.pricing.analytics`, through the transactional outbox.
7. Answer two read-only A2A skills, `item_price` and `quote_status`
   ([ADR 0007](/docs/adr/0007-a2a-inbound-adapter)).

## Where to go next

| You want to | Read |
| --- | --- |
| See the layers, binaries and data stores | [Architecture](/docs/overview/architecture) |
| Run it and make the first calls | [Quickstart](/docs/overview/quickstart) |
| Know every setting | [Configuration](/docs/operations/configuration) |
| Deploy and operate it | [Runbook](/docs/operations/runbook), [Observability](/docs/operations/observability), [Before the first release](/docs/operations/before-first-release) |
| Fix something that broke | [Troubleshooting](/docs/operations/troubleshooting) |
| Know what tests and CI gates exist | [Testing](/docs/development/testing) |
| See who talks to whom | [Integration](/docs/ecosystem/integration) |
| See the use cases and their invariants | [Use cases](/docs/ddd/use-cases) |
| Read the design as ddd-crew artifacts | [DDD artifacts](/docs/ddd/ddd-artifacts), [Ubiquitous language](/docs/ddd/ubiquitous-language) |
| Read the contracts | [API overview](/docs/api-reference), [Event catalogue](/docs/api-reference/events) |
| Read the decisions | [ADR index](/docs/adr) |

Published at [https://iqvo.github.io/pricing/](https://iqvo.github.io/pricing/).
The cross-context view lives in the aggregator site:
[Storefront](https://iqvo.github.io/warehouse-docs/storefront/).
