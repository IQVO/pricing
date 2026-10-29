# ADR 0002: Assumptions where the model is silent or ambiguous

## Status

Accepted (2026-10-25).

## Context

The frozen storefront model and the orchestrator's model-gap decisions leave small
points open. Each is resolved here with the least surprising reading; code comments
cite them as `ADR 0002 A<n>`.

## Decision

- **A1 One currency.** The configured currency is `PRICING_CURRENCY` (ISO 4217, three
  upper-case letters), default `BRL`. Every amount of every request must use it
  (`currency-mismatch`, 422).
- **A2 CatalogItemView.** The local view only records ACTIVE or DISCONTINUED per
  catalogItemId. An item the view never saw is unknown (`unknown-catalog-item`, 422).
  DISCONTINUED is terminal: a late or replayed `CatalogItemCreated` never revives it.
- **A3 Withdrawn is final.** WITHDRAWN is terminal. A withdrawn price accepts no
  list-price change or sale and cannot be quoted (`illegal-transition`, 409). A new
  price for the item needs a new catalogue item.
- **A4 One event per published change.** `ItemPriceChanged` is raised exactly when the
  published view (list price, effective price, sale end) differs from the one last
  published, by every command and by the boundary policy, so a replayed boundary
  raises nothing. CancelSale of a live sale therefore raises `ItemPriceChanged`
  (model-gap decision 2); there is no PriceQuoteExpired event (decision 3).
- **A5 Optimistic concurrency.** A save expects stored version == loaded version;
  a mismatch is `concurrent-update` (409). A command is retried once on conflict.
  `SetListPrice` takes an optional `version` guard; a mismatch is 409
  `concurrent-update`.
- **A6 Bounds.** Plain literals: at most 100 quote lines, 50 groups, 1000 units per
  line, a bounded number of sale windows per item and bands per table, amounts up to
  `shared.MaxMinor`. Beyond them the request is `validation-failed` (422).
- **A7 Integer ceiling.** Every bounded integer stays below the Postgres integer
  ceiling (`shared.MaxInt32`).
- **A8 Time.** Instants are UTC and truncated to the microsecond Postgres stores, so
  a database round trip never changes an aggregate.
- **A9 Tax.** A band is a closed range of 3-character postal prefixes; bands never
  overlap; the rate is 0..10000 basis points; a destination in no band is untaxed.
- **A10 Quote arithmetic.** Line total = unit price x quantity. A group is charged the
  tier's charge from the CURRENT schedule, free when the goods of the group reach the
  tier's free-over threshold. Tax = the band rate applied to goods plus delivery,
  rounded half up to a minor unit. Total = goods + delivery + tax.
- **A11 Missing policy tables.** With no CURRENT schedule or tax table a quote is
  refused with 422 `no-charge-schedule` or `no-tax-table`.
- **A12 Quote state on read.** A quote reads as EXPIRED once `expiresAt` has passed,
  whether or not the clock-driven policy stored it yet.
- **A13 Idempotency.** Every POST requires `Idempotency-Key` (1-128 characters); the
  first response below 500 is stored for 24 h per (route, key); the same key with
  another request is 422 `idempotency-key-reused`.
- **A14 IssuePriceQuote request.** `{cartId, lines[catalogItemId, quantity],
  groups[groupNo, offerId, tier, lineNos], destinationPostalPrefix}` (model-gap
  decision 5). The quote prices exactly what the checkout session froze; `offerId`
  is carried for traceability and is not priced.
- **A15 Policy loop.** PublishEffectivePriceOnWindowBoundary and the quote expiry
  run in-process every `POLICY_INTERVAL` (default 5 s) in `cmd/api`; each pass is
  idempotent and bounded to a batch.
- **A16 List paging.** `GET /item-prices` is bootstrap and rebuild only: ordered by
  catalogItemId, `after` is the last id of the previous page, `limit` 1..500
  (default 100), `next` is null on the last page.
- **A17 Clock.** The production clock returns UTC truncated to the microsecond (see A8).
- **A18 Schedules and tables.** A published version must exceed the current one
  (`non-increasing-version`, 409); publishing supersedes the previous one in the same
  transaction (states CURRENT to SUPERSEDED, model-gap decision 4).
- **A19 WithdrawPrice route.** `POST /item-prices/{catalogItemId}:withdraw`
  (model-gap decision 1).

## Consequences

Each point is covered by a domain, use-case or route test. A change to one needs a
superseding ADR.
