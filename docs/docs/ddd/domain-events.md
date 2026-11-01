---
id: domain-events
title: Domain Events
sidebar_label: Domain Events
description: "Every CloudEvents type Pricing publishes and consumes, with topics, keys, payload fields, consumers, idempotency and one complete example envelope."
---

# Domain Events

Every event this context publishes and consumes, with the producer's entry for
each consumed event. Source: `apis/asyncapi.yaml`,
`internal/adapters/outbound/kafka/{encoder,encoder_data,payloads}.go`,
`internal/adapters/inbound/kafka/offer_availability.go`. The domain-wide rules are on
[Event standard: CloudEvents](https://iqvo.github.io/warehouse-docs/storefront/strategic-design/event-standard-cloudevents).

## Envelope

Every Kafka message is a **CloudEvents 1.0 event in structured JSON mode**
(`conventions.cloudevents`):

| Attribute | Value for this context |
| --- | --- |
| `specversion` | `1.0` |
| `id` | a UUID minted once when the event is written to the outbox; a redelivery carries the same `id` |
| `source` | `/retailnetwork/pricing` |
| `type` | `com.retailnetwork.storefront.pricing.<entity>.<EventName>` (tier `storefront`, entity from the `publishes` row) |
| `subject` | the aggregate root id (`catalogItemId`, `scheduleId`, `taxTableId`, `quoteId`) |
| `time` | the instant the fact happened, UTC |
| `datacontenttype` | `application/json` |
| `dataschema` | `urn:retailnetwork:pricing:<events or analytics>:<EventName>:v1`; the stream segment names the stream the event is on |
| Kafka topic | `retailnetwork.pricing.events` (`retailnetwork.pricing.analytics` for analytics-only events) |
| Kafka key | the aggregate root id, so one item's, schedule's or quote's events stay on one partition and in order |

## Published

Every event is written to `outbox_events` in the same transaction as the
aggregate save, and a relay publishes it. `PriceQuoteIssued` is
analytics-only: by `conventions.integration_rules` it goes to the
`.analytics` topic only.

### ItemPriceChanged

| Item | Value |
| --- | --- |
| Full type | `com.retailnetwork.storefront.pricing.itemprice.ItemPriceChanged` |
| Topic | `retailnetwork.pricing.events` |
| Subject / key | `catalogItemId` |
| Dataschema | `urn:retailnetwork:pricing:events:ItemPriceChanged:v1` |
| Producing command / use case | `CreateItemPrice`, `SetListPrice`, `ScheduleSale` (window already open), `CancelSale` (live sale), and the `PublishEffectivePriceOnWindowBoundary` policy at a sale boundary |
| Consumers | `product-discovery` (price fields and `purchasable` on `ProductView`, guarded by the price `version`), `shopping-cart` (`PurchasabilityView`, `EstimateView`) |

| Field | Type | Meaning |
| --- | --- | --- |
| `catalogItemId` | string | The catalogue item priced. |
| `currency` | string | The one configured currency (ISO 4217). |
| `listPriceMinor` | integer | The standing list price in minor units. |
| `effectivePriceMinor` | integer | The lowest active sale price, else the list price. |
| `saleEndsAt` | timestamp, optional | When the active sale window ends; absent at list price. |
| `version` | integer | The item price's version; consumers apply it as a monotonic guard. |

### ItemPriceWithdrawn

| Item | Value |
| --- | --- |
| Full type | `com.retailnetwork.storefront.pricing.itemprice.ItemPriceWithdrawn` |
| Topic | `retailnetwork.pricing.events` |
| Subject / key | `catalogItemId` |
| Dataschema | `urn:retailnetwork:pricing:events:ItemPriceWithdrawn:v1` |
| Producing command / use case | `WithdrawPrice`, including the `WithdrawOnDiscontinue` policy on `CatalogItemDiscontinued` |
| Consumers | `product-discovery` (item no longer purchasable), `shopping-cart` (`PurchasabilityView`) |

| Field | Type | Meaning |
| --- | --- | --- |
| `catalogItemId` | string | The item whose price is withdrawn. |
| `version` | integer | The item price's version at withdrawal. |

### DeliveryChargeScheduleChanged

| Item | Value |
| --- | --- |
| Full type | `com.retailnetwork.storefront.pricing.deliverychargeschedule.DeliveryChargeScheduleChanged` |
| Topic | `retailnetwork.pricing.events` |
| Subject / key | `scheduleId` |
| Dataschema | `urn:retailnetwork:pricing:events:DeliveryChargeScheduleChanged:v1` |
| Producing command / use case | `PublishChargeSchedule` |
| Consumers | `shopping-cart` (`EstimateView` tier charges) |

| Field | Type | Meaning |
| --- | --- | --- |
| `scheduleId` | string | The new current schedule. |
| `version` | integer | Strictly increasing schedule version. |
| `tiers[]` | array | One entry per service tier. |
| `tiers[].tier` | enum | `SAME_DAY`, `NEXT_DAY`, `TWO_DAY` or `STANDARD`. |
| `tiers[].chargeMinor` | integer | The delivery charge for the tier, minor units. |
| `tiers[].freeOverMinor` | integer, optional | Free-delivery threshold for the tier, minor units. |

### TaxTableChanged

| Item | Value |
| --- | --- |
| Full type | `com.retailnetwork.storefront.pricing.taxtable.TaxTableChanged` |
| Topic | `retailnetwork.pricing.events` |
| Subject / key | `taxTableId` |
| Dataschema | `urn:retailnetwork:pricing:events:TaxTableChanged:v1` |
| Producing command / use case | `PublishTaxTable` |
| Consumers | `shopping-cart` (`EstimateView` tax band) |

| Field | Type | Meaning |
| --- | --- | --- |
| `taxTableId` | string | The new current tax table. |
| `version` | integer | Strictly increasing table version. |
| `bands[]` | array | The postal-prefix bands. |
| `bands[].postalPrefixFrom` | string | Band start, inclusive. |
| `bands[].postalPrefixTo` | string | Band end, inclusive. |
| `bands[].rateBasisPoints` | integer | Synthetic rate in basis points (D10). |

### PriceQuoteIssued

| Item | Value |
| --- | --- |
| Full type | `com.retailnetwork.storefront.pricing.pricequote.PriceQuoteIssued` |
| Topic | `retailnetwork.pricing.analytics` (analytics only) |
| Subject / key | `quoteId` |
| Dataschema | `urn:retailnetwork:pricing:analytics:PriceQuoteIssued:v1` ([ADR 0006](/docs/adr/0006-dataschema-stream-segment-names-the-actual-stream)) |
| Producing command / use case | `IssuePriceQuote` (REST `POST /price-quotes`, called by `checkout`) |
| Consumers | analytics only |

| Field | Type | Meaning |
| --- | --- | --- |
| `quoteId` | string | The quote. |
| `cartId` | string | The cart the quote prices. |
| `currency` | string | The configured currency. |
| `totalMinor` | integer | The binding total. |
| `expiresAt` | timestamp | `issuedAt` plus the `QuoteTtl` (default 15 minutes). |

`checkout` does **not** consume `PriceQuoteIssued`: it gets the quote in the
synchronous reply to `POST /price-quotes`. No `PriceQuoteExpired` event
exists.

## Consumed

Every consumer is idempotent on the CloudEvents `id`: the id is inserted
into `processed_events` in the same transaction as the effect, and a
duplicate is acknowledged without effect. Consumers dispatch on the **full
type string**, never on a suffix. Consumer group names come from mandatory environment variables (`OFFER_AVAILABILITY_CONSUMER_GROUP`; the service refuses to start without them); the group deployed by warehouse-infra is
`pricing-offer-availability`.

### From offer-availability (`retailnetwork.offer-availability.events`, Conformist)

| Event | Full type | Fields used | What this context does |
| --- | --- | --- | --- |
| `CatalogItemCreated` | `com.retailnetwork.commerce.offer-availability.catalogitem.CatalogItemCreated` | `catalogItemId` | Adds the item to `CatalogItemView` as known and not discontinued, so `SetListPrice` and `ScheduleSale` can pass `ErrUnknownCatalogItem`. |
| `CatalogItemDiscontinued` | `com.retailnetwork.commerce.offer-availability.catalogitem.CatalogItemDiscontinued` | `catalogItemId` | Marks the item discontinued in `CatalogItemView` and runs `WithdrawOnDiscontinue`: `WithdrawPrice`, publishing `ItemPriceWithdrawn`. |

**Ordering.** The producer keys by `catalogItemId`, and pricing's view and
aggregate share that key, so create-before-discontinue holds per item. A
`CatalogItemDiscontinued` for an item pricing has never priced only updates
the view; no `ItemPriceWithdrawn` is emitted for a price that does not
exist.

## Example envelope

The most-consumed event, `ItemPriceChanged`, for an item entering a sale
(all ids synthetic):

```json
{
  "specversion": "1.0",
  "id": "7a2c9e41-5f0b-4c3d-9e6a-1b8d4f0c2a55",
  "source": "/retailnetwork/pricing",
  "type": "com.retailnetwork.storefront.pricing.itemprice.ItemPriceChanged",
  "subject": "cat-100234",
  "time": "2026-10-24T09:00:00Z",
  "datacontenttype": "application/json",
  "dataschema": "urn:retailnetwork:pricing:events:ItemPriceChanged:v1",
  "data": {
    "catalogItemId": "cat-100234",
    "currency": "BRL",
    "listPriceMinor": 12990,
    "effectivePriceMinor": 9990,
    "saleEndsAt": "2026-10-31T23:59:59Z",
    "version": 7
  }
}
```

No shopper reference appears in any pricing event: the storefront stores no
PII, and a quote names its `cartId`, never a person (`conventions.no_auth`).
