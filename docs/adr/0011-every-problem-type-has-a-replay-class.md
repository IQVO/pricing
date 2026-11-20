# ADR 0011: Every problem type has a replay class; a retryable refusal is not stored

- Status: Accepted (2026-11-20)
- Builds on [ADR 0008](/docs/adr/0008-adopt-the-fleet-rest-api-guideline) (the idempotency section) and [ADR 0003](/docs/adr/0003-consumed-contracts-and-local-read-model).
- Fleet rule: `.claude/rules/fleet/idempotency-and-outbox.md`; standard: warehouse-harness-template `docs/idempotency-replay-classes.md`. Owner decision 2026-11-18, adopted fleet-wide.

## Context

ADR 0008 stored the first answer below 500 of a key — a 4xx too — and replayed it for
24 hours. A refusal that a retry could pass was thereby pinned: a `409 concurrent-update`
replayed to a caller that did the right thing and retried; a `422 unknown-catalog-item`
replayed for 24 hours to a caller whose item had become known a moment later. Status
codes cannot separate the cases (`409` covers both a lost race and a terminal state),
so the fleet classified **per problem type**.

## Decision

A retried request is answered from the store only when its first answer is **final for
that request**. Every problem type of the catalogue declares exactly one replay class
next to its status and slug (`problemCatalogue`, field `replay`):

- **FINAL** — the answer is a function of the request alone (or of state that only ever
  moves one way); stored and replayed.
- **OUTCOME** — the command committed state and then answered an error that must be
  remembered; the writes commit with the stored answer.
- **RETRYABLE** — nothing committed and the answer depends on state that can change
  (an optimistic-lock conflict, a referenced fact not known yet, an aggregate state not
  reached yet); NOT stored, the key is released, a retry executes again.

A 2xx is stored; a 5xx and a panic are never stored; a replay carries
`Idempotency-Replayed: true`. The middleware holds no slug list and no status ranges
beyond 2xx/5xx: `writeError` marks the capturing writer with the class of the problem
it answers, the capture turns that into `idempotency.Record.Release`, and the store
releases exactly like its 5xx branch (the whole request rolls back, key row included).
A runtime refusal with no class (a handler writing a bare status) is released like
RETRYABLE as the safe fallback; `TestAnUnclassifiedErrorAnswerIsNotStored` pins it and
`TestEveryProblemTypeDeclaresAReplayClass` keeps the catalogue complete. Releasing a
RETRYABLE key means a different body under the same key afterwards is a **new request**,
not `idempotency-key-reused`. No consumer-side or client-side retry behaviour is added:
callers driven by people must surface a refusal; automation retries on its own.

### The store design this rides on (verified from code)

`ports.IdempotencyStore.Do` runs the handler inside the transaction that claims the key
(`internal/adapters/outbound/postgres/idempotency.go`): the claim
`INSERT ... ON CONFLICT (route, key)` (route is always `''`, so the key is global) goes
in first, the handler runs behind `SAVEPOINT handler_run` with the transaction carried
in the context (every unit of work joins it), and the stored answer
(`UPDATE ... SET status, content_type, body`) commits with whatever the handler wrote.
A 4xx whose savepoint release succeeds therefore **commits its writes with the key row**
— OUTCOME *can* exist in this design. It does not arise here: every keyed use case of
pricing returns its refusal before it saves anything (load, domain call, then persist;
`internal/application/usecases/itemprice.go`, `publish.go`, `quote.go`), so no refusal
commits state and **no problem type is OUTCOME**. A statement that fails on the server
aborts the transaction; `ROLLBACK TO SAVEPOINT handler_run` then discards the handler's
half-done work, heals the transaction and stores the truthful 4xx with the claim —
unchanged. A 5xx, an error from `fn`, or `Record.Release` returns before the store
`UPDATE`; the deferred `Rollback` drops claim and writes, so the key is free. A
concurrent duplicate blocks on the `(route, key)` unique index until the first
transaction ends: it replays the winner's stored answer or, when the first ended
RETRYABLE (nothing committed, the row never existed), claims the key and runs the
handler itself — it never replays a released refusal and never runs alongside the first
(`TestACopyWaitingOnARetryableFirstRunsItself`). TTL is 24 hours, enforced at read and
by claim takeover of an expired row. The in-memory double
(`internal/adapters/outbound/memory/infra.go`) holds the store lock for the same
semantics.

### Class table

| Problem type | Class | Status | Reason (code) |
| --- | --- | --- | --- |
| `malformed-request` | FINAL | 400 | the body is not valid JSON for the operation: `decodeJSON` (response.go) |
| `not-found` | RETRYABLE | 404 | mixed slug: the item price of the path is created by another request after an offer-availability event (ADR 0003), and `itemprice.ErrUnknownSale` shares the slug; splitting is a follow-up |
| `overlapping-sale-window` | RETRYABLE | 409 | judged against the item's live windows (itemprice.go `ScheduleSale`): a cancelled or ended window stops overlapping |
| `non-increasing-version` | FINAL | 409 | versions strictly increase (CLAUDE.md rule 6) and nothing is deleted, so the current version only moves up: a version refused today never passes (schedule.go / tax.go `Publish`) |
| `concurrent-update` | RETRYABLE | 409 | the optimistic-lock guard or a unique-index race (itemprices.go `writeItemPrice`) after the use case's `commandAttempts` |
| `illegal-transition` | RETRYABLE | 409 | withdraw an ACTIVE price, cancel a live sale: aggregate state other requests move |
| `unknown-catalog-item` | RETRYABLE | 422 | the local CatalogItemView is fed by offer-availability's events (ADR 0003): the fact can arrive after the request (itemprice.go `checkCatalog`) |
| `sale-not-below-list` | RETRYABLE | 422 | judged against the current list price (itemprice.go `ScheduleSale`), which `PUT .../list-price` moves |
| `incomplete-charge-schedule` | FINAL | 422 | the tier set is the fixed enum `shared.Tiers()` (schedule.go `orderTiers`): the request alone |
| `overlapping-tax-band` | FINAL | 422 | the bands of the request against each other (tax.go) |
| `rate-out-of-range` | FINAL | 422 | a band rate of the request (tax.go) |
| `item-not-priced` | RETRYABLE | 422 | a line whose item has no ACTIVE price now (quote.go `priceLines`): prices are created and withdrawn by other requests |
| `empty-quote` | FINAL | 422 | a quote has at least one line (quote.go `checkShape`): the request alone |
| `no-charge-schedule` | RETRYABLE | 422 | no CURRENT schedule yet (quote.go `Issue`: `sched == nil`); one is published by another request |
| `no-tax-table` | RETRYABLE | 422 | no CURRENT tax table yet (quote.go `Issue`: `table == nil`); same |
| `negative-amount` | FINAL | 422 | an amount of the request (shared/money.go `Check`) |
| `currency-mismatch` | FINAL | 422 | the request's currency against the one configured currency (shared/money.go `Check`); the configuration is fixed at deploy time |
| `validation-failed` | RETRYABLE | 422 | mixed: mostly request fields, but "an item holds at most 50 sale windows" (itemprice.go `ScheduleSale`) counts the item's live windows; splitting is a follow-up |

`TestTheADRDeclaresTheClassOfEveryProblem` keeps this table equal to the catalogue in
both directions.

Outside the replay decision (never reach it): `idempotency-key-required` /
`idempotency-key-invalid` (written before the store runs), `idempotency-key-reused`
(decided on the replay path), `invalid-limit` / `invalid-cursor` /
`unknown-query-parameter` (GET collection parameters only), `resource-not-found`
(the NUL guard, before the middleware), `dependency-unavailable` (`/readyz`) and
`internal-error` (unmapped; a 5xx is never stored).

## Judgement calls

1. **`non-increasing-version` is FINAL, not RETRYABLE.** The answer compares the
   request's version with the current published one, which is state — but the domain
   invariant "versions strictly increase" (CLAUDE.md rule 6, enforced by
   `schedule.Publish` / `tax.Publish` refusing `version <= currentVersion`) plus
   "nothing is ever deleted" make that state monotone: a version that does not exceed
   the current one today never will. Storing the refusal costs nothing; releasing it
   would cost an execution that can only be refused again.
2. **`validation-failed` is RETRYABLE although almost all its causes are
   request-only** (ids, quantities, windows, bands): one cause counts the item's live
   sale windows (`MaxSales`), which other requests change. One class per slug; the
   default rule picks RETRYABLE; splitting the slug is a recorded follow-up, not part
   of this change.
3. **`not-found` is RETRYABLE although `itemprice.ErrUnknownSale` alone would be
   FINAL** (pricing mints sale ids and never deletes them): `ports.ErrNotFound` shares
   the slug with causes that can clear (the item price appears with the consumed
   event), so the slug is mixed and RETRYABLE until split.
4. **`unknown-catalog-item` is RETRYABLE even for a DISCONTINUED item**, whose cause
   can never clear (discontinuation is terminal): the same entry answers both, and the
   mixed-state default plus the event-fed view make RETRYABLE the safe side.
5. **No OUTCOME.** The store would support one (a 4xx's writes commit with the key
   row), but no keyed use case of pricing writes before it refuses; a future one that
   does must prove it with the OUTCOME test of the rendered template.

## Consequences

- A refusal of class RETRYABLE no longer pins a key: checkout's derived key
  `<Idempotency-Key>:price-quote` on `POST /price-quotes` (the only deterministic-key
  caller; checkout `open_session.go` `priceQuoteKey`) now re-executes when
  `no-charge-schedule`, `no-tax-table`, `item-not-priced`, `unknown-catalog-item` or
  `validation-failed` cleared, instead of replaying the stale refusal for 24 hours.
  The caller side is out of scope (no client retry behaviour is added here).
- A different body under a released key is a new request, so the reuse conflict only
  answers against a stored (FINAL, OUTCOME or 2xx) answer.
- The OpenAPI `Idempotency-Key` description, the README, the REST rule, the
  troubleshooting guide and the BDD feature now say which refusals replay.
- Retention: expired `idempotency_keys` rows are still only skipped, never deleted —
  unchanged by this ADR; recorded as an open point (a purge sweep is a separate change,
  see order-sourcing #84 / last-mile-delivery #86 for the shape).
