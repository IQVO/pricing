---
id: events
title: Event catalogue
sidebar_label: Event catalogue
sidebar_position: 2
description: "Every Kafka event Pricing publishes and consumes: CloudEvents types, topics, keys and payload fields."
---

# Event catalogue

Every Kafka message this service produces or consumes is a CloudEvents 1.0
event in **structured mode**: the Kafka value is the whole JSON envelope and the
header `content-type` is `application/cloudevents+json; charset=UTF-8`. Envelopes
are built and decoded only in `internal/adapters/kafka/cloudevents`
([ADR 0004](/docs/adr/0004-cloudevents-envelope-and-type-catalogue)). The contract
is `apis/asyncapi.yaml` (AsyncAPI 3.0.0, version 1.0.0), kept equal to the code by
`TestEventCatalogueMatchesContract`; field-level detail and Mermaid diagrams are on
[Domain events](/docs/ddd/domain-events).

Source: `internal/adapters/outbound/kafka/{encoder,encoder_data,payloads}.go`,
`internal/adapters/inbound/kafka/offer_availability.go`, golden files in
`internal/adapters/outbound/kafka/testdata/`.

## Envelope

| Attribute | Value |
| --- | --- |
| `specversion` | `1.0` |
| `id` | UUID minted once at encode time and stored with the outbox row; stable across relay retries. Dedupe on it |
| `source` | `/retailnetwork/pricing` |
| `type` | `com.retailnetwork.storefront.pricing.<entity>.<EventName>` |
| `subject` | the aggregate id; also the Kafka key |
| `time` | the instant of the change, RFC 3339 UTC |
| `datacontenttype` | `application/json` |
| `dataschema` | `urn:retailnetwork:pricing:<events or analytics>:<EventName>:v1`; the stream segment names the topic stream ([ADR 0006](/docs/adr/0006-dataschema-stream-segment-names-the-actual-stream)) |

`data` is camelCase, amounts are integer minor units of the single configured
`currency`, optional fields are omitted when absent (never null), arrays are
always present. A breaking payload change is a new `.v2` type. Consumers dispatch
on the full `type`, ignore unknown types and dedupe on `id`. Kafka messages also
carry W3C `traceparent` headers when the raising request was traced.

## Published

| Type | Topic | Key | Raised when | `data` |
| --- | --- | --- | --- | --- |
| `com.retailnetwork.storefront.pricing.itemprice.ItemPriceChanged` | `retailnetwork.pricing.events` | `catalogItemId` | an item is first priced, and whenever the list price, the effective price or the sale end differs from the last published view: `SetListPrice`, a sale window opening or closing (`PublishEffectivePriceOnWindowBoundary`), `ScheduleSale` of an open window, `CancelSale` of the live sale | `catalogItemId`, `currency`, `listPriceMinor`, `effectivePriceMinor`, `saleEndsAt?` (only while a sale is live), `version` |
| `com.retailnetwork.storefront.pricing.itemprice.ItemPriceWithdrawn` | `retailnetwork.pricing.events` | `catalogItemId` | `WithdrawPrice` or the `WithdrawOnDiscontinue` policy | `catalogItemId`, `version` |
| `com.retailnetwork.storefront.pricing.deliverychargeschedule.DeliveryChargeScheduleChanged` | `retailnetwork.pricing.events` | `scheduleId` | `PublishChargeSchedule`; the previous schedule is `SUPERSEDED` in the same transaction | `scheduleId`, `version`, `tiers[{tier, chargeMinor, freeOverMinor?}]` |
| `com.retailnetwork.storefront.pricing.taxtable.TaxTableChanged` | `retailnetwork.pricing.events` | `taxTableId` | `PublishTaxTable`; the previous table is `SUPERSEDED` in the same transaction | `taxTableId`, `version`, `bands[{postalPrefixFrom, postalPrefixTo, rateBasisPoints}]` |
| `com.retailnetwork.storefront.pricing.pricequote.PriceQuoteIssued` | `retailnetwork.pricing.analytics` (analytics only) | `quoteId` | `IssuePriceQuote` | `quoteId`, `cartId`, `currency`, `totalMinor`, `expiresAt` |

Ordering: item events of one item share the key and carry a monotonically
increasing `version`; a consumer applies a version only above the last one it
applied. Schedule and tax-table events carry the published `version`. There is
no `PriceQuoteExpired` event and `CancelSale` has no event of its own.

## Consumed

| Type | Topic | Used for |
| --- | --- | --- |
| `com.retailnetwork.commerce.offer-availability.catalogitem.CatalogItemCreated` | `retailnetwork.offer-availability.events` | marks the item `ACTIVE` in the local catalogue view |
| `com.retailnetwork.commerce.offer-availability.catalogitem.CatalogItemDiscontinued` | `retailnetwork.offer-availability.events` | marks the item `DISCONTINUED` and withdraws its `ACTIVE` price |

Only `data.catalogItemId` is read; unknown fields are ignored and every other
type on the topic is skipped. The consumer group comes from
`OFFER_AVAILABILITY_CONSUMER_GROUP`. A message that fails is retried and then
published to `retailnetwork.offer-availability.events.dlq` with `x-dlq-*`
headers; see the [Runbook](/docs/operations/runbook#consumer-and-dead-letters).

## Dataschema per stream

`apis/asyncapi.yaml` declares the `PriceQuoteIssued` `dataschema` as
`urn:retailnetwork:pricing:analytics:PriceQuoteIssued:v1` and the other four as
`...:events:...`, and the encoder produces exactly that: the stream of each event
(`internal/adapters/outbound/kafka/encoder.go`, `buildMessage`) decides the
segment, so it always names the topic the event is on.
