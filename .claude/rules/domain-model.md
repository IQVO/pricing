# Domain model: ubiquitous language, aggregates, events, use cases

## Ubiquitous Language (use these exact names)

- **ItemPrice** — the list price and sale windows of one catalogue item, versioned. Invariants: amounts are never negative, use the one configured currency, a sale price is below the list price, windows never overlap.
- **Effective price** — the live sale price, else the list price: what a shopper pays now.
- **SaleWindow** — a priced period `[from, to)` of an ItemPrice, cancellable.
- **DeliveryChargeSchedule** — the charge (and optional free-over threshold) per service tier; exactly one is CURRENT, a new version supersedes it.
- **TaxTable** — basis-point rates by destination postal-prefix band; bands never overlap; exactly one is CURRENT.
- **PriceQuote** — the binding, immutable, expiring (QuoteTtl, 15 min) price snapshot checkout confirms against.
- **CatalogItemView** — the local read model of offer-availability's catalogue items (known / discontinued).
- **Tier** — a delivery-promise service tier: SAME_DAY, NEXT_DAY, TWO_DAY, STANDARD.

## Aggregates

- **ItemPrice** (`internal/domain/itemprice`): ACTIVE -> WITHDRAWN (final). Raises ItemPriceChanged whenever the published view moves, ItemPriceWithdrawn on withdrawal.
- **DeliveryChargeSchedule** (`internal/domain/schedule`): CURRENT -> SUPERSEDED; a version strictly increases; every tier is named.
- **TaxTable** (`internal/domain/tax`): CURRENT -> SUPERSEDED; bands never overlap; rate within 0..10000 bp.
- **PriceQuote** (`internal/domain/quote`): ISSUED -> EXPIRED; every line names an ACTIVE price; immutable once issued.
- Shared value objects (Money, Tier, ids, errors) live in `internal/domain/shared`.

## Domain events

- `ItemPriceChanged` — the effective price (or list price, or sale end) of an item moved; CancelSale of a live sale raises it too.
- `ItemPriceWithdrawn` — an item's price was withdrawn (command or WithdrawOnDiscontinue).
- `DeliveryChargeScheduleChanged` — a new schedule became CURRENT.
- `TaxTableChanged` — a new tax table became CURRENT.
- `PriceQuoteIssued` — analytics only. There is no PriceQuoteExpired event.

## Key use cases (`internal/application/usecases`)

- `CreateItemPrice`, `SetListPrice`, `ScheduleSale`, `CancelSale`, `WithdrawPrice` — ItemPrice commands (itemprice.go); each loads, calls the domain, saves and appends events in one unit of work.
- `PublishChargeSchedule`, `PublishTaxTable` — supersede the CURRENT schedule/table (publish.go).
- `IssuePriceQuote` — prices the frozen cart lines at the effective price now (quote.go); `ExpireQuotes` is the QuoteTtl policy.
- `PublishBoundaries` — PublishEffectivePriceOnWindowBoundary (clock driven).
- `Consumer` — CatalogItemCreated / CatalogItemDiscontinued from offer-availability, with the WithdrawOnDiscontinue policy (consume.go).
- Queries: `GetItemPrice`, `ListItemPrices` (bootstrap/rebuild only), `GetCurrentChargeSchedule`, `GetCurrentTaxTable`, `GetPriceQuote`.
