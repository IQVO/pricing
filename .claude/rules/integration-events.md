---
paths:
  - "internal/adapters/**/kafka/**"
  - "internal/adapters/outbound/events/**"
  - "apis/asyncapi*"
---

# Cross-service integration events (Kafka)

This service PUBLISHES five events (four on `retailnetwork.pricing.events`, the
analytics-only `PriceQuoteIssued` on `retailnetwork.pricing.analytics`) through the
transactional outbox, and CONSUMES two events from
`retailnetwork.offer-availability.events`. No `warehouse.*` topic is read or written.

## Events: CloudEvents 1.0 is MANDATORY

Every Kafka message this service produces or consumes (integration
`warehouse.<ctx>.events` AND analytics `warehouse.<ctx>.analytics`) is a
CloudEvents 1.0 event in structured content mode. This is a hard fleet rule,
not a preference — there is nothing to "choose" here:

- No flat envelope (`event_id`/`event_type`/`occurred_at`), no dual-write,
  no dual-read, no envelope toggle env var (`EVENT_ENVELOPE_MODE` is gone
  fleet-wide). `internal/architecture/fitness_test.go`'s
  `TestNoEventEnvelopeToggleOrFlatEnvelope` fails CI on any of them.
- Build/validate/(un)marshal with `github.com/cloudevents/sdk-go/v2/event`
  (latest v2) via ONE helper package,
  `internal/adapters/kafka/cloudevents/` — copy it from this template's
  `templates/cloudevents/cloudevents.go.tmpl` (+ its test) and change only
  the per-repo constants. Transport stays `segmentio/kafka-go` (no sdk-go
  protocol/client packages, no hand-rolled CloudEvent structs).
- Every produced message carries the Kafka header
  `content-type: application/cloudevents+json; charset=UTF-8`
  (`cloudevents.ContentTypeHeader()`), next to the W3C trace headers
  (`traceparent`/`tracestate` stay in headers, never duplicated into
  extension attributes). Message key = aggregate id, `kafkago.Hash{}`
  balancer.
- Required attributes: `specversion=1.0`; `id` (UUID v4 minted ONCE per
  domain event and persisted with the outbox row, so redelivery carries
  the same id); `source=/warehouse/<repo>`; `type`; `subject` (aggregate
  instance id, never empty); `time` (domain occurred-at, UTC);
  `datacontenttype=application/json`;
  `dataschema=urn:warehouse:<repo>:<events|analytics>:<EventName>:v<N>`.
  No custom extension attributes without an ADR.
- `type` = `com.warehouse.<subdomain>.<bounded-context>.<entity>.<EventName>`
  (this repo's exact prefix: `com.retailnetwork.storefront.pricing`, so
  `com.retailnetwork.storefront.pricing.itemprice.ItemPriceChanged`; the
  storefront namespace replaces the `com.warehouse.*` pattern, and `source` is
  `/retailnetwork/pricing`, `dataschema` `urn:retailnetwork:pricing:events:<EventName>:v1`). The SAME `type` names the
  occurrence on both the integration and the analytics topic; `dataschema`
  names the payload shape. Breaking payload change => new `.v2` type + new
  dataschema version, never mutate an existing one.
- Consumers decode with `cloudevents.Decode` (validates), dispatch on the
  FULL `type` string (never a short name or suffix match), ignore unknown
  types, read `time`/`subject` from attributes and the payload via
  `DataAs`, dedupe on `id`, and DLQ/skip — WARN log + commit past, never
  crash, never block the partition, never fall back to parsing a legacy
  shape — anything that fails CloudEvents validation.
- Tests: a golden exact-JSON test per published `type` (all attributes +
  the `content-type` header); a legacy-flat-message-rejected test per
  consumer; Kafka integration tests via testcontainers only.

Full standard, subdomain table and the fleet's cross-service type
catalogue: `.claude/rules/fleet/cloudevents.md`.
Record it in this repo as its own ADR "CloudEvents 1.0 as the mandatory
event envelope" under `docs/adr/`.

### Published types

| `type` | topic | `subject` | `dataschema` |
| --- | --- | --- | --- |
| `com.retailnetwork.storefront.pricing.itemprice.ItemPriceChanged` | `retailnetwork.pricing.events` | catalogItemId | `urn:retailnetwork:pricing:events:ItemPriceChanged:v1` |
| `com.retailnetwork.storefront.pricing.itemprice.ItemPriceWithdrawn` | `retailnetwork.pricing.events` | catalogItemId | `urn:retailnetwork:pricing:events:ItemPriceWithdrawn:v1` |
| `com.retailnetwork.storefront.pricing.deliverychargeschedule.DeliveryChargeScheduleChanged` | `retailnetwork.pricing.events` | scheduleId | `urn:retailnetwork:pricing:events:DeliveryChargeScheduleChanged:v1` |
| `com.retailnetwork.storefront.pricing.taxtable.TaxTableChanged` | `retailnetwork.pricing.events` | taxTableId | `urn:retailnetwork:pricing:events:TaxTableChanged:v1` |
| `com.retailnetwork.storefront.pricing.pricequote.PriceQuoteIssued` | `retailnetwork.pricing.analytics` | quoteId | `urn:retailnetwork:pricing:events:PriceQuoteIssued:v1` |

Catalogue and payloads: `docs/adr/0004-cloudevents-envelope-and-type-catalogue.md`
and `apis/asyncapi.yaml`. Encoding lives in `internal/adapters/outbound/kafka`
(golden files in `testdata/`).

### Consumed types

| `type` | topic | producer |
| --- | --- | --- |
| `com.retailnetwork.commerce.offer-availability.catalogitem.CatalogItemCreated` | `retailnetwork.offer-availability.events` | offer-availability |
| `com.retailnetwork.commerce.offer-availability.catalogitem.CatalogItemDiscontinued` | `retailnetwork.offer-availability.events` | offer-availability |

Consumer: `internal/adapters/inbound/kafka` (inbox on the CloudEvents id, DLQ for
poison payloads). See `docs/adr/0003-consumed-contracts-and-local-read-model.md`.

## Consumer group id

The group id comes from the mandatory variable `OFFER_AVAILABILITY_CONSUMER_GROUP`
(boot error naming it when unset); it is never a literal at the call site. The
consumer resumes from its committed offset, so one stable group per deployment.
