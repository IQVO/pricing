---
id: bounded-context-canvas
title: Bounded Context Canvas
sidebar_label: Bounded Context Canvas
description: "The ddd-crew Bounded Context Canvas v5 for Pricing, with purpose, classification, roles, every inbound and outbound message, business decisions, assumptions, metrics and open questions."
---

# Bounded Context Canvas

Following the ddd-crew [Bounded Context Canvas v5](https://github.com/ddd-crew/bounded-context-canvas).
Every message row comes from `contexts[pricing]` (`inbound`, `consumes`,
`publishes`, `rest`) or from a neighbour's entry that names this context.
`sync_calls` is empty: pricing calls nobody.

## Canvas at a glance

Two pictures of the sections below, laid out like the ddd-crew canvas:
first the inbound collaborators feeding this context (purpose, domain roles
and the business decisions it enforces), then the outbound collaborators it
notifies. Arrows follow the message type: **solid blue** commands (`cmd`),
**dotted green** queries (`qry`), **thick orange** events (`evt`); each
arrow names its channel and the messages it carries. Box shapes mark the
collaborator kind: rounded = people, UI or any client; blue = sibling
bounded context. Channel names on the arrows are abbreviated; the Inbound
and Outbound Communication tables carry the exact route, topic and
CloudEvents type for every message.

### Inbound

```mermaid
%%{init: {"flowchart": {"nodeSpacing": 14, "rankSpacing": 40, "padding": 6}}}%%
flowchart LR
    classDef bc fill:#fff3bf,stroke:#e67700,stroke-width:1.5px,color:#000
    classDef card fill:#fff9db,stroke:#f08c00,color:#000,text-align:left
    classDef ctx fill:#e7f5ff,stroke:#1c7ed6,color:#000
    classDef actor fill:#fff4e6,stroke:#e8590c,color:#000
    classDef net fill:#ffe3e3,stroke:#c92a2a,color:#000
    subgraph IN["Inbound - who commands, queries or feeds it"]
        direction TB
        in0(["Storefront merchandiser<br/>(human)"]):::actor
        in2["checkout"]:::ctx
        in3["product-discovery"]:::ctx
        in4["offer-availability<br/>(network)"]:::net
    end
    subgraph BC["pricing - Core subdomain - storefront tier"]
        direction TB
        card["<b>Purpose</b><br/>Own what a shopper pays: list<br/>prices and sale windows, the<br/>delivery charge per tier, a<br/>synthetic tax table, and the<br/>binding, expiring PriceQuote.<br/><br/><b>Domain roles</b><br/>- Specification (price of an item,<br/>charge of a tier)<br/>- Execution (price quotes)<br/><br/><b>Business decisions it enforces</b><br/>- Sale below list; sale windows<br/>of one item never overlap<br/>- One current charge schedule and<br/>tax table; versions supersede<br/>- A PriceQuote is binding,<br/>immutable and expiring<br/>- Item master stays in<br/>offer-availability"]:::card
    end
    in0 -->|"cmd - REST<br/>SetListPrice, ScheduleSale,<br/>CancelSale, WithdrawPrice,<br/>PublishChargeSchedule,<br/>PublishTaxTable"| BC
    in0 -.->|"qry - REST<br/>GetItemPrice,<br/>GetCurrentChargeSchedule,<br/>GetCurrentTaxTable"| BC
    in2 -->|"cmd - REST<br/>IssuePriceQuote"| BC
    in2 -.->|"qry - REST<br/>GetPriceQuote"| BC
    in3 -.->|"qry - REST paged,<br/>bootstrap only<br/>ListItemPrices"| BC
    in4 ==>|"evt - Kafka<br/>retailnetwork.offer-<br/>availability.events<br/>CatalogItemCreated,<br/>CatalogItemDiscontinued"| BC
    linkStyle 0 stroke:#1971c2,stroke-width:1.5px
    linkStyle 1 stroke:#2f9e44,stroke-width:1.5px
    linkStyle 2 stroke:#1971c2,stroke-width:1.5px
    linkStyle 3 stroke:#2f9e44,stroke-width:1.5px
    linkStyle 4 stroke:#2f9e44,stroke-width:1.5px
    linkStyle 5 stroke:#e8590c,stroke-width:2.5px
    class BC bc
```

Source: `internal/adapters/inbound/{http,kafka,a2a}`, `apis/openapi.yaml` (inbound collaborators and messages).

### Outbound

```mermaid
%%{init: {"flowchart": {"nodeSpacing": 14, "rankSpacing": 40, "padding": 6}}}%%
flowchart LR
    classDef bc fill:#fff3bf,stroke:#e67700,stroke-width:1.5px,color:#000
    classDef ctx fill:#e7f5ff,stroke:#1c7ed6,color:#000
    classDef actor fill:#fff4e6,stroke:#e8590c,color:#000
    BC["<b>pricing - Core subdomain - storefront tier</b>"]:::bc
    subgraph OUT["Outbound - whom it notifies"]
        direction TB
        out0["product-discovery"]:::ctx
        out1["shopping-cart"]:::ctx
        out2(["analytics consumers"]):::actor
    end
    BC ==>|"evt - Kafka<br/>retailnetwork.pricing.events<br/>ItemPriceChanged,<br/>ItemPriceWithdrawn"| out0
    BC ==>|"evt - Kafka<br/>retailnetwork.pricing.events<br/>ItemPriceChanged, ItemPriceWithdrawn,<br/>DeliveryChargeScheduleChanged,<br/>TaxTableChanged"| out1
    BC ==>|"evt - Kafka<br/>retailnetwork.pricing.analytics<br/>PriceQuoteIssued"| out2
    linkStyle 0 stroke:#e8590c,stroke-width:2.5px
    linkStyle 1 stroke:#e8590c,stroke-width:2.5px
    linkStyle 2 stroke:#e8590c,stroke-width:2.5px
```

Source: `internal/adapters/outbound/kafka/{encoder,encoder_data}.go`, `apis/asyncapi.yaml` (outbound topics and types).

Derived from: `model.yaml → contexts[pricing].inbound`, `.publishes`,
`.purpose`, `.roles`, `.business_decisions`;
`contexts[product-discovery].inbound`, `contexts[shopping-cart].inbound`
(rows from `pricing`).

The diagrams omit assumptions, verification metrics, open questions and the
ubiquitous language (sections below), the relationship pattern per message
(see the tables), and the synchronous replies. Pricing has no outbound
synchronous call: `sync_calls` is empty.

## Name

**Pricing** (`pricing`, code `PR`, CloudEvents tier `storefront`).

## Purpose

To own what a shopper pays. The context:

- keeps the list price and sale windows of every catalogue item, versioned,
  with the effective price derived (lowest active sale, else list);
- prices each delivery service tier (`SAME_DAY`, `NEXT_DAY`, `TWO_DAY`,
  `STANDARD`) with an optional free-over threshold;
- holds the synthetic tax table by postal-prefix band;
- issues the binding, immutable, expiring `PriceQuote` a checkout session
  is confirmed against, pinning the price, schedule and tax-table versions
  it was computed from.

## Strategic Classification

| Axis | Verdict |
| --- | --- |
| **Domain** | **Core** subdomain. See the [Core Domain Chart](/docs/ddd/core-domain-chart). |
| **Business Model** | Revenue (the price of speed per tier is where the network's promise becomes revenue). |
| **Evolution** | Custom-built. |

## Domain Roles

| Role | Applies? | Evidence in the model |
| --- | --- | --- |
| **Specification (price of an item, charge of a tier)** | **Yes, primary** | `ItemPrice`, `DeliveryChargeSchedule`, `TaxTable` with their invariants; the read routes every other surface renders from. |
| **Execution (price quotes)** | **Yes** | `IssuePriceQuote`, `ExpireQuote`, the `PriceQuote` aggregate and `QuoteTtl`. |
| Analysis | No | No read model here is a decision input for another context; `CatalogItemView` is an enforcement input. |
| Gateway | No | The storefront edge is `storefront-bff`, the experience layer; pricing is one of the contexts behind it, and shoppers never read it directly (they see prices through `product-discovery`). |

## Inbound Communication

| Collaborator | Message | Type | Channel | Relationship |
| --- | --- | --- | --- | --- |
| Storefront merchandiser (human) | `SetListPrice` | Command | REST `POST /item-prices`, `PUT /item-prices/{catalogItemId}/list-price` | UI |
| Storefront merchandiser (human) | `ScheduleSale`, `CancelSale` | Command | REST `POST /item-prices/{catalogItemId}/sales`, `POST /item-prices/{catalogItemId}/sales/{saleId}/cancellations` | UI |
| Storefront merchandiser (human) | `WithdrawPrice` | Command | REST `POST /item-prices/{catalogItemId}/withdrawals` | UI |
| Storefront merchandiser (human) | `PublishChargeSchedule` | Command | REST `POST /delivery-charge-schedules` | UI |
| Storefront merchandiser (human) | `PublishTaxTable` | Command | REST `POST /tax-tables` | UI |
| Storefront merchandiser (human) | `GetItemPrice` | Query | REST `GET /item-prices/{catalogItemId}` | UI |
| Storefront merchandiser (human) | `GetCurrentChargeSchedule` | Query | REST `GET /delivery-charge-schedules/current` | UI |
| Storefront merchandiser (human) | `GetCurrentTaxTable` | Query | REST `GET /tax-tables/current` | UI |
| `checkout` | `IssuePriceQuote` | Command | REST `POST /price-quotes` | Customer/Supplier (we are the Supplier) |
| `checkout` | `GetPriceQuote` | Query | REST `GET /price-quotes/{quoteId}` | Customer/Supplier (we are the Supplier) |
| `product-discovery` | `ListItemPrices` | Query | REST `GET /item-prices` (bootstrap and rebuild only, paged) | Customer/Supplier (we are the Supplier) |
| `offer-availability` (network) | `CatalogItemCreated` | Event | Kafka `retailnetwork.offer-availability.events`, `com.retailnetwork.commerce.offer-availability.catalogitem.CatalogItemCreated` | Conformist |
| `offer-availability` (network) | `CatalogItemDiscontinued` | Event | Kafka `retailnetwork.offer-availability.events`, `com.retailnetwork.commerce.offer-availability.catalogitem.CatalogItemDiscontinued` | Conformist |
| Agent clients | `item_price`, `quote_status` skills | Query | A2A JSON-RPC 2.0 on `cmd/a2a` (`/`, `/a2a`), read-only | Delegation |

Route-to-command note: the model's `inbound` row for the merchandiser names
the route groups, not one route per command; the mapping above is this
pack's reading of `rest` against `commands` (see Open Questions).

## Outbound Communication

| Collaborator | Message | Type | Channel | Relationship |
| --- | --- | --- | --- | --- |
| `product-discovery`, `shopping-cart` | `ItemPriceChanged` | Event | Kafka `retailnetwork.pricing.events`, `com.retailnetwork.storefront.pricing.itemprice.ItemPriceChanged` | Conformist (product-discovery) / Customer/Supplier (shopping-cart) |
| `product-discovery`, `shopping-cart` | `ItemPriceWithdrawn` | Event | Kafka `retailnetwork.pricing.events`, `com.retailnetwork.storefront.pricing.itemprice.ItemPriceWithdrawn` | Conformist (product-discovery) / Customer/Supplier (shopping-cart) |
| `shopping-cart` | `DeliveryChargeScheduleChanged` | Event | Kafka `retailnetwork.pricing.events`, `com.retailnetwork.storefront.pricing.deliverychargeschedule.DeliveryChargeScheduleChanged` | Customer/Supplier (we are the Supplier) |
| `shopping-cart` | `TaxTableChanged` | Event | Kafka `retailnetwork.pricing.events`, `com.retailnetwork.storefront.pricing.taxtable.TaxTableChanged` | Customer/Supplier (we are the Supplier) |
| analytics only | `PriceQuoteIssued` | Event | Kafka `retailnetwork.pricing.analytics`, `com.retailnetwork.storefront.pricing.pricequote.PriceQuoteIssued` | analytics stream |

What the consumers do with them (from their own model entries):
`product-discovery` refreshes the price fields of `ProductView` (monotonic
guard on the price `version`) and its `purchasable` flag;
`shopping-cart` refreshes `PurchasabilityView` and recomputes the labelled,
non-binding `EstimateView`. Relationship evidence:
[Context Map](/docs/ddd/context-map).

## Ubiquitous Language

Full glossary: [Ubiquitous Language](/docs/ddd/ubiquitous-language).
Top terms: **List Price**, **Sale Window**, **Effective Price**,
**Delivery Charge**, **Tax Band**, **Price Quote** (binding; not the cart
**Estimate**).

## Business Decisions

1. **A sale price is always below the list price** (`ErrSaleNotBelowList`)
   **and sale windows of one item never overlap**
   (`ErrOverlappingSaleWindow`). The effective price needs no tie-break.
2. **Exactly one charge schedule and one tax table are current; a new
   version supersedes, never edits** (`ErrNonIncreasingVersion`). A quote
   can always name the versions it used.
3. **A PriceQuote is binding, immutable and expiring** (`QuoteTtl`, default
   15 minutes). The review page shows the binding total; a changed total is
   visible before confirm (gap S2).
4. **The item master stays in offer-availability.** A price exists only for
   a known, not discontinued item (`ErrUnknownCatalogItem`), enforced
   against the local `CatalogItemView`.
5. **Money is integer minor units of one configured currency**; every
   amount is non-negative (`ErrNegativeAmount`) and single-currency
   (`ErrCurrencyMismatch`).
6. **A discontinued item's price is withdrawn** (`WithdrawOnDiscontinue`),
   so no surface keeps selling what the network retired.

## Assumptions

- `offer-availability` publishes `CatalogItemCreated` before pricing can be
  asked to price the item; an earlier `SetListPrice` fails
  `ErrUnknownCatalogItem` and the merchandiser retries.
- The merchandiser's UI and the consumers tolerate the outbox lag between a
  price decision and `ItemPriceChanged`; sale boundaries are published by
  `PublishEffectivePriceOnWindowBoundary`, idempotent per item and boundary.
- `checkout` opens the quote, shows it and confirms within `QuoteTtl`; its
  own `ExpireOnQuoteExpiry` policy mirrors the TTL, because pricing
  publishes no expiry event (see Open Questions).
- Every consumed event is applied once (inbox on the CloudEvents `id`).
- One configured currency everywhere (`conventions.integration_rules`).

## Verification Metrics

| Metric | Target (proposed) | Source |
| --- | --- | --- |
| `POST /price-quotes` success rate | tracked; alert on a drop | `pricing.quotes.issued` by `outcome`; Kong 5xx ratio alerts in warehouse-infra |
| Share of `IssuePriceQuote` rejected `ErrItemNotPriced` | about 0 once checkout checks purchasability | `pricing.quotes.issued` with `reason` = `item-not-priced` |
| Quotes confirmed after `expiresAt` (checkout's `ErrPriceQuoteExpired`) | tracked; drives the `QuoteTtl` value | `checkout` metrics |
| Sale-boundary `ItemPriceChanged` emitted at the boundary minute | 100% (idempotent per item and boundary) | scheduler log vs outbox |
| `CatalogItemView` lag behind `retailnetwork.offer-availability.events` | below 30 s | broker consumer-group lag (the service exports no lag metric) |
| Outbox age of the oldest unpublished pricing event | below 5 s | `outbox_events.created_at` |
| Prices on items the view has discontinued but not yet withdrawn | 0 after `WithdrawOnDiscontinue` lag | join of `CatalogItemView` and `ItemPrice` |

## Open Questions

- **Resolved in the model-fix:** `WithdrawPrice` has the route
  `POST /item-prices/{catalogItemId}/withdrawals`; `CancelSale` raises no event
  of its own (the window-boundary policy emits `ItemPriceChanged` for the
  reverted price); quote expiry is checkout-internal TTL state with no
  `PriceQuoteExpired` event; the schedule and the tax table have the states
  `CURRENT` then `SUPERSEDED`; `IssuePriceQuote` lists its request fields
  (`cartId`, `lines`, `groups`, `destinationPostalPrefix`); `GET /item-prices`
  is bootstrap and rebuild only, and shoppers see prices through
  `product-discovery`'s `ProductView`.
