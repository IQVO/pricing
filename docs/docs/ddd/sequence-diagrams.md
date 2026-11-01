---
id: sequence-diagrams
title: Sequence Diagrams
sidebar_label: Sequence Diagrams
description: "UML sequence diagrams for every Pricing command use case, from inbound adapter through aggregate, repository and outbox to Kafka, with error, concurrency and idempotency branches."
---

# Sequence Diagrams

One UML sequence diagram per command use case on
[Use Cases](/docs/ddd/use-cases). Participant names are the
packages and ports of the code, from the
[Class Diagram](/docs/ddd/class-diagram):

- `http` and `kafka` are the inbound adapters; `scheduler` is the policy loop of `cmd/api` (`cmd/api/policies.go`).
- The use case is in `internal/application/usecases`.
- `Postgres` stands for the repositories, the outbox, the inbox and the
  idempotency store. They share one transaction through `UnitOfWork`.
- `Relay / Kafka` is the outbox relay. It reads committed `outbox_events`
  rows and publishes them to `retailnetwork.pricing.events` (or
  `retailnetwork.pricing.analytics`).

Every save is conditional on the loaded `version` (optimistic concurrency).
Full CloudEvents types are on [Domain Events](/docs/ddd/domain-events).

## 1. CreateItemPrice and SetListPrice (REST POST /item-prices and :set-list-price)

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 120, "actorMargin": 24, "messageMargin": 28, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant H as http
    participant UC as SetListPrice
    participant CV as CatalogView
    participant A as ItemPrice
    participant DB as Postgres
    participant K as Relay / Kafka
    H->>DB: find Idempotency-Key
    alt key seen with the same body
        DB-->>H: stored response
        H-->>H: replay stored response
    else new key
        H->>UC: Execute(catalogItemId, listPrice)
        UC->>CV: known and not discontinued?
        alt unknown or discontinued
            CV-->>UC: miss
            UC-->>H: 422 unknown-catalog-item<br/>ErrUnknownCatalogItem
        else known
            UC->>A: Create or SetListPrice(price, inputs)
            alt ErrNegativeAmount or ErrCurrencyMismatch
                A-->>UC: error
                UC-->>H: 422 problem
            else valid
                A-->>UC: ACTIVE + ItemPriceChanged
                UC->>DB: BEGIN, insert or update where version matches
                UC->>DB: insert outbox ItemPriceChanged
                UC->>DB: store key response, COMMIT
                UC-->>H: 201 version
                K->>DB: poll unpublished outbox rows
                K->>K: publish key catalogItemId
                K->>DB: mark published_at
            end
        end
    end
```

Source: `internal/application/usecases/itemprice.go` (`CreateItemPrice`, `SetListPrice`), `internal/adapters/inbound/http/{handlers,idempotency}.go`.

Derived from: `model.yaml → contexts[pricing].aggregates[ItemPrice]`
(`SetListPrice`, invariants), `.read_models[CatalogItemView]`,
`.publishes[ItemPriceChanged]`; `conventions.integration_rules`.

Omitted: the version-conflict retry on update (as in diagram 2). On a
create there is no conflict: the `catalogItemId` is the key and a second
insert is a unique violation mapped to 409 `concurrent-update`.

## 2. ScheduleSale (REST POST /item-prices/catalogItemId/sales)

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 120, "actorMargin": 24, "messageMargin": 28, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant H as http
    participant UC as ScheduleSale
    participant A as ItemPrice
    participant DB as Postgres
    participant K as Relay / Kafka
    H->>DB: find Idempotency-Key
    alt key seen
        DB-->>H: stored response, replay
    else new key
        H->>UC: Execute(catalogItemId, price, from, to)
        UC->>DB: load ItemPrice and version
        alt not found
            UC-->>H: 404 not-found
        else found
            UC->>A: ScheduleSale(price, from, to)
            alt ErrSaleNotBelowList or ErrOverlappingSaleWindow
                A-->>UC: error
                UC-->>H: 422 sale-not-below-list<br/>or 409 overlapping-sale-window
            else added
                A-->>UC: window stored
                UC->>DB: BEGIN, update where version matches
                alt version changed
                    DB-->>UC: 0 rows, ROLLBACK
                    UC->>UC: reload and retry once, then 409 concurrent-update
                else saved
                    opt effective price moved (window already open)
                        UC->>DB: insert outbox ItemPriceChanged
                    end
                    UC->>DB: store key response, COMMIT
                    UC-->>H: 201 saleId
                    K->>DB: poll outbox
                    K->>K: publish key catalogItemId
                end
            end
        end
    end
```

Source: `internal/application/usecases/itemprice.go` (`ScheduleSale`), `internal/domain/itemprice/itemprice.go`.

Derived from: `model.yaml → contexts[pricing].aggregates[ItemPrice]`
(`ScheduleSale`, invariants), `.publishes[ItemPriceChanged]`.

Omitted: `ErrNegativeAmount` and `ErrCurrencyMismatch` (422, same branch as
the sale rules). A future window emits nothing now; the boundary policy
publishes when it opens (diagram 8).

## 3. CancelSale (REST POST .../sales/saleId:cancel)

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 120, "actorMargin": 24, "messageMargin": 28, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant H as http
    participant UC as CancelSale
    participant A as ItemPrice
    participant DB as Postgres
    H->>DB: find Idempotency-Key
    alt key seen
        DB-->>H: stored response, replay
    else new key
        H->>UC: Execute(catalogItemId, saleId)
        UC->>DB: load ItemPrice and version
        alt price or window not found
            UC-->>H: 404 not-found
        else price WITHDRAWN
            UC-->>H: 409 illegal-transition
        else found
            UC->>A: CancelSale(saleId)
            A-->>UC: window removed
            UC->>DB: BEGIN, update where version matches
            opt the cancelled window was active
                UC->>DB: insert outbox ItemPriceChanged<br/>effective price back at list
            end
            UC->>DB: store key response, COMMIT
            UC-->>H: 200
        end
    end
    Note over UC,DB: no distinct CancelSale event in the model<br/>ItemPriceChanged carries the fact
```

Source: `internal/application/usecases/itemprice.go` (`CancelSale`).

Derived from: `model.yaml → contexts[pricing].aggregates[ItemPrice].commands`,
`.publishes`. The event choice is this pack's reading (open question on the
[canvas](/docs/ddd/bounded-context-canvas)).

Omitted: the version-conflict retry (as in diagram 2) and the relay.

## 4. WithdrawPrice (merchandiser, REST)

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 120, "actorMargin": 24, "messageMargin": 28, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant H as http
    participant UC as WithdrawPrice
    participant A as ItemPrice
    participant DB as Postgres
    participant K as Relay / Kafka
    H->>DB: find Idempotency-Key
    alt key seen
        DB-->>H: stored response, replay
    else new key
        H->>UC: Execute(catalogItemId)
        UC->>DB: load ItemPrice and version
        alt not found
            UC-->>H: 404 not-found
        else already WITHDRAWN
            UC-->>H: 409 illegal-transition
        else ACTIVE
            UC->>A: WithdrawPrice(now)
            A-->>UC: WITHDRAWN + ItemPriceWithdrawn
            UC->>DB: BEGIN, update where version matches
            UC->>DB: insert outbox ItemPriceWithdrawn
            UC->>DB: store key response, COMMIT
            UC-->>H: 200 WITHDRAWN
            K->>DB: poll outbox
            K->>K: publish key catalogItemId
        end
    end
```

Source: `internal/application/usecases/itemprice.go` (`WithdrawPrice`).

Derived from: `model.yaml → contexts[pricing].aggregates[ItemPrice]`
(`WithdrawPrice`, transitions), `.publishes[ItemPriceWithdrawn]`.

Omitted: the HTTP adapter detail. The route is
`POST /item-prices/{catalogItemId}:withdraw`.

## 5. WithdrawPrice by policy WithdrawOnDiscontinue (Kafka)

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 120, "actorMargin": 24, "messageMargin": 28, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant KC as kafka consumer
    participant UC as Consumer.CatalogItemDiscontinued
    participant A as ItemPrice
    participant DB as Postgres
    participant K as Relay / Kafka
    KC->>DB: processed_events has CloudEvents id?
    alt duplicate
        KC->>KC: ack, no effect
    else new CatalogItemDiscontinued
        KC->>UC: CatalogItemDiscontinued(eventId, catalogItemId)
        UC->>DB: BEGIN, claim the event id, mark discontinued in catalog_items
        UC->>DB: load ItemPrice
        alt no price or already WITHDRAWN
            UC->>DB: insert inbox row, COMMIT
        else ACTIVE
            UC->>A: WithdrawPrice(now)
            A-->>UC: WITHDRAWN + ItemPriceWithdrawn
            UC->>DB: update where version matches
            UC->>DB: insert outbox ItemPriceWithdrawn, inbox row, COMMIT
            K->>DB: poll outbox
            K->>K: publish key catalogItemId
        end
    end
```

Source: `internal/application/usecases/consume.go` (`CatalogItemDiscontinued`), `internal/adapters/inbound/kafka/consumer.go`.

Derived from: `model.yaml → contexts[pricing].policies[WithdrawOnDiscontinue]`,
`.consumes` (`CatalogItemDiscontinued`), `.read_models`.

Omitted: the version-conflict retry (redeliver). View update, aggregate
save, outbox and inbox share one transaction, so a crash anywhere replays
the whole event safely.

## 6. PublishChargeSchedule and PublishTaxTable (REST)

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 120, "actorMargin": 24, "messageMargin": 28, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant H as http
    participant UC as PublishChargeSchedule<br/>or PublishTaxTable
    participant A as Schedule / TaxTable
    participant DB as Postgres
    participant K as Relay / Kafka
    H->>DB: find Idempotency-Key
    alt key seen
        DB-->>H: stored response, replay
    else new key
        H->>UC: Execute(version, tiers or bands)
        UC->>DB: load current version
        UC->>A: Publish(version, entries)
        alt rule violated
            A-->>UC: ErrIncompleteChargeSchedule / ErrOverlappingTaxBand<br/>/ ErrRateOutOfRange
            UC-->>H: 422 problem
        else ErrNonIncreasingVersion
            A-->>UC: error
            UC-->>H: 409 non-increasing-version
        else valid
            A-->>UC: new CURRENT + Changed event
            UC->>DB: BEGIN, insert new, supersede previous
            UC->>DB: insert outbox Changed event
            UC->>DB: store key response, COMMIT
            UC-->>H: 201 id, version
            K->>DB: poll outbox
            K->>K: publish key scheduleId or taxTableId
        end
    end
```

Source: `internal/application/usecases/publish.go` (`PublishChargeSchedule`, `PublishTaxTable`).

Derived from: `model.yaml → contexts[pricing].aggregates[DeliveryChargeSchedule]`,
`.aggregates[TaxTable]` (commands, invariants, transitions), `.publishes`.

One diagram covers both aggregates: the shape is identical, only the rules
differ. Omitted: nothing else; the model gives both aggregates the same
lifecycle.

## 7. IssuePriceQuote (REST POST /price-quotes)

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 120, "actorMargin": 24, "messageMargin": 28, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant H as http
    participant UC as IssuePriceQuote
    participant P as ItemPrice store
    participant S as Schedule store
    participant T as TaxTable store
    participant A as PriceQuote
    participant DB as Postgres
    participant K as Relay / Kafka
    H->>DB: find Idempotency-Key
    alt key seen with the same body
        DB-->>H: stored response, replay 201
    else new key
        H->>UC: Execute(cartId, lines, groups, destinationPrefix)
        UC->>P: effective price per line
        alt a line not ACTIVE
            P-->>UC: miss
            UC-->>H: 422 item-not-priced<br/>ErrItemNotPriced
        else priced
            UC->>S: current schedule, charge per group tier
            UC->>T: current table, band of the prefix
            UC->>A: quote.Issue(input, schedule, table)
            alt ErrEmptyQuote or ErrCurrencyMismatch
                A-->>UC: error
                UC-->>H: 422 problem
            else issued
                A-->>UC: ISSUED + PriceQuoteIssued<br/>pins versions, expiresAt
                UC->>DB: BEGIN, insert quote
                UC->>DB: insert outbox PriceQuoteIssued (analytics topic)
                UC->>DB: store key response, COMMIT
                UC-->>H: 201 quoteId, totalMinor, expiresAt
                K->>DB: poll outbox
                K->>K: publish key quoteId
            end
        end
    end
```

Source: `internal/application/usecases/quote.go` (`IssuePriceQuote`), `internal/domain/quote/quote.go`.

Derived from: `model.yaml → contexts[pricing].aggregates[PriceQuote]`
(`IssuePriceQuote`, invariants, description), `.policies[QuoteTtl]`,
`.publishes[PriceQuoteIssued]`; `contexts[checkout].sync_calls`.

Omitted: the request's field list, which the model gives (`cartId`, `lines`,
`groups`, `destinationPostalPrefix`). There is no version conflict on an insert: the `quoteId` is minted
here. The reads of prices, schedule and table are store reads through
ports, not aggregate method calls; the quote pins their versions so a later
change never rewrites it.

## 8. ExpireQuote and the sale-boundary policy (scheduler)

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 120, "actorMargin": 24, "messageMargin": 28, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant SCH as scheduler
    participant UC as ExpireQuote /<br/>Boundary publication
    participant DB as Postgres
    participant K as Relay / Kafka
    SCH->>DB: scan ISSUED quotes past expiresAt
    loop each expired quote
        UC->>DB: update status EXPIRED where still ISSUED
        Note over UC,DB: no outbox row:<br/>the model publishes nothing on expiry
    end
    SCH->>DB: scan sale windows at a from/to boundary
    loop each item at a boundary
        UC->>DB: recompute effective price
        opt price moved, not published for this boundary
            UC->>DB: insert outbox ItemPriceChanged, COMMIT
            K->>DB: poll outbox
            K->>K: publish key catalogItemId
        end
    end
```

Source: `cmd/api/policies.go`, `internal/application/usecases/{consume,quote}.go` (`PublishBoundaries`, `ExpireQuotes`).

Derived from: `model.yaml → contexts[pricing].policies[QuoteTtl]`,
`.policies[PublishEffectivePriceOnWindowBoundary]`,
`.aggregates[PriceQuote].transitions`.

Omitted: the boundary bookkeeping. `item_prices.evaluated_at`, the `published_*`
columns and the indexed `next_boundary_at` find the due items and make the
publication idempotent per boundary: `Reevaluate` raises an event only when the
published view moved. Expiry needs no marker: the state flip is itself
idempotent.

## 9. Project CatalogItemView (Kafka)

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 120, "actorMargin": 24, "messageMargin": 28, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant KC as kafka consumer
    participant UC as ProjectCatalogItemView
    participant DB as Postgres
    KC->>DB: processed_events has CloudEvents id?
    alt duplicate
        KC->>KC: ack, no effect
    else new CatalogItemCreated or CatalogItemDiscontinued
        KC->>UC: Execute(catalogItemId, kind)
        UC->>DB: BEGIN, upsert catalog_items, insert inbox row, COMMIT
        opt CatalogItemDiscontinued
            UC->>UC: hand off to WithdrawOnDiscontinue (diagram 5)
        end
    end
    Note over UC,DB: no event, no outbox row
```

Source: `internal/application/usecases/consume.go` (`CatalogItemCreated`).

Derived from: `model.yaml → contexts[pricing].consumes`, `.read_models`.

Omitted: nothing. The view holds id and the discontinued flag only; per-item
order comes from the producer keying both types by `catalogItemId`.
