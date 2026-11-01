# ADR 0008: Adopt the fleet REST API guideline

## Status

Accepted (2026-11-01). Supersedes the idempotency, paging and route parts of
[ADR 0002](./0002-pricing-assumptions.md) (A13, A16, A19). Implements the warehouse REST API guideline
(STANDARD sections 1 to 4, enforced by `.spectral.yaml` rules R1 to R8 in the `api-lint` CI job).

## Context

The 2026-10-30 application portfolio assessment found three command-URL styles across the fleet, two spellings
of the replay header, key reuse answered 422 in most services and 409 in one, writes without an idempotency
key, and collections without paging. pricing had three `:custom-method` routes, a per-route `(route, key)`
idempotency store that was written *after* the handler ran (outside the unit of work), the header spelled
`Idempotent-Replayed`, reuse answered 422, and `GET /item-prices` paged with `after` and `next`.

## Decision

Cut over, with no alias, no dual route and no toggle. The cutover merges and deploys together with the other
services of the REST cutover set.

### Routes (R1, R2)

| Old | New | Rule |
|---|---|---|
| `POST /item-prices/{catalogItemId}:set-list-price` | `PUT /item-prices/{catalogItemId}/list-price` | replaces an attribute (1.3 a); idempotent by method |
| `POST /item-prices/{catalogItemId}:withdraw` | `POST /item-prices/{catalogItemId}/withdrawals` | lifecycle transition (1.3 d, table 1.4) |
| `POST /item-prices/{catalogItemId}/sales/{saleId}:cancel` | `POST /item-prices/{catalogItemId}/sales/{saleId}/cancellations` | lifecycle transition (1.3 d, table 1.4) |

`operationId`s (`setListPrice`, `withdrawPrice`, `cancelSale`), status codes and response bodies are unchanged.
`TestRetiredRoutesAreGone` pins that every old route answers 404 or 405.

### Idempotency (R3 to R6, R8)

- Every `POST` (the seven of the API; none was unwrapped before) sits behind `RequireIdempotencyKey(store, clock)`,
  route-scoped. `PUT /item-prices/{id}/list-price` takes no middleware: a key sent to it is accepted and
  ignored. No `PUT` of pricing appends a row, so the append exception (STANDARD 2.1) does not apply.
- `Idempotency-Key`: 1 to 128 printable ASCII characters. Missing: 400 `idempotency-key-required`; malformed
  (longer than 128, or a byte outside `0x20..0x7e`): 400 `idempotency-key-invalid`.
- The key is **global**: the stored record is looked up by key alone and compared by fingerprint
  (method + path + SHA-256 of the body). Same fingerprint: the stored answer is replayed with
  `Idempotency-Replayed: true` (and only then). Different fingerprint, including the same key on another route:
  **409** `idempotency-key-reused` (was 422).
- **Persistence.** `ports.IdempotencyStore` is now `Do(ctx, key, requestHash, at, fn)`. The Postgres store claims
  the key row inside the transaction it opens, runs the handler with a context that carries that transaction, and
  stores the answer before committing, so the key row, the aggregate change and the outbox rows commit together.
  A 2xx or a 4xx is stored; a 5xx, an error or a panic rolls everything back and stores nothing, so the retry
  re-executes. Concurrent requests with one key serialise on the `(route, key)` unique index (every row carries
  the empty route, so the existing table and migration are reused unchanged); the loser replays the winner's
  answer. The in-memory store used by tests and BDD holds the store lock for the same effect. Records live 24
  hours.
- A unit of work started inside another (the use cases' `transact` retry loop inside the key's transaction) is a
  savepoint in Postgres and a state snapshot in memory, so a failed attempt undoes only itself and the retry
  starts clean.

### Pagination (R7)

`GET /item-prices` answers `{ "items": [...], "nextCursor": "..." }` and takes `limit` (1 to 500, default 100)
and the opaque `cursor`; `nextCursor` is absent on the last page. The cursor is the fleet `page` package
(`internal/adapters/inbound/http/page`, copied unchanged): base64url JSON of the last row's sort key and id. The
list is ordered by `catalogItemId`, which is unique, so the keyset is that id as both sort key and id; no two
rows tie, in Postgres (`WHERE catalog_item_id > $1 ORDER BY catalog_item_id LIMIT $2`) or in memory. Bad `limit`:
400 `invalid-limit`; undecodable cursor: 400 `invalid-cursor`; any other query parameter (the retired `after`,
`offset`, `nextToken`, `from`): 400 `unknown-query-parameter`.

### Exemptions

None. Every `POST` is keyed and the one collection `GET` is paginated; no operation carries
`x-pagination-exempt`.

### Consumers changed

None in this repository: pricing has no web remote and no outbound HTTP client (its outbound adapters are
Postgres, Kafka and the outbox relay). Callers in other repositories (product-discovery's projection rebuild
of `GET /item-prices`, checkout's `POST /price-quotes`) change in their own pull requests of the cutover set.

## Consequences

- Breaking for every REST client: new URLs, a `PUT`, the list envelope, `Idempotency-Key` required (and a 409
  on reuse), `Idempotency-Replayed` instead of `Idempotent-Replayed`.
- A retried request can no longer double-publish: the key row and the outbox rows are one transaction.
- Tests: route tests with a real router, the Postgres repository contract (key store, nested unit of work,
  page walk) on testcontainers, and `cmd/api` integration tests that drive the shipped binary (fresh key,
  byte-identical replay, 409 reuse on another body and on another route, missing key, 4xx cached, N concurrent
  requests with one key run once).
