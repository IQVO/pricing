---
id: aggregate-design-canvas
title: Aggregate Design Canvas
sidebar_label: Aggregate Design Canvas
description: "The ddd-crew Aggregate Design Canvas for Pricing's four aggregates, ItemPrice, DeliveryChargeSchedule, TaxTable and PriceQuote, and its one read model."
---

# Aggregate Design Canvas

Following the ddd-crew [Aggregate Design Canvas v1.1](https://github.com/ddd-crew/aggregate-design-canvas).
The model has four aggregate roots in this context, **ItemPrice**,
**DeliveryChargeSchedule**, **TaxTable** and **PriceQuote**, and one read
model, **CatalogItemView**. Go packages: `internal/domain/itemprice`, `internal/domain/quote`, `internal/domain/schedule`, `internal/domain/tax` (plus `shared`).

## ItemPrice

### 1. Name

`ItemPrice` (`itemprice.ItemPrice`), identified by `catalogItemId`.

### 2. Description

List price and sale windows of one catalogue item, versioned. The effective
price is the lowest active sale price, else the list price. Value objects:

| Value object | Fields | Note |
| --- | --- | --- |
| `Money` | `amountMinor`, `currency` | Integer minor units of the one configured currency. |
| `SaleWindow` | `saleId`, `price`, `from`, `to` | A time-bounded sale price; windows of one item never overlap. |

### 3. State Transitions

```mermaid
stateDiagram-v2
    [*] --> ACTIVE: SetListPrice
    ACTIVE --> ACTIVE: ScheduleSale / CancelSale
    ACTIVE --> WITHDRAWN: WithdrawPrice
    WITHDRAWN --> [*]
    note right of WITHDRAWN
        WithdrawOnDiscontinue runs WithdrawPrice
        on CatalogItemDiscontinued
    end note
```

Source: `internal/domain/itemprice/itemprice.go` (states and transitions).

Derived from: `model.yaml → contexts[pricing].aggregates[ItemPrice].states`,
`.transitions`, `.commands`, `.policies`.

The entry arrow `[*] --> ACTIVE` is the creating command `SetListPrice`;
the model lists transitions between states only, so the creation and the
self-transition for sale management are this page's reading. The model
names no transition out of `WITHDRAWN`: a withdrawn price is not revived;
selling the item again needs a new price decision (open question on the
[canvas](/docs/ddd/bounded-context-canvas)).

### 4. Enforced Invariants

| Invariant | Enforced by |
| --- | --- |
| Every amount is non-negative | all commands → `ErrNegativeAmount` |
| All prices of an item use the configured currency | all commands → `ErrCurrencyMismatch` |
| A sale price is below the list price | `ScheduleSale` → `ErrSaleNotBelowList` |
| Sale windows of one item never overlap | `ScheduleSale` → `ErrOverlappingSaleWindow` |
| The item exists in the local `CatalogItemView` and is not `DISCONTINUED` | `SetListPrice`, `ScheduleSale` → `ErrUnknownCatalogItem` |
| A `WITHDRAWN` price takes no further command, and is never revived | every command → `ErrIllegalTransition` (409 `illegal-transition`) |
| At most 50 live sale windows | `ScheduleSale` → `ErrValidation` (422 `validation-failed`) |

Derived from: `model.yaml → contexts[pricing].aggregates[ItemPrice].invariants`.

### 5. Corrective Policies

| Policy | Owner | Rule | Effect on this aggregate |
| --- | --- | --- | --- |
| `PublishEffectivePriceOnWindowBoundary` | this context | Emit `ItemPriceChanged` when a sale starts or ends, idempotent per item and boundary. | No state change; republishes the effective price as the windows dictate. |
| `WithdrawOnDiscontinue` | this context | On `CatalogItemDiscontinued` withdraw the item's price. | `ACTIVE → WITHDRAWN`, `ItemPriceWithdrawn`. |
| Idempotent consumption | this context | Every consumed event is applied once (inbox on the CloudEvents `id`). | A repeated `CatalogItemDiscontinued` is acknowledged without effect. |

### 6. Handled Commands

| Command | Trigger | Transition |
| --- | --- | --- |
| `SetListPrice` | Merchandiser, REST `POST /item-prices` / `POST /item-prices/{catalogItemId}:set-list-price` | `[*] → ACTIVE`, then list-price updates in `ACTIVE` |
| `ScheduleSale` | Merchandiser, REST `POST /item-prices/{catalogItemId}/sales` | none (adds a window) |
| `CancelSale` | Merchandiser, REST `POST /item-prices/{catalogItemId}/sales/{saleId}:cancel` | none (removes a window); raises `ItemPriceChanged` when the effective price reverts |
| `WithdrawPrice` | Merchandiser, REST `POST /item-prices/{catalogItemId}:withdraw`, or `WithdrawOnDiscontinue` on Kafka `CatalogItemDiscontinued` | `ACTIVE → WITHDRAWN`; on a `WITHDRAWN` price `ErrIllegalTransition` |

### 7. Created Events

| Event | Raised when | Full CloudEvents type |
| --- | --- | --- |
| `ItemPriceChanged` | `SetListPrice` (create or update), `ScheduleSale` of an open window and `CancelSale` of the live sale are saved, and on a sale window boundary (`PublishEffectivePriceOnWindowBoundary`), each time only when the published view (list price, effective price, sale end) moved | `com.retailnetwork.storefront.pricing.itemprice.ItemPriceChanged` |
| `ItemPriceWithdrawn` | `WithdrawPrice` is saved | `com.retailnetwork.storefront.pricing.itemprice.ItemPriceWithdrawn` |

`CancelSale` raises `ItemPriceChanged` (the effective price reverts to the
list price) and has no event of its own, by decision: a cancelled sale
changes the effective price, the window-boundary policy already emits that
as `ItemPriceChanged` (idempotent per item and boundary), and a separate
event would duplicate the fact. Both
events leave through the transactional outbox in the same transaction as
the save.

### 8. Throughput

*Estimate.* The model says "low: merchandising writes; bursts on sale
boundaries". One row per catalogue item, so contention on a single
`catalogItemId` is negligible; the burst is the boundary scheduler
republishing many items at once when a large sale starts or ends.

### 9. Size

*Estimate.* The model says "one row per ACTIVE catalogue item; ~10^6". A
root row with the list price, a handful of sale windows, and the `version`
every event carries.

## DeliveryChargeSchedule

### 1. Name

`DeliveryChargeSchedule` (`schedule.DeliveryChargeSchedule`), identified by
`scheduleId`.

### 2. Description

The delivery charge per service tier (`SAME_DAY`, `NEXT_DAY`, `TWO_DAY`,
`STANDARD`), with an optional free-over threshold per tier. Exactly one
schedule is current; versions strictly increase.

| Value object | Fields | Note |
| --- | --- | --- |
| `TierCharge` | `tier`, `charge`, `freeOver?` | `charge` is a `Money`; `freeOver` is the optional free-delivery threshold. |

### 3. State Transitions

```mermaid
stateDiagram-v2
    [*] --> CURRENT: PublishChargeSchedule
    CURRENT --> SUPERSEDED: PublishChargeSchedule
    SUPERSEDED --> [*]
    note right of SUPERSEDED
        a new version supersedes, never edits;
        the read route serves the CURRENT one
    end note
```

Source: `internal/domain/schedule/schedule.go` (`Publish`, `Supersede`).

Derived from: `model.yaml → contexts[pricing].aggregates[DeliveryChargeSchedule].states`,
`.transitions`, `.notes`.

Publishing a new schedule creates it `CURRENT` and moves the previous
current one to `SUPERSEDED` in the same transaction; the read route
`GET /delivery-charge-schedules/current` serves the `CURRENT` one.

### 4. Enforced Invariants

| Invariant | Enforced by |
| --- | --- |
| A schedule names a charge for every tier | `PublishChargeSchedule` → `ErrIncompleteChargeSchedule` |
| A new schedule's version strictly increases | `PublishChargeSchedule` → `ErrNonIncreasingVersion` |

### 5. Corrective Policies

None in the model. A wrong schedule is corrected by publishing a higher
version, never by editing.

### 6. Handled Commands

| Command | Trigger | Transition |
| --- | --- | --- |
| `PublishChargeSchedule` | Merchandiser, REST `POST /delivery-charge-schedules` | `[*] → CURRENT`; previous `CURRENT → SUPERSEDED` |

### 7. Created Events

| Event | Raised when | Full CloudEvents type |
| --- | --- | --- |
| `DeliveryChargeScheduleChanged` | `PublishChargeSchedule` is saved | `com.retailnetwork.storefront.pricing.deliverychargeschedule.DeliveryChargeScheduleChanged` |

### 8. Throughput

*Estimate.* The model says "rare".

### 9. Size

*Estimate.* The model says "one current + history": one row per published
version, plus its four tier rows.

## TaxTable

### 1. Name

`TaxTable` (`tax.TaxTable`), identified by `taxTableId`.

### 2. Description

A synthetic tax table (D10): basis-point rates by destination postal-prefix
band. Exactly one table is current; versions strictly increase.

| Value object | Fields | Note |
| --- | --- | --- |
| `TaxBand` | `postalPrefixFrom`, `postalPrefixTo`, `rateBasisPoints` | Bands never overlap; the rate is within the configured range. |

### 3. State Transitions

```mermaid
stateDiagram-v2
    [*] --> CURRENT: PublishTaxTable
    CURRENT --> SUPERSEDED: PublishTaxTable
    SUPERSEDED --> [*]
    note right of SUPERSEDED
        follows DeliveryChargeSchedule;
        GET /tax-tables/current serves CURRENT
    end note
```

Source: `internal/domain/tax/tax.go` (`Publish`, `Supersede`).

Derived from: `model.yaml → contexts[pricing].aggregates[TaxTable].states`,
`.transitions`, `.notes`.

### 4. Enforced Invariants

| Invariant | Enforced by |
| --- | --- |
| Bands never overlap | `PublishTaxTable` → `ErrOverlappingTaxBand` |
| A rate is within the configured range | `PublishTaxTable` → `ErrRateOutOfRange` |

### 5. Corrective Policies

None in the model; supersede with a higher version.

### 6. Handled Commands

| Command | Trigger | Transition |
| --- | --- | --- |
| `PublishTaxTable` | Merchandiser, REST `POST /tax-tables` | `[*] → CURRENT`; previous `CURRENT → SUPERSEDED` |

### 7. Created Events

| Event | Raised when | Full CloudEvents type |
| --- | --- | --- |
| `TaxTableChanged` | `PublishTaxTable` is saved | `com.retailnetwork.storefront.pricing.taxtable.TaxTableChanged` |

### 8. Throughput

*Estimate.* The model says "rare".

### 9. Size

*Estimate.* The model says "one current + history": one row per version,
plus its bands.

## PriceQuote

### 1. Name

`PriceQuote` (`quote.PriceQuote`), identified by `quoteId`.

### 2. Description

A binding, immutable, expiring price snapshot for one checkout: lines with
unit and line totals, groups with tier and delivery charge, tax, total, the
versions of price, schedule and tax table used, `issuedAt` and `expiresAt`.

| Value object | Fields | Note |
| --- | --- | --- |
| `QuoteLine` | `catalogItemId`, `quantity`, `unitPrice`, `lineTotal` | `unitPrice` is the item's effective price at issue. |
| `QuoteGroup` | `groupNo`, `tier`, `deliveryCharge` | The price of the tier the shopper selected for that shipment group. |

### 3. State Transitions

```mermaid
stateDiagram-v2
    [*] --> ISSUED: IssuePriceQuote
    ISSUED --> EXPIRED: ExpireQuote
    EXPIRED --> [*]
    note right of ISSUED
        immutable: no transition changes
        a quote's content, only its expiry
    end note
```

Source: `internal/domain/quote/quote.go` (`Issue`, `Expire`).

Derived from: `model.yaml → contexts[pricing].aggregates[PriceQuote].states`,
`.transitions`, `.commands`.

### 4. Enforced Invariants

| Invariant | Enforced by |
| --- | --- |
| Every line names an `ACTIVE` price | `IssuePriceQuote` → `ErrItemNotPriced` |
| A quote has at least one line | `IssuePriceQuote` → `ErrEmptyQuote` |
| All amounts use the configured currency | `IssuePriceQuote` → `ErrCurrencyMismatch` |
| A `CURRENT` charge schedule and tax table exist | `IssuePriceQuote` → `ErrNoChargeSchedule`, `ErrNoTaxTable` |
| At most 100 lines, 50 groups, 1000 units per line; every line in exactly one group | `IssuePriceQuote` → `ErrValidation` |

### 5. Corrective Policies

| Policy | Owner | Rule | Effect on this aggregate |
| --- | --- | --- | --- |
| `QuoteTtl` | this context | A PriceQuote expires 15 minutes (default) after issue; expiry is by time, immutable otherwise. | `ISSUED → EXPIRED` through `ExpireQuote`. |
| `ExpireOnQuoteExpiry` | `checkout` | An `OPEN` or `DECLINED` session expires with its price quote. | None here; quote expiry is checkout-internal TTL state, so checkout keeps its own clock and pricing publishes no expiry event. |

### 6. Handled Commands

| Command | Trigger | Transition |
| --- | --- | --- |
| `IssuePriceQuote` | `checkout`, REST `POST /price-quotes` | `[*] → ISSUED` |
| `ExpireQuote` | the clock (`QuoteTtl`), a scheduler | `ISSUED → EXPIRED` |

### 7. Created Events

| Event | Raised when | Full CloudEvents type |
| --- | --- | --- |
| `PriceQuoteIssued` | `IssuePriceQuote` is saved | `com.retailnetwork.storefront.pricing.pricequote.PriceQuoteIssued` |

`PriceQuoteIssued` is analytics-only (topic
`retailnetwork.pricing.analytics`). `ExpireQuote` publishes nothing, by decision (no
`PriceQuoteExpired` exists): expiry is checkout-internal TTL state, not a
business fact anyone consumes. It is observable from `expiresAt`, and
`checkout` enforces it at confirm (`ErrPriceQuoteExpired`).

### 8. Throughput

*Estimate.* The model says "one per checkout session opened". Reads outnumber
writes: `checkout` reads the quote back at confirm.

### 9. Size

*Estimate.* The model says "TTL minutes; retained for the order's lifetime
via the checkout session reference". Lines and groups are a handful per
quote; the rows outlive the TTL because the placed order's totals must stay
explainable.

## Read models

| Read model | Source events | Serves | Why it is not an aggregate |
| --- | --- | --- | --- |
| `CatalogItemView` | `offer-availability` `CatalogItemCreated`, `CatalogItemDiscontinued` | `ErrUnknownCatalogItem` enforcement inside `SetListPrice` / `ScheduleSale` | It protects no invariant of its own and takes no command; it is a projection of facts `offer-availability` owns, rebuildable by replaying the topic. |

Derived from: `model.yaml → contexts[pricing].read_models`. The view keeps
exactly what the invariant needs: that the item exists and whether it is
discontinued. The consumed events carry only `catalogItemId` in the fields
this context declares, so the view stores id and state, not the item's
attributes.
