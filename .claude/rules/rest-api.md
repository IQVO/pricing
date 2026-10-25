---
paths:
  - "internal/adapters/inbound/http/**"
  - "apis/openapi*.yaml"
  - "apis/openapi/**"
---

# REST API (inbound adapter)

Routes of `apis/openapi.yaml`, served by `internal/adapters/inbound/http` (chi); every
POST requires `Idempotency-Key`.

- `POST /item-prices` -> `CreateItemPrice`
- `GET /item-prices` -> `ListItemPrices` (bootstrap/rebuild only; `after`, `limit` 1..500)
- `GET /item-prices/{catalogItemId}` -> `GetItemPrice`
- `POST /item-prices/{catalogItemId}:set-list-price` -> `SetListPrice`
- `POST /item-prices/{catalogItemId}:withdraw` -> `WithdrawPrice`
- `POST /item-prices/{catalogItemId}/sales` -> `ScheduleSale`
- `POST /item-prices/{catalogItemId}/sales/{saleId}:cancel` -> `CancelSale`
- `POST /delivery-charge-schedules` -> `PublishChargeSchedule`
- `GET /delivery-charge-schedules/current` -> `GetCurrentChargeSchedule`
- `POST /tax-tables` -> `PublishTaxTable`
- `GET /tax-tables/current` -> `GetCurrentTaxTable`
- `POST /price-quotes` -> `IssuePriceQuote`
- `GET /price-quotes/{quoteId}` -> `GetPriceQuote`
- `GET /healthz` (liveness), `GET /readyz` (drain gate + Postgres ping + Kafka metadata)

## Conventions

- Error shape: RFC 9457 `application/problem+json`, `type` = `/problems/<slug>`;
  the mapping is the catalogue in `internal/adapters/inbound/http/errors.go`.
- A NUL character in a path, query or body is a client error (404 / 400), never a 500.
- Auth: none. Fleet-wide REST+MCP auth was deliberately reverted 2026-09-11;
  `internal/architecture/fitness_test.go`'s TestNoAuthMiddlewareReintroduced guards it.
