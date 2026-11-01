---
id: quickstart
title: Quickstart
sidebar_label: Quickstart
sidebar_position: 3
description: "Build, test and run Pricing locally on Postgres and Kafka, then make the first REST and A2A calls."
---

# Quickstart

Build the service, run it against Postgres and Kafka, publish a price and issue
a quote. Everything below is derived from the code on this branch
(`cmd/api`, `cmd/a2a`, `internal/adapters/inbound/http`, the embedded
migration); the JSON bodies are the request shapes of `apis/openapi.yaml`.
**No response on this page was captured from a live run**: the example
responses are the shapes the DTOs in `internal/adapters/inbound/http/dto.go`
serialise, with illustrative values.

## Prerequisites

| Tool | Version | Needed for |
| --- | --- | --- |
| Go | `1.27.3` (`go.mod`) | everything |
| `make` | any | the targets below |
| Docker | any recent | the Postgres container below and `make integration` (testcontainers) |
| golangci-lint | `v2.14.0` (`GOLANGCI_VERSION` in the `Makefile`, pinned in CI) | `make lint`, `make check` |
| gremlins | `v0.6.0` (`GREMLINS_VERSION`) | `make mutation`, `make mutation-full` |
| govulncheck | latest | `make vuln` |
| Node 20 | | building this documentation site (`docs/`) |
| lefthook | optional | `lefthook install` wires `fmt-check vet lint` at pre-commit and `make check` at pre-push |

The fleet runs **one** Kafka broker, reachable at `localhost:9092` from the
kind cluster's external access; the docker-compose Kafka is retired and this
repository ships no compose file. The service needs Postgres **and** Kafka to
boot and to be ready: there is no in-memory or permissive mode.

## Build, test, run

```bash
make help          # list the targets
make build         # go build ./...
make test          # go test ./... -race
make arch-test     # go test ./internal/architecture/... -v
make bdd           # go test ./... -run TestFeatures -v
make coverage      # tests with -race plus the 90% gate over domain + application
make check         # fmt-check vet build lint test
make check-all     # check + coverage + arch-test + bdd
make integration   # go test -tags=integration ./... -race -count=1 (needs Docker)
```

Start the binary with nothing configured and it refuses to boot, before it
opens a connection or binds the port. `loadRuntime` returns the first missing
variable (`DATABASE_URL`, then `KAFKA_BROKERS`, then
`OFFER_AVAILABILITY_CONSUMER_GROUP`) and `main` logs it as
`service exited with error`, exit status 1:

```bash
go build -o pricing ./cmd/api
./pricing
# DATABASE_URL is required: this context runs on real Postgres, there is no in-memory mode
```

## Run on Postgres and Kafka

Start a throwaway database and point the service at your Kafka. The service
never creates topics through an admin API: the broker auto-creates
`retailnetwork.pricing.events` and `retailnetwork.pricing.analytics` on the
relay's first write, and the consumed `retailnetwork.offer-availability.events`
exists once its producer published.

```bash
docker run -d --rm --name pricing-pg -e POSTGRES_PASSWORD=pw -e POSTGRES_DB=pricing \
  -p 127.0.0.1:55432:5432 postgres:16-alpine

DATABASE_URL='postgres://postgres:pw@127.0.0.1:55432/pricing?sslmode=disable' \
KAFKA_BROKERS=localhost:9092 \
OFFER_AVAILABILITY_CONSUMER_GROUP=pricing-offer-availability-dev \
HTTP_ADDR=127.0.0.1:18080 ./pricing
```

Migrations run at boot (`0001_pricing_schema`, embedded in
`internal/adapters/outbound/postgres/migrations`). The startup log is JSON on
stdout: `postgres adapters configured`, `telemetry configured`, `consumer
running` (with `topic`, `group_id`, `brokers`), `outbox relay running`,
`policies running` and `http server listening`. The
[Observability](/docs/operations/observability) page lists every message.

`GET /readyz` is `200` only while Postgres answers a ping **and** a Kafka
broker answers a metadata request (cached 3 s, bounded 2 s). With the broker
unreachable the process stays up and live (`/healthz` is `200`) but `/readyz`
is `503`, and the consumer and relay log their failures and retry.

Stop it with Ctrl-C: `/readyz` flips to `503`, the process waits
`SHUTDOWN_DRAIN_DELAY` (5 s), drains HTTP, stops the consumer and the policy
loop, then the relay, then closes the pool.

## First calls

REST is unauthenticated. Every `POST` takes an `Idempotency-Key` header of 1 to
128 characters (`400 idempotency-key-required` without it). Probes first:

```bash
curl -s localhost:18080/healthz
curl -s localhost:18080/readyz
```

A price can only be created for an item the local `catalog_items` view knows
(`422 unknown-catalog-item` otherwise). In a live cluster the view is filled by
`CatalogItemCreated` events from offer-availability. For a local look you can
insert the row by hand; this is a development shortcut, not a supported
interface:

```bash
docker exec pricing-pg psql -U postgres -d pricing \
  -c "INSERT INTO catalog_items (catalog_item_id, state) VALUES ('sku-1', 'ACTIVE')"
```

Publish a list price (minor units, in the configured currency, `BRL` by
default), then read it back:

```bash
curl -si -X POST localhost:18080/item-prices \
  -H 'Content-Type: application/json' -H 'Idempotency-Key: demo-price-1' \
  -d '{"catalogItemId":"sku-1","listPrice":{"amountMinor":10000,"currency":"BRL"}}'

curl -s localhost:18080/item-prices/sku-1
```

The response is an `itemPriceDTO`: `catalogItemId`, `state` (`ACTIVE`), `listPrice`,
`effectivePrice`, `sales` (always an array) and `version`. Repeating the `POST`
with the same key and body replays the stored answer with an
`Idempotent-Replayed: true` header; the same key with another body is
`422 idempotency-key-reused`.

A quote needs a current delivery charge schedule (a charge for **every** tier:
`SAME_DAY`, `NEXT_DAY`, `TWO_DAY`, `STANDARD`) and a current tax table:

```bash
curl -s -X POST localhost:18080/delivery-charge-schedules \
  -H 'Content-Type: application/json' -H 'Idempotency-Key: demo-sched-1' \
  -d '{"version":1,"tiers":[
        {"tier":"SAME_DAY","charge":{"amountMinor":2500,"currency":"BRL"}},
        {"tier":"NEXT_DAY","charge":{"amountMinor":1500,"currency":"BRL"}},
        {"tier":"TWO_DAY","charge":{"amountMinor":1000,"currency":"BRL"}},
        {"tier":"STANDARD","charge":{"amountMinor":500,"currency":"BRL"},
         "freeOver":{"amountMinor":20000,"currency":"BRL"}}]}'

curl -s -X POST localhost:18080/tax-tables \
  -H 'Content-Type: application/json' -H 'Idempotency-Key: demo-tax-1' \
  -d '{"version":1,"bands":[{"postalPrefixFrom":"000","postalPrefixTo":"999","rateBasisPoints":1000}]}'
```

Then issue the quote the way checkout does, with the lines and shipment groups
the session froze:

```bash
curl -s -X POST localhost:18080/price-quotes \
  -H 'Content-Type: application/json' -H 'Idempotency-Key: demo-quote-1' \
  -d '{"cartId":"cart-1","destinationPostalPrefix":"010",
       "lines":[{"catalogItemId":"sku-1","quantity":2}],
       "groups":[{"groupNo":1,"offerId":"offer-1","tier":"STANDARD","lineNos":[1]}]}'
```

The answer is a `priceQuoteDTO`: `quoteId`, `cartId`, `state` (`ISSUED`),
`currency`, `lines` (`unitPrice`, `lineTotal`), `groups` (`deliveryCharge`),
`taxMinor`, `totalMinor`, `versions`, `issuedAt` and `expiresAt` (15 minutes
after issue). Fetch it again with `GET /price-quotes/{quoteId}`; after
`expiresAt` it reads `EXPIRED`, whether or not the policy loop stored that yet.

Errors are RFC 9457 `application/problem+json` with `type` `/problems/<slug>`.
For instance a quote request for an item without an active price is
`422 item-not-priced`, and one issued before any schedule or tax table was
published is `422 no-charge-schedule` or `422 no-tax-table`. See
[Troubleshooting](/docs/operations/troubleshooting#rest-problem-types).

Look at the outbox:

```bash
docker exec pricing-pg psql -U postgres -d pricing -At \
  -c "select id, topic, event_type, subject, attempts, published_at is not null from outbox_events"
```

Expect rows for `ItemPriceChanged`, `DeliveryChargeScheduleChanged`,
`TaxTableChanged` on `retailnetwork.pricing.events` and `PriceQuoteIssued` on
`retailnetwork.pricing.analytics`. The relay publishes them within
`OUTBOX_RELAY_INTERVAL` (1 s by default) and sets `published_at`; while Kafka is
unreachable `attempts` grows by one per failed pass and the row is excluded
from claims at `OUTBOX_MAX_ATTEMPTS` (20). See the
[Runbook](/docs/operations/runbook#outbox-relay).

## The A2A agent

`cmd/a2a` is a second, read-only binary. It needs only Postgres and the URL it
advertises:

```bash
go build -o pricing-a2a ./cmd/a2a
DATABASE_URL='postgres://postgres:pw@127.0.0.1:55432/pricing?sslmode=disable' \
A2A_AGENT_URL=http://localhost:8093/ ./pricing-a2a

curl -s localhost:8093/healthz
curl -s localhost:8093/.well-known/agent-card.json
```

A skill call is a JSON-RPC 2.0 `SendMessage` whose first message part carries
`{"skill": "<id>", "arguments": {...}}` (a data part, or a text part holding
the same JSON). The SDK method name and the `ROLE_USER` role are those of
`a2aproject/a2a-go` v2; confirm against the Agent Card of the running binary:

```bash
curl -s localhost:8093/ -H 'Content-Type: application/json' -d '{
  "jsonrpc":"2.0","id":1,"method":"SendMessage",
  "params":{"message":{"messageId":"m-1","role":"ROLE_USER",
    "parts":[{"text":"{\"skill\":\"item_price\",\"arguments\":{\"catalogItemId\":\"sku-1\"}}"}]}}}'
```

The agent answers in the message envelope; a domain rejection (unknown id) is
an in-band RFC 7807 `problem` with status `404`, never a JSON-RPC error.

## Cleaning up

Stop the processes with Ctrl-C and remove the container with
`docker rm -f pricing-pg`.

## Building these docs

```bash
cd docs
npm ci
npm run build          # also fails on broken links
npm run gen-api-docs   # regenerate docs/api-reference/rest from apis/openapi.yaml
```

The REST reference pages are generated from `apis/openapi.yaml`; run the
generator after any spec change and commit the result, because `sidebars.ts`
imports the generated `sidebar.ts`.
