---
id: class-diagram
title: Class Diagram
sidebar_label: Class Diagram
description: "UML class diagrams of Pricing's domain model, events and policies, and the hexagonal ports and adapters, as the code has them."
---

# Class Diagram

UML class diagrams of the `internal/domain/**` packages, plus the hexagonal
ports and adapters. Fields are unexported in Go and shown with `-`; methods
are shown with `+`. Exported types per package:

| Package | Exported types |
| --- | --- |
| `internal/domain/itemprice` | `ID`, `State`, `CatalogState`, `Inputs`, `SaleWindow`, `View`, `ItemPrice`, `Snapshot`, `ItemPriceChanged`, `ItemPriceWithdrawn` |
| `internal/domain/schedule` | `ID`, `State`, `TierCharge`, `DeliveryChargeSchedule`, `Snapshot`, `DeliveryChargeScheduleChanged` |
| `internal/domain/tax` | `ID`, `State`, `TaxBand`, `TaxTable`, `Snapshot`, `TaxTableChanged` |
| `internal/domain/quote` | `ID`, `State`, `LineInput`, `GroupInput`, `IssueInput`, `Line`, `Group`, `PriceQuote`, `Snapshot`, `PriceQuoteIssued` |
| `internal/domain/shared` | `Money`, `Tier`, `Event`, `Header`, the shared errors and the id helpers |

Terms are explained on [Ubiquitous language](/docs/ddd/ubiquitous-language) and
the invariants on the [Aggregate Design Canvas](/docs/ddd/aggregate-design-canvas).

## 1. ItemPrice aggregate

```mermaid
classDiagram
    direction TB
    class ItemPrice {
        <<AggregateRoot>>
        -id ID
        -state State
        -list Money
        -sales SaleWindow[]
        -version int64
        -evaluatedAt time.Time
        -published View
        +Create(id, list, Inputs)$ ItemPrice
        +SetListPrice(list Money, Inputs) events
        +ScheduleSale(w SaleWindow, Inputs) events
        +CancelSale(saleID string, now) events
        +WithdrawPrice(now) events
        +Reevaluate(now) events
        +EffectivePrice(at time.Time) Money
        +NextBoundary() time.Time
    }
    class State {
        <<Enumeration>>
        ACTIVE
        WITHDRAWN
    }
    class Money {
        <<ValueObject>>
        +AmountMinor int64
        +Currency string
    }
    class SaleWindow {
        <<ValueObject>>
        +SaleID string
        +Price Money
        +From time.Time
        +To time.Time
    }
    class View {
        <<ValueObject>>
        +ListMinor int64
        +EffectiveMinor int64
        +SaleEndsAt time.Time
    }
    class ItemPriceRepository {
        <<Port>>
        +Get(ctx, id) ItemPrice
        +Save(ctx, p) error
        +List(ctx, after, limit) ItemPrice[]
        +DueForBoundary(ctx, now, limit) ID[]
    }
    ItemPrice --> State : state
    ItemPrice *-- "1" Money : list
    ItemPrice *-- "0..50" SaleWindow : sales
    ItemPrice *-- "1" View : published
    SaleWindow *-- "1" Money : Price
    ItemPriceRepository ..> ItemPrice : persists
```

Source: `internal/domain/itemprice/itemprice.go`, `internal/application/ports/ports.go`.

`EffectivePrice` is derived, never stored: the lowest active sale price, else
the list price. Every command ends in `settle`, which bumps `version` and raises
`ItemPriceChanged` exactly when the published `View` differs from the last one.
`Save` is conditional on the loaded version (`LoadedVersion`, `MarkPersisted`).

## 2. DeliveryChargeSchedule and TaxTable aggregates

```mermaid
classDiagram
    direction TB
    class DeliveryChargeSchedule {
        <<AggregateRoot>>
        -id ID
        -version int64
        -state State
        -tiers TierCharge[]
        -publishedAt time.Time
        +Publish(id, version, tiers, currentVersion, currency, now)$ DeliveryChargeSchedule
        +Supersede() error
        +ChargeFor(tier, goods Money) Money
    }
    class TierCharge {
        <<ValueObject>>
        +Tier Tier
        +Charge Money
        +FreeOver Money
    }
    class Tier {
        <<Enumeration>>
        SAME_DAY
        NEXT_DAY
        TWO_DAY
        STANDARD
    }
    class TaxTable {
        <<AggregateRoot>>
        -id ID
        -version int64
        -state State
        -bands TaxBand[]
        -publishedAt time.Time
        +Publish(id, version, bands, currentVersion, now)$ TaxTable
        +Supersede() error
        +RateFor(prefix string) int
    }
    class TaxBand {
        <<ValueObject>>
        +PostalPrefixFrom string
        +PostalPrefixTo string
        +RateBasisPoints int
    }
    class State {
        <<Enumeration>>
        CURRENT
        SUPERSEDED
    }
    DeliveryChargeSchedule --> State
    DeliveryChargeSchedule *-- "4" TierCharge : tiers
    TierCharge --> Tier
    TaxTable --> State
    TaxTable *-- "1..500" TaxBand : bands
```

Source: `internal/domain/schedule/schedule.go`, `internal/domain/tax/tax.go`.

`Tier` is the network's delivery-promise vocabulary, re-declared locally in
`internal/domain/shared`: there is no shared kernel. Both aggregates share the
`CURRENT`/`SUPERSEDED` lifecycle: publishing a new version supersedes the
previous one in the same transaction (`PublishChargeSchedule`,
`PublishTaxTable`). `RateFor` returns 0 for a prefix no band covers.

## 3. PriceQuote aggregate and events

```mermaid
classDiagram
    direction TB
    class PriceQuote {
        <<AggregateRoot>>
        -id ID
        -cartID string
        -state State
        -currency string
        -lines Line[]
        -groups Group[]
        -tax int64
        -total int64
        -scheduleVersion int64
        -taxVersion int64
        -issuedAt time.Time
        -expiresAt time.Time
        -version int64
        +Issue(IssueInput, schedule, table)$ PriceQuote
        +Expire(now) error
        +ExpiredAt(at time.Time) bool
    }
    class State {
        <<Enumeration>>
        ISSUED
        EXPIRED
    }
    class Line {
        <<ValueObject>>
        +LineNo int
        +CatalogItemID string
        +Quantity int
        +UnitPrice Money
        +LineTotal Money
        +PriceVersion int64
    }
    class Group {
        <<ValueObject>>
        +GroupNo int
        +Tier Tier
        +DeliveryCharge Money
    }
    PriceQuote --> State
    PriceQuote *-- "1..100" Line : lines
    PriceQuote *-- "1..50" Group : groups
```

```mermaid
classDiagram
    direction TB
    class Event {
        <<Interface>>
        +EventName() string
        +Entity() string
        +SubjectID() string
        +AggregateVersion() int64
        +OccurredAt() time.Time
    }
    class ItemPriceChanged {
        <<DomainEvent>>
        +CatalogItemID
        +Currency
        +ListPriceMinor
        +EffectivePriceMinor
        +SaleEndsAt
    }
    class ItemPriceWithdrawn {
        <<DomainEvent>>
        +CatalogItemID
    }
    class DeliveryChargeScheduleChanged {
        <<DomainEvent>>
        +ScheduleID
        +Tiers
    }
    class TaxTableChanged {
        <<DomainEvent>>
        +TaxTableID
        +Bands
    }
    class PriceQuoteIssued {
        <<DomainEvent>>
        +QuoteID
        +CartID
        +Currency
        +TotalMinor
        +ExpiresAt
    }
    Event <|.. ItemPriceChanged
    Event <|.. ItemPriceWithdrawn
    Event <|.. DeliveryChargeScheduleChanged
    Event <|.. TaxTableChanged
    Event <|.. PriceQuoteIssued
```

Source: `internal/domain/quote/quote.go`, `internal/domain/itemprice/events.go`,
`internal/domain/shared/shared.go`.

The three policies are use cases, not domain types: `PublishBoundaries`
(`PublishEffectivePriceOnWindowBoundary`) and `ExpireQuotes` (`QuoteTtl`) run
in the policy loop, `Consumer.CatalogItemDiscontinued` is `WithdrawOnDiscontinue`.
Issuing a quote publishes `PriceQuoteIssued`; expiry publishes nothing.

## 4. Hexagonal ports and adapters

```mermaid
flowchart TB
    subgraph IN["internal/adapters/inbound"]
        HTTP["http<br/>merchandiser writes and reads,<br/>checkout quote routes,<br/>paged bootstrap read"]
        KC["kafka<br/>consumer: offer-availability"]
        A2A["a2a<br/>skills item_price, quote_status"]
    end
    LOOP["cmd/api policy loop<br/>publish-boundaries, expire-quotes"]
    APP["<b>internal/application/usecases</b><br/>Create/SetListPrice, ScheduleSale, CancelSale<br/>WithdrawPrice, PublishChargeSchedule<br/>PublishTaxTable, IssuePriceQuote<br/>PublishBoundaries, ExpireQuotes<br/>Consumer, queries"]
    DOM["<b>internal/domain</b><br/>itemprice.ItemPrice<br/>schedule.DeliveryChargeSchedule<br/>tax.TaxTable<br/>quote.PriceQuote"]
    PORTS["<b>internal/application/ports</b><br/>ItemPriceRepository<br/>ScheduleRepository<br/>TaxTableRepository<br/>QuoteRepository<br/>CatalogView, Inbox<br/>EventOutbox, UnitOfWork<br/>IdempotencyStore, Clock<br/>IDGenerator, QuoteMetrics"]
    subgraph OUT["internal/adapters/outbound"]
        A1["postgres<br/>repositories, outbox, inbox,<br/>catalog view,<br/>idempotency store, UoW"]
        A2["outbox + kafka<br/>relay, encoder, writer"]
        A3["telemetry, clock, ids"]
    end
    HTTP --> APP
    KC --> APP
    A2A --> APP
    LOOP --> APP
    APP --> DOM
    APP --> PORTS
    PORTS --- A1
    PORTS --- A3
    A1 -. outbox rows .-> A2
```

Source: `cmd/api/main.go`, `cmd/api/policies.go`, `cmd/a2a/main.go`,
`internal/application/ports/ports.go`, `internal/architecture/architecture_test.go`.

There is no outbound REST client in this context. The read routes
(`GetItemPrice`, `ListItemPrices`, `GetCurrentChargeSchedule`,
`GetCurrentTaxTable`, `GetPriceQuote`) are query use cases over the same ports.
The A2A adapter holds `GetItemPrice` and `GetPriceQuote` only. The in-memory
adapters under `internal/adapters/outbound/memory` are test doubles that
`cmd/**` may not import.
