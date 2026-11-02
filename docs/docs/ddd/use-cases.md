---
id: use-cases
title: Use cases
sidebar_label: Use cases
description: "Every application use case of Pricing: trigger, inputs, invariants, errors and events raised."
---

# Use cases

The use cases are the structs in `internal/application/usecases`: one per REST
operation, one per consumed event, one per clock-driven policy, and the
read-only queries (`deps.go`, `itemprice.go`, `publish.go`, `quote.go`,
`queries.go`, `consume.go`). Every command runs in **one unit of work**: load
the aggregate, call the domain, save it, append the raised events to the
outbox, return a view. A version conflict on save re-runs the attempt (twice
for commands, five times for consumed events) before it surfaces as
`concurrent-update`.

Source: `internal/application/usecases/*.go`,
`internal/adapters/inbound/http/{server,handlers,errors}.go`,
`internal/adapters/inbound/kafka/offer_availability.go`, `cmd/api/policies.go`.

## At a glance

| Use case | Trigger | Adapter | Emits |
| --- | --- | --- | --- |
| `CreateItemPrice` | Merchandiser | `POST /item-prices` | `ItemPriceChanged` |
| `SetListPrice` | Merchandiser | `PUT /item-prices/{catalogItemId}/list-price` | `ItemPriceChanged` when the published view moved |
| `ScheduleSale` | Merchandiser | `POST /item-prices/{catalogItemId}/sales` | `ItemPriceChanged` only if the window is already open |
| `CancelSale` | Merchandiser | `POST /item-prices/{catalogItemId}/sales/{saleId}/cancellations` | `ItemPriceChanged` when the live sale was cancelled |
| `WithdrawPrice` | Merchandiser | `POST /item-prices/{catalogItemId}/withdrawals` | `ItemPriceWithdrawn` |
| `PublishChargeSchedule` | Merchandiser | `POST /delivery-charge-schedules` | `DeliveryChargeScheduleChanged` |
| `PublishTaxTable` | Merchandiser | `POST /tax-tables` | `TaxTableChanged` |
| `IssuePriceQuote` | `checkout` | `POST /price-quotes` | `PriceQuoteIssued` (analytics topic) |
| `Consumer.CatalogItemCreated` | offer-availability event | Kafka consumer | none |
| `Consumer.CatalogItemDiscontinued` (policy `WithdrawOnDiscontinue`) | offer-availability event | Kafka consumer | `ItemPriceWithdrawn` when the price was `ACTIVE` |
| `PublishBoundaries` (policy `PublishEffectivePriceOnWindowBoundary`) | Clock, every `POLICY_INTERVAL` | policy loop in `cmd/api` | `ItemPriceChanged` |
| `ExpireQuotes` (policy `QuoteTtl`) | Clock, every `POLICY_INTERVAL` | policy loop in `cmd/api` | none |
| `GetItemPrice`, `ListItemPrices`, `GetCurrentChargeSchedule`, `GetCurrentTaxTable`, `GetPriceQuote` | Callers | `GET` routes; `GetItemPrice` and `GetPriceQuote` also the A2A skills `item_price`, `quote_status` | none |

All `POST` routes require an `Idempotency-Key` header (1 to 128 printable ASCII
characters; the key is global; the first response below 500 is stored 24 hours in
the same transaction as the change, `internal/adapters/inbound/http/idempotency.go`).
`PUT` is idempotent by method and needs none.

## CreateItemPrice

- **Input:** `catalogItemId`, `listPrice` (`amountMinor`, `currency`).
- **Invariants:** the id is valid (1 to 64 characters of `A-Z a-z 0-9 . _ : -`);
  the item is `ACTIVE` in `catalog_items` (`unknown-catalog-item` otherwise);
  the amount is not negative; the currency is `PRICING_CURRENCY`. A second
  create for the same item is `concurrent-update` (409).
- **Result:** `201` with the item price (`state` `ACTIVE`, `version` 1).
- **Events:** `ItemPriceChanged` with the list and effective price.

## SetListPrice

- **Input:** `listPrice`, optional `version` (above 0 it must equal the stored
  version, else `concurrent-update`).
- **Invariants:** the price is `ACTIVE` (a `WITHDRAWN` price is
  `illegal-transition`, never revived); the item is still `ACTIVE` in the
  catalogue view; no existing sale price is at or above the new list price
  (`sale-not-below-list`). An active sale keeps winning over the new list price.
- **Events:** `ItemPriceChanged` only when the list price, the effective price
  or the sale end differs from the last published view.

## ScheduleSale

- **Input:** `price`, `from`, `to`; the server mints the `saleId`.
- **Invariants:** price `ACTIVE`; item `ACTIVE` in the view; sale price below
  the list price (`sale-not-below-list`); `to` after `from` and in the future;
  at most 50 live windows; the window overlaps no live window of the item
  (`overlapping-sale-window`, 409). From is inclusive, to exclusive.
- **Result:** `201` with the sale window.
- **Events:** `ItemPriceChanged` if the window is already open; a future window
  raises nothing now, the boundary policy publishes it when it opens.

## CancelSale

- **Input:** the item id and `saleId`.
- **Invariants:** price `ACTIVE`; the sale exists (`not-found` otherwise).
- **Events:** cancelling the live sale reverts the effective price to the list
  price and raises `ItemPriceChanged` (the model has no event of its own for
  it); cancelling a future window raises nothing.

## WithdrawPrice

- **Invariants:** price `ACTIVE`. Withdrawing a withdrawn price is
  `illegal-transition` (409), not a no-op.
- **Events:** `ItemPriceWithdrawn`.

## PublishChargeSchedule

- **Input:** `version`, `tiers[]` (`tier`, `charge`, optional `freeOver`).
- **Invariants:** a charge for every tier of `SAME_DAY`, `NEXT_DAY`, `TWO_DAY`,
  `STANDARD` (`incomplete-charge-schedule`); amounts in the configured
  currency; `version` above the current one (`non-increasing-version`, 409).
- **Effect:** in one transaction the previous `CURRENT` schedule becomes
  `SUPERSEDED` and the new one `CURRENT`; a second `CURRENT` row is refused by a
  partial unique index (`concurrent-update`).
- **Events:** `DeliveryChargeScheduleChanged`.

## PublishTaxTable

- **Input:** `version`, `bands[]` (`postalPrefixFrom`, `postalPrefixTo`,
  `rateBasisPoints`).
- **Invariants:** at most 500 bands; prefixes are three upper-case letters or
  digits and `from <= to`; bands never overlap (`overlapping-tax-band`); rate 0
  to 10000 bp (`rate-out-of-range`); `version` above the current one
  (`non-increasing-version`).
- **Effect and events:** as for the schedule, raising `TaxTableChanged`.

## IssuePriceQuote

- **Trigger:** `checkout` when a session opens. **Input:** `cartId`, `lines[]`
  (`catalogItemId`, `quantity`), `groups[]` (`groupNo`, `offerId`, `tier`,
  `lineNos`), `destinationPostalPrefix`.
- **Invariants:** at least one line (`empty-quote`), at most 100 lines and 50
  groups, quantity 1 to 1000, a catalogue item on one line only; every line's
  item has an `ACTIVE` price (`item-not-priced`); every line belongs to exactly
  one group; group numbers unique; the tier is charged from the `CURRENT`
  schedule, free when the group's goods reach the tier's free-over threshold;
  a `CURRENT` schedule and tax table exist (`no-charge-schedule`,
  `no-tax-table`); amounts in the configured currency.
- **Arithmetic:** line total = unit price times quantity; tax = the destination
  band's rate applied to goods plus delivery, rounded half up to a minor unit
  (a prefix with no band is rate 0); total = goods + delivery + tax. The quote
  pins the price version of each line and the schedule and tax-table versions.
- **Effect:** state `ISSUED`, `expiresAt` = issue time + 15 minutes
  (`DefaultQuoteTTL`; not configurable by an environment variable). Every
  attempt increments `pricing.quotes.issued` with `outcome` and, when rejected,
  the problem slug as `reason`.
- **Result:** `201` with the quote. **Events:** `PriceQuoteIssued` on the
  analytics topic.

## Consumer: CatalogItemCreated and CatalogItemDiscontinued

- **Trigger:** `retailnetwork.offer-availability.events`, types
  `com.retailnetwork.commerce.offer-availability.catalogitem.CatalogItemCreated`
  and `...CatalogItemDiscontinued`; only `data.catalogItemId` is read.
- **Flow (one unit of work):** claim the CloudEvents `id` in `processed_events`
  (a duplicate is acknowledged and does nothing); then
  - `CatalogItemCreated`: mark the item `ACTIVE`; it never revives a
    `DISCONTINUED` item, so a replayed creation cannot undo a discontinuation.
  - `CatalogItemDiscontinued`: mark it `DISCONTINUED` (creating the entry when
    the item was never seen); if the item has an `ACTIVE` price, withdraw it and
    append `ItemPriceWithdrawn`.
- **Errors:** a `catalogItemId` that is not a valid id is `ErrInvalidEvent` and is
  dead-lettered at once; any other error is transient and retried.

## Policies

- **`PublishBoundaries`:** per pass, up to 200 `ACTIVE` item prices whose next
  sale boundary is at or before now, earliest first; each is re-evaluated
  (`Reevaluate`) and `ItemPriceChanged` is raised only when the published view
  moved, so re-running a boundary raises nothing. A missing item, a concurrent
  update or an illegal transition is logged (`boundary publication skipped`) and
  skipped.
- **`ExpireQuotes`:** per pass, up to 200 `ISSUED` quotes with `expiresAt` at or
  before now move to `EXPIRED`; nothing is published. Reads do not wait for it:
  a quote past `expiresAt` reads `EXPIRED` at once.

## Queries

`GetItemPrice` returns state, list price, the effective price **now**, the sale
windows and the version. `ListItemPrices` pages by `catalogItemId` (`limit`
1 to 500, default 100, the opaque `cursor` is the keyset after the last id of the
page, answered as `nextCursor`) and is meant
for projection bootstrap and rebuild. The charge schedule and tax table queries
return the `CURRENT` one or `not-found`. `GetPriceQuote` returns the quote with
its state computed at read time.

## Errors and the problem types they become

`internal/adapters/inbound/http/errors.go` maps each typed error to a status and
a `/problems/<slug>` type; the full table with causes and fixes is on
[Troubleshooting](/docs/operations/troubleshooting#rest-problem-types). In short:
`not-found` 404; `overlapping-sale-window`, `non-increasing-version`,
`concurrent-update`, `illegal-transition`, `idempotency-key-reused` 409;
`unknown-catalog-item`,
`sale-not-below-list`, `incomplete-charge-schedule`, `overlapping-tax-band`,
`rate-out-of-range`, `item-not-priced`, `empty-quote`, `no-charge-schedule`,
`no-tax-table`, `negative-amount`, `currency-mismatch`, `validation-failed` 422;
`malformed-request`, `idempotency-key-required`, `idempotency-key-invalid`,
`invalid-limit`, `invalid-cursor`, `unknown-query-parameter`
400; anything else `internal-error` 500.
