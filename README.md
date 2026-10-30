# pricing

Storefront (e-commerce) domain of the retail-network organisation, bounded context **Pricing** (Core, storefront tier).

Owns what a shopper pays: the list price and sale windows per catalogue item, the delivery charge per service tier, a synthetic tax table, and the binding, immutable, expiring PriceQuote a checkout session is confirmed against. One configured currency, integer minor units; promotions are a later phase (D10).

Status: built (REST API, outbox relay, offer-availability consumer, policies). The design lives in [warehouse-docs](https://iqvo.github.io/warehouse-docs/storefront/) `storefront/model/model.yaml` (single source of truth).
This repository is part of the storefront domain: it consumes the retail network's Open Host Service routes and its
published `retailnetwork.*` events; it never calls an in-org or restricted network route.

Runtime: Go, hexagonal, real PostgreSQL and Kafka only (`DATABASE_URL`, `KAFKA_BROKERS` are mandatory), CloudEvents 1.0 on every message
(`com.retailnetwork.storefront.pricing.<entity>.<Event>`), transactional outbox.

## Disclaimer

This project is a domain-driven design study. It is not associated with, endorsed by or affiliated with any company, and it uses no company's code, data, logos or trademarks. All names, figures and examples are illustrative or synthetic.

See `docs/adr/` for decisions and `docs/before-first-release.md` for the packaging checklist.

## Run

`go run ./cmd/api` needs `DATABASE_URL`, `KAFKA_BROKERS` and `OFFER_AVAILABILITY_CONSUMER_GROUP`; optional
`PRICING_CURRENCY` (default `BRL`), `POLICY_INTERVAL`, `OUTBOX_RELAY_INTERVAL`, `HTTP_ADDR`. Contracts:
`apis/openapi.yaml`, `apis/asyncapi.yaml`. Assumptions: `docs/adr/0002-pricing-assumptions.md`.

## A2A agent (read-only)

`cmd/a2a` ([ADR 0007](docs/adr/0007-a2a-inbound-adapter.md)) is a second binary in the same image (`/app/a2a`) that
serves an Agent2Agent agent over JSON-RPC: the Agent Card is at `/.well-known/agent-card.json`, the endpoint is
mounted at `/` and `/a2a`, `GET /healthz` serves the probes. It is **read-only** and needs only `DATABASE_URL` and
the mandatory `A2A_AGENT_URL` (the absolute http(s) URL advertised on the card); optional `MIGRATIONS_DATABASE_URL`,
`A2A_ADDR` (default `:8093`). It reads and writes no Kafka topic and runs no outbox relay. The Helm chart deploys it
as `<release>-a2a` when `a2a.enabled=true` (default `false`), routed at `/api/a2a/pricing` when `a2a.route.enabled=true`.

Two skills, called as a `SendMessage` data part `{"skill": "<id>", "arguments": {...}}`; each returns what the matching
REST `GET` returns:

- `item_price`: `{catalogItemId}` returns the state, list price, effective price now and sale windows
  (`GET /item-prices/{catalogItemId}`).
- `quote_status`: `{quoteId}` returns the quote, `ISSUED` or `EXPIRED` now, with its lines, delivery charges, tax and
  total (`GET /price-quotes/{quoteId}`).

Rejections come back as an in-band RFC 7807 `problem` with the REST status class (`404` unknown id, `400` malformed
request). A2A carries delegation only; facts still leave as CloudEvents on Kafka.

## Observability

Logs, metrics and traces follow the fleet standard ([ADR 0005](docs/adr/0005-fleet-observability-standard.md)): JSON logs on stdout carry `trace_id`/`span_id`
and a `request_id`; `cmd/api` exports OTLP/gRPC to the Collector (`OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_SERVICE_NAME`, `SERVICE_VERSION`, `ENVIRONMENT`;
export never blocks, so no Collector costs telemetry, nothing else) with HTTP request duration, Go runtime metrics and the business counter
`pricing.quotes.issued` (`outcome`, `reason`); the trace continues across Kafka in the `traceparent` header of each published event and is picked up by the
consumer.
