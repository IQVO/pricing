# pricing

[![CI](https://github.com/IQVO/pricing/actions/workflows/ci.yml/badge.svg?branch=develop)](https://github.com/IQVO/pricing/actions/workflows/ci.yml?query=branch%3Adevelop) [![coverage gate](https://img.shields.io/badge/coverage%20gate-90%25-brightgreen)](https://github.com/IQVO/pricing/blob/develop/Makefile) [![Go](https://img.shields.io/badge/go-1.27.3-00ADD8?logo=go&logoColor=white)](https://github.com/IQVO/pricing/blob/develop/go.mod) [![release](https://img.shields.io/github/v/release/IQVO/pricing?sort=semver)](https://github.com/IQVO/pricing/releases) [![docs](https://img.shields.io/badge/docs-iqvo.github.io-blue)](https://iqvo.github.io/pricing/) [![license](https://img.shields.io/badge/license-MIT-blue)](https://github.com/IQVO/pricing/blob/develop/LICENSE)

Storefront (e-commerce) domain of the retail-network organisation, bounded context **Pricing** (Core, storefront tier).

Owns what a shopper pays: the list price and sale windows per catalogue item, the delivery charge per service tier, a synthetic tax table, and the binding, immutable, expiring PriceQuote a checkout session is confirmed against. One configured currency, integer minor units; promotions are a later phase (D10).

Status: built (REST API, outbox relay, offer-availability consumer, policies). This repository is part of the storefront domain: it consumes the retail network's Open Host Service routes and its published `retailnetwork.*` events; it never calls an in-org or restricted network route.

## Disclaimer

This project is a domain-driven design study. It is not associated with, endorsed by or affiliated with any company, and it uses no company's code, data, logos or trademarks. All names, figures and examples are illustrative or synthetic.

## Documentation

Published at **[https://iqvo.github.io/pricing/](https://iqvo.github.io/pricing/)**
(Docusaurus, sources in [`docs/`](docs/); built by `.github/workflows/docs.yml`).
The cross-context design lives in
[warehouse-docs](https://iqvo.github.io/warehouse-docs/storefront/).

| Area | Pages |
| --- | --- |
| Overview | [Introduction](https://iqvo.github.io/pricing/docs/overview/introduction), [Architecture](https://iqvo.github.io/pricing/docs/overview/architecture), [Quickstart](https://iqvo.github.io/pricing/docs/overview/quickstart) |
| Operations | [Configuration](https://iqvo.github.io/pricing/docs/operations/configuration), [Runbook](https://iqvo.github.io/pricing/docs/operations/runbook), [Observability](https://iqvo.github.io/pricing/docs/operations/observability), [Troubleshooting](https://iqvo.github.io/pricing/docs/operations/troubleshooting), [Before the first release](https://iqvo.github.io/pricing/docs/operations/before-first-release) |
| Development | [Testing](https://iqvo.github.io/pricing/docs/development/testing) |
| Domain-driven design | [Ubiquitous language](https://iqvo.github.io/pricing/docs/ddd/ubiquitous-language), [Use cases](https://iqvo.github.io/pricing/docs/ddd/use-cases), [Subdomain classification](https://iqvo.github.io/pricing/docs/ddd/subdomain-classification), [DDD artifacts](https://iqvo.github.io/pricing/docs/ddd/ddd-artifacts) |
| ddd-crew artifacts | [Core domain chart](https://iqvo.github.io/pricing/docs/ddd/core-domain-chart), [Bounded context canvas](https://iqvo.github.io/pricing/docs/ddd/bounded-context-canvas), [Context map](https://iqvo.github.io/pricing/docs/ddd/context-map), [Aggregate design canvas](https://iqvo.github.io/pricing/docs/ddd/aggregate-design-canvas), [Domain message flow](https://iqvo.github.io/pricing/docs/ddd/domain-message-flow), [EventStorming](https://iqvo.github.io/pricing/docs/ddd/eventstorming), [Class diagram](https://iqvo.github.io/pricing/docs/ddd/class-diagram), [Entity relationship](https://iqvo.github.io/pricing/docs/ddd/entity-relationship), [Sequence diagrams](https://iqvo.github.io/pricing/docs/ddd/sequence-diagrams), [Domain events](https://iqvo.github.io/pricing/docs/ddd/domain-events) |
| API reference | [API overview](https://iqvo.github.io/pricing/docs/api-reference) (REST, A2A), [REST reference generated from `apis/openapi.yaml`](https://iqvo.github.io/pricing/docs/api-reference/rest/pricing-api), [Event catalogue](https://iqvo.github.io/pricing/docs/api-reference/events) |
| Ecosystem | [Integration](https://iqvo.github.io/pricing/docs/ecosystem/integration) |
| Decisions | [ADR index](https://iqvo.github.io/pricing/docs/adr) and the sources in [`docs/adr/`](docs/adr/) |
| In this repo | [`docs/docs/operations/runbook.md`](docs/docs/operations/runbook.md), [`docs/docs/operations/before-first-release.md`](docs/docs/operations/before-first-release.md) |
| Aggregator | [Pricing in the storefront pack](https://iqvo.github.io/warehouse-docs/storefront/contexts/pricing) and the [Storefront strategic design](https://iqvo.github.io/warehouse-docs/storefront/strategic-design/) |

## Architecture

Hexagonal / ports & adapters; dependencies point **inward only**.

```
cmd/api/                    composition root: REST API, outbox relay, the offer-availability
                            consumer and the clock-driven policies
cmd/a2a/                    read-only A2A agent over the same read use cases (ADR 0007)
internal/
  domain/                   pure business logic: itemprice, quote, schedule, tax, shared
  application/
    ports/                  OUT interfaces only (repos, EventPublisher, Clock, metrics)
    usecases/               one struct per use case
    idempotency/            idempotency-key pool
    outbox/                 transactional outbox
    views/                  read views (the local CatalogItemView, ADR 0003)
  adapters/
    inbound/                http (chi), kafka (the consumer), a2a, requestlog
    outbound/               postgres, kafka, outbox relay, telemetry, clock, ids,
                            memory (test doubles), repocontract
    kafka/cloudevents/      the CloudEvents 1.0 Kafka writer
  architecture/             the fitness tests below
  bootretry/                bounded Postgres/migration boot retry
  bdd/                      godog wiring for features/
```

There is no in-memory mode and no log sink: `cmd/api` needs a database and a
broker, and exits at boot naming the missing variable. The layering is
enforced as an executable test, not a convention:
`internal/architecture/architecture_test.go` (`TestHexagonalArchitecture`,
arch-go) fails the build if the domain ever imports an outer layer or an
adapter reaches sideways into a sibling; the same package adds fitness tests
for the event catalogue vs `apis/asyncapi.yaml`, the A2A adapter's read-only
rule, Kafka writer acks/topics/DLQ shutdown, the outbox relay cap, and
no-Go-files-at-root.

## Domain events

Contract: [`apis/asyncapi.yaml`](apis/asyncapi.yaml); the type catalogue is ADR 0004.

**Published** — five integration events through a **transactional outbox** (the
aggregate row and its already-encoded events are written in ONE database
transaction; an always-on relay drains them to Kafka, at-least-once,
stable CloudEvents `id` for dedupe):

- → `retailnetwork.pricing.events`: `ItemPriceChanged`, `ItemPriceWithdrawn`,
  `DeliveryChargeScheduleChanged`, `TaxTableChanged`. `ItemPriceChanged` is
  raised whenever the effective price moves: a list-price change, a sale
  window starting or ending (the clock-driven
  `PublishEffectivePriceOnWindowBoundary` policy) or a `CancelSale` of a live
  sale (CancelSale has no event of its own). Consumers include
  `shopping-cart` and `product-discovery`.
- → `retailnetwork.pricing.analytics` (analytics-only): `PriceQuoteIssued`.

There is no `PriceQuoteExpired` event: quote expiry is checkout-internal TTL
state.

**Consumed** — `CatalogItemCreated` and `CatalogItemDiscontinued` from
`retailnetwork.offer-availability.events` feed the local `CatalogItemView`
(ADR 0003); a discontinued item also runs the `WithdrawOnDiscontinue` policy
(its ACTIVE price is withdrawn and `ItemPriceWithdrawn` published). The
consumer group name comes from the mandatory
`OFFER_AVAILABILITY_CONSUMER_GROUP`. Unknown fields are ignored; the
producer's own AsyncAPI is authoritative.

**Envelope** (mandatory): CloudEvents 1.0, structured content mode, Kafka
header `content-type: application/cloudevents+json; charset=UTF-8`; `source` =
`/retailnetwork/pricing`, `subject` and Kafka key = the aggregate id,
`dataschema` = `urn:retailnetwork:pricing:events:<EventName>:v1`. Type
convention, real examples:

```
com.retailnetwork.storefront.pricing.itemprice.ItemPriceChanged
com.retailnetwork.storefront.pricing.pricequote.PriceQuoteIssued
```

A breaking payload change is a new `.v2` type, never a mutation. Dispatch on
the full `type`, ignore unknown types, dedupe on `id`.

## Running it

Real PostgreSQL and Kafka only (`DATABASE_URL`, `KAFKA_BROKERS` and
`OFFER_AVAILABILITY_CONSUMER_GROUP` are mandatory; a boot error names the
missing variable). Migrations run automatically at boot. There is no compose
file in this repo — point the variables at any Postgres/Kafka (the fleet's
`warehouse-infra` kind cluster or the integration tests' testcontainers both
work).

```sh
export DATABASE_URL="postgres://user:***@localhost:5432/pricing?sslmode=disable"
export KAFKA_BROKERS="localhost:9092"
export OFFER_AVAILABILITY_CONSUMER_GROUP=pricing-offer-availability
go run ./cmd/api      # REST on :8080; relay, consumer and policies in the same process
```

`GET /readyz` is 200 only while Postgres answers a ping AND a Kafka broker
answers a metadata request; `/healthz` is liveness only. Assumptions:
[`docs/adr/0002-pricing-assumptions.md`](docs/adr/0002-pricing-assumptions.md).

### Configuration

`cmd/api`:

| Env var | Default | Meaning |
|---|---|---|
| `HTTP_ADDR` | `:8080` | REST listen address |
| `DATABASE_URL` | **required** | Postgres DSN; unset ⇒ boot error naming the variable |
| `MIGRATIONS_DATABASE_URL` | `DATABASE_URL` | Direct (non-pooled) DSN for the boot migrations |
| `KAFKA_BROKERS` | **required** | Comma-separated brokers (relay, consumer, `/readyz`) |
| `OFFER_AVAILABILITY_CONSUMER_GROUP` | **required** | Stable group id of the consumer |
| `OUTBOX_RELAY_INTERVAL` | `1s` | Relay poll interval (Go duration or seconds) |
| `PRICING_CURRENCY` | `BRL` | The one ISO 4217 currency |
| `POLICY_INTERVAL` | `5s` | Clock-driven policy pass interval (sale window boundaries) |
| `OUTBOX_MAX_ATTEMPTS` | `20` | Failures before the relay excludes a row (`0` = unlimited) |
| `SHUTDOWN_DRAIN_DELAY` | `5s` | Wait after flipping `/readyz` before closing |
| `LOG_LEVEL` | `info` | `debug`/`info`/`warn`/`error` |
| `OTEL_SERVICE_NAME` | `pricing` | `service.name` of spans and metrics |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | OTLP/gRPC endpoint (the chart sets the in-cluster Collector); export never blocks |
| `SERVICE_VERSION` | `dev` | `service.version` |
| `ENVIRONMENT` | `local` | `deployment.environment.name` |

`cmd/a2a` reads `DATABASE_URL` and `A2A_AGENT_URL` (both **required**; the
latter is the absolute http(s) URL advertised on the card),
`MIGRATIONS_DATABASE_URL` (default `DATABASE_URL`), `A2A_ADDR` (default
`:8093`), `LOG_LEVEL` and the shared telemetry variables (`OTEL_SERVICE_NAME`
defaults to `pricing-a2a`; `SERVICE_VERSION` also versions the Agent Card). It
reads and writes no Kafka topic and runs no outbox relay.

## Observability

Logs, metrics and traces follow the fleet standard ([ADR
0005](docs/adr/0005-fleet-observability-standard.md)): JSON logs on stdout
carry `trace_id`/`span_id` and a `request_id`; `cmd/api` exports OTLP/gRPC to
the Collector (`OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_SERVICE_NAME`,
`SERVICE_VERSION`, `ENVIRONMENT`; export never blocks, so no Collector costs
telemetry, nothing else); the trace continues across Kafka in the
`traceparent` header of each published event and is picked up by the consumer.

| Instrument | Kind | Source |
|---|---|---|
| `pricing.quotes.issued` | counter, attributes `outcome`, `reason` | **this service's business metric** |
| HTTP request duration | histogram + one server span and request-log line per call | the HTTP adapter, sharing a trace id |
| `go.*` runtime metrics | various | OTel Go runtime instrumentation |

## API

Contract: [`apis/openapi.yaml`](apis/openapi.yaml). No authentication. Errors
are RFC 9457 problem details (`application/problem+json`). Every
state-changing request takes an `Idempotency-Key` header; a retry with the
same key replays the first response.

| Method | Path | Purpose |
|---|---|---|
| `POST` | `/item-prices` | Publish an item price |
| `GET` | `/item-prices` | List item prices |
| `GET` | `/item-prices/{catalogItemId}` | Get one item price (state, list price, effective price, sale windows) |
| `POST` | `/item-prices/{catalogItemId}:set-list-price` | Set the list price |
| `POST` | `/item-prices/{catalogItemId}:withdraw` | Withdraw the price |
| `POST` | `/item-prices/{catalogItemId}/sales` | Schedule a sale window |
| `POST` | `/item-prices/{catalogItemId}/sales/{saleId}:cancel` | Cancel a sale |
| `POST` | `/delivery-charge-schedules` | Publish a delivery charge schedule |
| `GET` | `/delivery-charge-schedules/current` | Get the current schedule |
| `POST` | `/tax-tables` | Publish a tax table |
| `GET` | `/tax-tables/current` | Get the current tax table |
| `POST` | `/price-quotes` | Issue a PriceQuote (binding, immutable, expiring) |
| `GET` | `/price-quotes/{quoteId}` | Get a quote (`ISSUED` or `EXPIRED` now) |
| `GET` | `/healthz` | Liveness |
| `GET` | `/readyz` | Readiness (Postgres ping + Kafka metadata) |

## Helm chart

[`charts/pricing`](charts/pricing) — one always-on component: the `api`
Deployment (REST, relay, consumer and policies in one process); the read-only
A2A agent is an optional extra component (`a2a.enabled=true`, default off).
The chart refuses to render without `database.*`, `kafka.brokers` and
`config.offerAvailabilityConsumerGroup` (named in the failure). `DATABASE_URL`
always comes from a Secret.

```sh
helm install pricing ./charts/pricing \
  --set database.existingSecret=pricing-db \
  --set kafka.brokers=kafka:9092 \
  --set config.offerAvailabilityConsumerGroup=pricing-offer-availability
```

Telemetry is the `otel` values block (`enabled: true` by default, safe where
no Collector is deployed). Chart invariants are asserted by
`charts/pricing/tests/` (`render_test.sh`, `test_service_selectors.py`), run
in CI by the `helm-lint` (`ct lint`) and `chart-tests` jobs.

### Running the A2A agent in Kubernetes

`cmd/a2a` ([ADR 0007](docs/adr/0007-a2a-inbound-adapter.md)) is a second
binary in the same image (`/app/a2a`) that serves an Agent2Agent agent over
JSON-RPC: the Agent Card is at `/.well-known/agent-card.json`, the endpoint is
mounted at `/` and `/a2a`, `GET /healthz` serves the probes. The Helm chart
deploys it as `<release>-a2a` when `a2a.enabled=true` (default `false`),
routed at `/api/a2a/pricing` when `a2a.route.enabled=true`.

Two skills, called as a `SendMessage` data part
`{"skill": "<id>", "arguments": {...}}`; each returns what the matching REST
`GET` returns:

- `item_price`: `{catalogItemId}` returns the state, list price, effective
  price now and sale windows (`GET /item-prices/{catalogItemId}`).
- `quote_status`: `{quoteId}` returns the quote, `ISSUED` or `EXPIRED` now,
  with its lines, delivery charges, tax and total
  (`GET /price-quotes/{quoteId}`).

Rejections come back as an in-band RFC 7807 `problem` with the REST status
class (`404` unknown id, `400` malformed request). A2A carries delegation
only; facts still leave as CloudEvents on Kafka.

## Local development / quality gate

Every CI sensor is a `make` target; `make help` lists them all.

```sh
make check-fast  # fmt-check, vet, arch-test (+ tests of changed packages)
make check       # fmt-check, vet, build, lint, test
make check-all   # check + coverage (90% gate over domain+application) + arch-test + bdd
make integration # testcontainers Postgres/Kafka — needs only Docker
make mutation    # gremlins over ./internal/domain
make vuln        # govulncheck ./...
```

Git hooks are managed by [lefthook](https://github.com/evilmartians/lefthook)
(`lefthook.yml`): pre-commit runs `fmt-check` + `vet` + `lint`; pre-push runs
`make check`. Activate once per clone:

```sh
brew install lefthook   # or: go install github.com/evilmartians/lefthook@latest
lefthook install
```

## Tests

```sh
go test ./... -race                        # unit suites
make coverage                              # domain+application coverage, CI gate >= 90%
go test -tags=integration ./...            # testcontainers Postgres/Kafka (Docker only)
go test ./features/ -run TestFeatures -v   # godog/BDD over the real HTTP API
go test ./internal/architecture/... -v     # architecture fitness tests (arch-go)
gremlins unleash ./internal/domain         # mutation, thresholds in .gremlins.yaml
```

CI (`.github/workflows/ci.yml`) runs `lint`, `guide-lint`, `complexity`,
`test` (unit + the 90% coverage gate), `bdd`, `integration`, `mutation-fast`
(the blocking subset), `api-lint` (Spectral over `apis/openapi.yaml`), `vuln`
(govulncheck), `helm-lint` (`ct lint`), `chart-tests`, and `arch-test` on
every push (branches `main`, `develop`) and pull request. The exhaustive
`mutation` job runs only on the weekly schedule (Monday 06:00 UTC) and manual
dispatch, opening a `harness:red` issue on a scheduled failure.
`docker-publish` (GHCR, cosign-signed) and `release` (tag `vX.Y.Z` + chart
publish) run only on pushes to `main`, after every other job is green.

## Architecture Decision Records

[`docs/adr/`](docs/adr/) — 7 ADRs (0001 bounded context, 0002 assumptions,
0003 consumed contracts and local read model, 0004 CloudEvents envelope and
type catalogue, 0005 fleet observability standard, 0006 dataschema stream
segment names the actual stream, 0007 A2A inbound adapter).

## License

[MIT](LICENSE).
