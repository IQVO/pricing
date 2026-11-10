# CLAUDE.md — pricing

`pricing` is the **Pricing** bounded context (Core, `storefront` tier) of the
retail-network organisation's storefront domain (GitHub org `IQVO`, Go module
`github.com/IQVO/pricing`). It owns what a shopper pays: the list price and
sale windows per catalogue item (`ItemPrice`), the delivery charge per service
tier (`DeliveryChargeSchedule`), a synthetic tax table (`TaxTable`), and the
binding, immutable, expiring `PriceQuote` a checkout session is confirmed
against. One configured currency (`PRICING_CURRENCY`), integer minor units;
promotions are a later phase. It keeps a local `CatalogItemView` fed by
offer-availability's events. It ships two binaries: `cmd/api` (REST + outbox
relay + the offer-availability consumer + the clock-driven policies) and
`cmd/a2a` (read-only A2A, `:8093`, skills `item_price` and `quote_status`).
No MCP server, no `web/`.

## Hard rules (each one cost an incident or is fitness-tested)

1. **Hexagonal dependency rule.** `internal/domain` depends on nothing internal
   but domain; `internal/application` only on domain and application; adapters
   never depend on each other; only `cmd/` wires layers. `make arch-test`
   (`TestHexagonalArchitecture`) enforces it.
2. **CloudEvents 1.0 is MANDATORY** on every Kafka message produced or
   consumed (structured mode, no flat envelope, no dual-write, no toggle:
   `TestNoEventEnvelopeToggleOrFlatEnvelope`, `TestCloudEventsOnly`). Build and
   decode only through `internal/adapters/kafka/cloudevents/`; `type` is
   `com.retailnetwork.storefront.pricing.<entity>.<Event>`, `source`
   `/retailnetwork/pricing`, subject and Kafka key = the aggregate id. The
   `dataschema` stream segment names the actual stream (ADR 0006:
   `urn:retailnetwork:pricing:analytics:PriceQuoteIssued:v1`). A breaking
   payload change is a new `.v2` type. Catalogue:
   `docs/adr/0004-cloudevents-envelope-and-type-catalogue.md`, kept in step with
   `apis/asyncapi.yaml` by `TestEventCatalogueMatchesContract`.
3. **Events leave through the transactional outbox only**: the aggregate row
   and its encoded events commit in one unit of work; the always-on relay in
   `cmd/api` publishes, capped by `OUTBOX_MAX_ATTEMPTS`
   (`TestOutboxRelaysPassAttemptCap`). Four integration events go to
   `retailnetwork.pricing.events`; `PriceQuoteIssued` goes to
   `retailnetwork.pricing.analytics`. There is no `PriceQuoteExpired` event.
4. **`ItemPriceChanged` fires whenever the effective price moves**: a
   list-price change, a sale window starting or ending (the
   `PublishBoundaries` policy) or a `CancelSale` of a live sale. A change that
   moves nothing raises nothing.
5. **Consume offer-availability, never call it** (ADR 0003). Only
   `CatalogItemCreated` and `CatalogItemDiscontinued` from
   `retailnetwork.offer-availability.events`; claim the CloudEvents `id` in the
   inbox inside the unit of work, dispatch on the full `type`, ignore unknown
   types, dead-letter a payload that can never apply (never on shutdown
   cancellation: `TestDeadLetterCallsGuardCancellation`). The group id comes
   from the mandatory `OFFER_AVAILABILITY_CONSUMER_GROUP`
   (`TestKafkaConsumerGroupNeverHardcodedInline`). Pricing never calls a
   sibling over HTTP.
6. **Domain invariants**: amounts never negative, one currency, a sale price
   below the list price, sale windows never overlap; exactly one CURRENT
   schedule and tax table, versions strictly increase, tax bands never
   overlap; a PriceQuote is immutable once issued, names only ACTIVE prices and
   expires by the QuoteTtl policy (15 minutes).
7. **Every POST is behind `RequireIdempotencyKey`** (ADR 0008); `PUT
   /item-prices/{catalogItemId}/list-price` is idempotent by method and takes no
   key. `GET /item-prices` is keyset-paged (`limit`, `cursor`, `nextCursor`).
   Errors are RFC 9457 problems `/problems/<slug>`.
8. **Real Postgres and Kafka only**; the memory adapters are test doubles
   (`TestRuntimeBinaryHasNoInMemoryAdapters`), integration tests use
   testcontainers only.
9. **No auth layer on REST or A2A** (fleet-wide revert 2026-09-11,
   `TestNoAuthMiddlewareReintroduced`). The A2A agent is read-only, has no
   Kafka and no relay, and makes no outbound A2A call (`TestA2AAgentIsReadOnly`,
   `TestA2ABinaryHasNoKafkaOrRelay`, `TestNoOutboundA2AClient`).
10. **An ADR comes first** for a new consumed or published event, a sibling
    call, promotions, a second currency or any auth. ADRs live in `docs/adr/`.

## Commands

```bash
make check-fast   # fmt-check + vet + arch-test + tests of changed packages: run before saying "done"
make check        # fmt-check vet build lint test
make check-all    # check + coverage (90% gate on domain+application) + arch-test + bdd
make integration  # real Postgres/Kafka via testcontainers (needs Docker)
make mutation     # gremlins over every internal/domain package (CI job mutation-fast, blocking)
make vuln         # govulncheck ./...
make guide-lint   # these guides: skills load, references resolve, context budget
```

Run locally: `DATABASE_URL=... KAFKA_BROKERS=...
OFFER_AVAILABILITY_CONSUMER_GROUP=... go run ./cmd/api` (REST `:8080`;
migrations at boot; `/readyz` gates on Postgres and Kafka);
`DATABASE_URL=... A2A_AGENT_URL=... go run ./cmd/a2a`.

## Where things are written down (read before editing the matching area)

- `.claude/rules/domain-model.md`: ubiquitous language, aggregates, events, use
  cases and policies (always loaded). `.claude/rules/rest-api.md`: routes ->
  use cases. `.claude/rules/integration-events.md`: published and consumed
  types, outbox, consumer.
- `.claude/rules/fleet/*.md`: fleet-wide rules copied from the harness
  template; never edit them here.
- `docs/adr/`: decisions (index: `docs/adr/index.md`). `apis/openapi.yaml`,
  `apis/asyncapi.yaml`: contracts (`api-lint` runs the Spectral
  `rest-guideline` ruleset from `.spectral.yaml`). Operating docs:
  `docs/docs/operations/` (configuration, runbook, observability,
  troubleshooting), `docs/docs/development/testing.md`,
  `docs/docs/ecosystem/integration.md`. `HARNESS.md`: what each sensor runs.
- `charts/pricing/`: the Helm chart (api always on; A2A opt-in).

harness-template: v4
(hooks, guide-lint).

<!-- harness:scoped-rules:start (generated by tools/migrate_v3.py in warehouse-harness-template; do not hand-edit) -->
## Scoped rules and harness

Claude Code loads each rule below automatically when you touch the matching paths. OpenCode and Codex do NOT: read the rule BEFORE editing matching files.

| When touching | Read |
|---|---|
| `internal/adapters/**/kafka/**`, `internal/adapters/outbound/events/**`, `apis/asyncapi*` | `.claude/rules/integration-events.md` |
| `internal/adapters/inbound/http/**`, `apis/openapi*.yaml`, `apis/openapi/**` | `.claude/rules/rest-api.md` |

Hooks (`scripts/harness/hook.py`, wired for Claude Code, Codex and OpenCode) block pushes to develop/main, `--no-verify`, bare `rm -rf`, and edits to generated files, and feed gofmt/vet findings back after each edit. Before saying "done" run `make check-fast`; the full gate is `make check-all`. `HARNESS_OFF=1` disables the hooks when debugging the harness itself.
<!-- harness:scoped-rules:end -->
