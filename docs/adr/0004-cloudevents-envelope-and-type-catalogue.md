# ADR 0004: CloudEvents envelope and type catalogue

## Status

Accepted (2026-10-25).

## Context

Every Kafka message in the retail network is a CloudEvents 1.0 event in structured
mode (the repo rule `.claude/rules/fleet/cloudevents.md`). This ADR is the catalogue
of every type pricing publishes; `TestEventCatalogueMatchesContract` checks it against
`apis/asyncapi.yaml`. The consumed types are listed in ADR 0003.

## Decision

### Envelope

- `specversion` `1.0`; `id` a UUID persisted with the outbox row (stable across relay
  retries; consumers dedupe on it); `source` `/retailnetwork/pricing`; `subject` the
  aggregate root id; `time` the commit instant in UTC; `datacontenttype`
  `application/json`; `dataschema` `urn:retailnetwork:pricing:events:<EventName>:v1`.
- Kafka value = the JSON event format; header
  `content-type: application/cloudevents+json; charset=UTF-8`; Kafka key = the subject.
- `data` is camelCase; optional fields are omitted when unset; amounts are integer
  minor units; timestamps are RFC 3339 UTC.
- `<entity>` is the raising aggregate, lowercase: `itemprice`, `deliverychargeschedule`,
  `taxtable`, `pricequote`. Tier is `storefront`.

### Published types

```text
type: com.retailnetwork.storefront.pricing.itemprice.ItemPriceChanged stream: events
type: com.retailnetwork.storefront.pricing.itemprice.ItemPriceWithdrawn stream: events
type: com.retailnetwork.storefront.pricing.deliverychargeschedule.DeliveryChargeScheduleChanged stream: events
type: com.retailnetwork.storefront.pricing.taxtable.TaxTableChanged stream: events
type: com.retailnetwork.storefront.pricing.pricequote.PriceQuoteIssued stream: analytics
```

| Event | Topic | Subject / key | `data` |
|---|---|---|---|
| `ItemPriceChanged` | `retailnetwork.pricing.events` | `catalogItemId` | `{catalogItemId, currency, listPriceMinor, effectivePriceMinor, saleEndsAt?, version}` |
| `ItemPriceWithdrawn` | `retailnetwork.pricing.events` | `catalogItemId` | `{catalogItemId, version}` |
| `DeliveryChargeScheduleChanged` | `retailnetwork.pricing.events` | `scheduleId` | `{scheduleId, version, tiers[{tier, chargeMinor, freeOverMinor?}]}` |
| `TaxTableChanged` | `retailnetwork.pricing.events` | `taxTableId` | `{taxTableId, version, bands[{postalPrefixFrom, postalPrefixTo, rateBasisPoints}]}` |
| `PriceQuoteIssued` | `retailnetwork.pricing.analytics` | `quoteId` | `{quoteId, cartId, currency, totalMinor, expiresAt}` |

Payload rules:

- `version` increases for every change of one item, so a consumer applies a version
  only above the last one it applied.
- `ItemPriceChanged` is the only price fact: a cancelled sale, a sale starting or
  ending and a list-price change all raise it. There is no PriceQuoteExpired event.
- A breaking payload change is a new `.v2` type and dataschema, never a mutation of v1.

## Consequences

Adding a published type requires a line here in the same commit as its asyncapi
message, or the architecture fitness test fails.
