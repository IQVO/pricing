---
id: domain-message-flow
title: Domain Message Flow
sidebar_label: Domain Message Flow
description: "Four Pricing scenarios as ddd-crew domain message flows, covering price management, a sale window boundary, issuing a checkout price quote, and discontinuation."
---

# Domain Message Flow

Following ddd-crew [Domain Message Flow Modelling](https://github.com/ddd-crew/domain-message-flow-modelling).
Four scenarios. Participants are contexts and actors. Every arrow is a
message from the model, prefixed `cmd:` (command), `qry:` (query) or `evt:`
(event). A `Note` names a command a context runs on itself. Kafka is drawn
as one participant. Event names are written `entity.EventName`; the full
type is `com.retailnetwork.<tier>.<producer>.<entity>.<EventName>` (see
[Domain Events](/docs/ddd/domain-events)). REST path
parameters are written without braces. The storefront-wide flows follow from
the [Context Map](https://iqvo.github.io/warehouse-docs/storefront/strategic-design/context-map).

## 1. Merchandiser prices an item and schedules a sale

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 120, "actorMargin": 24, "messageMargin": 28, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    actor M as Merchandiser
    participant PR as pricing
    participant K as Kafka
    participant PD as product-discovery
    participant SC as shopping-cart
    M->>PR: cmd: SetListPrice<br/>POST /item-prices
    Note over PR: ErrUnknownCatalogItem<br/>against CatalogItemView
    PR->>K: evt: itemprice.<br/>ItemPriceChanged
    PR-->>M: cmd: reply 201<br/>version
    M->>PR: cmd: ScheduleSale<br/>POST /item-prices/catalogItemId/sales
    Note over PR: ErrSaleNotBelowList,<br/>ErrOverlappingSaleWindow
    PR->>K: evt: itemprice.<br/>ItemPriceChanged
    PR-->>M: cmd: reply 201 saleId
    K->>PD: evt: itemprice.<br/>ItemPriceChanged
    Note over PD: ProductView price fields,<br/>guard on version
    K->>SC: evt: itemprice.<br/>ItemPriceChanged
    Note over SC: PurchasabilityView,<br/>EstimateView
```

Source: `internal/application/usecases/itemprice.go`, `internal/adapters/inbound/http/handlers.go`.

Derived from: `model.yaml → contexts[pricing].inbound` (merchandiser row),
`.aggregates[ItemPrice].commands` + `.invariants`,
`.publishes[ItemPriceChanged]`; `contexts[product-discovery].read_models`;
`contexts[shopping-cart].read_models`.

Omitted: the outbox write and relay (see
[Sequence Diagrams](/docs/ddd/sequence-diagrams)); the
`CancelSale` variant, which republishes `ItemPriceChanged` with the
effective price back at list (this pack's reading; the model names no
distinct event).

## 2. A sale window starts: the boundary policy republishes the price

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 120, "actorMargin": 24, "messageMargin": 28, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant T as Clock / scheduler
    participant PR as pricing
    participant K as Kafka
    participant PD as product-discovery
    participant SC as shopping-cart
    T->>PR: sale window from-time reached
    Note over PR: PublishEffectivePriceOnWindowBoundary,<br/>idempotent per item and boundary
    PR->>K: evt: itemprice.ItemPriceChanged<br/>effectivePriceMinor, saleEndsAt
    K->>PD: evt: itemprice.<br/>ItemPriceChanged
    K->>SC: evt: itemprice.<br/>ItemPriceChanged
    T->>PR: sale window to-time reached
    PR->>K: evt: itemprice.ItemPriceChanged<br/>effective price back at list
    K->>PD: evt: itemprice.<br/>ItemPriceChanged
    K->>SC: evt: itemprice.<br/>ItemPriceChanged
```

Source: `cmd/api/policies.go`, `internal/application/usecases/consume.go` (`PublishBoundaries`).

Derived from: `model.yaml → contexts[pricing].policies[PublishEffectivePriceOnWindowBoundary]`,
`.aggregates[ItemPrice].value_objects` (`SaleWindow`), `.publishes`.

Omitted: the batch scan that finds the windows reaching a boundary, and the
burst behaviour the throughput estimate warns about ("bursts on sale
boundaries"). No aggregate state changes at a boundary: the windows are
already stored; only the published effective price moves.

## 3. Checkout issues a binding price quote

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 120, "actorMargin": 24, "messageMargin": 28, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant CK as checkout
    participant PR as pricing
    participant K as Kafka analytics
    CK->>PR: cmd: IssuePriceQuote<br/>POST /price-quotes cartId, lines, groups
    Note over PR: effective price per line,<br/>charge per selected tier,<br/>tax band by destination prefix
    alt a line has no ACTIVE price
        PR-->>CK: cmd: reply 422<br/>ErrItemNotPriced
    else priced
        Note over PR: quote ISSUED, pins price,<br/>schedule and tax versions,<br/>issuedAt, expiresAt (QuoteTtl)
        PR->>K: evt: pricequote.<br/>PriceQuoteIssued
        PR-->>CK: cmd: reply 201 quoteId,<br/>totalMinor, expiresAt
    end
    CK->>PR: qry: GetPriceQuote<br/>GET /price-quotes/quoteId
    PR-->>CK: qry: reply quote,<br/>or EXPIRED after QuoteTtl
```

Source: `internal/application/usecases/quote.go`, `internal/domain/quote/quote.go`.

Derived from: `model.yaml → contexts[pricing].inbound` (checkout rows),
`.aggregates[PriceQuote]` (`commands`, `invariants`, `description`),
`.policies[QuoteTtl]`, `.publishes[PriceQuoteIssued]`;
`contexts[checkout].sync_calls` (`IssuePriceQuote`), `.invariants`
(`ErrPriceQuoteExpired`).

Omitted: the request's field list (`cartId`, `lines`, `groups`,
`destinationPostalPrefix`); `ExpireQuote`
runs on the clock and publishes nothing, so `checkout` enforces the TTL at
confirm with `ErrPriceQuoteExpired` and its own `ExpireOnQuoteExpiry`
policy. A changed total between cart and confirm is visible before confirm
— gap S2's answer.

## 4. The network discontinues an item

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 120, "actorMargin": 24, "messageMargin": 28, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant OA as offer-availability
    participant K as Kafka
    participant PR as pricing
    participant PD as product-discovery
    participant SC as shopping-cart
    OA->>K: evt: catalogitem.<br/>CatalogItemDiscontinued
    K->>PR: evt: catalogitem.<br/>CatalogItemDiscontinued
    Note over PR: WithdrawOnDiscontinue:<br/>WithdrawPrice
    alt the item has an ACTIVE price
        PR->>K: evt: itemprice.<br/>ItemPriceWithdrawn
        K->>PD: evt: itemprice.<br/>ItemPriceWithdrawn
        Note over PD: no longer purchasable
        K->>SC: evt: itemprice.<br/>ItemPriceWithdrawn
        Note over SC: PurchasabilityView
    else never priced
        Note over PR: CatalogItemView only,<br/>no event
    end
```

Source: `internal/adapters/inbound/kafka/offer_availability.go`, `internal/application/usecases/consume.go`.

Derived from: `model.yaml → contexts[pricing].consumes`,
`.policies[WithdrawOnDiscontinue]`, `.publishes[ItemPriceWithdrawn]`,
`.read_models[CatalogItemView]`; `network/model/model.yaml →
contexts[offer-availability].publishes[CatalogItemDiscontinued]`.

Omitted: `CatalogItemCreated`, which feeds only the view; the inbox; and
`product-discovery`'s independent consumption of the same
`CatalogItemDiscontinued` (its own conformist edge to the network). Pricing
learns of discontinuation from the network, not from a sibling storefront
context.
