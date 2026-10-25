# 1. Pricing is a bounded context of the storefront domain

Status: Accepted (bootstrap). Design source: warehouse-docs `storefront/model/model.yaml`, context `pricing`.

## Context

The storefront is the e-commerce domain of the retail-network organisation: six bounded contexts, each its own
service and database. Owns what a shopper pays: the list price and sale windows per catalogue item, the delivery charge per service tier, a synthetic tax table, and the binding, immutable, expiring PriceQuote a checkout session is confirmed against. One configured currency, integer minor units; promotions are a later phase (D10).

## Decision

- This repo owns the aggregates ItemPrice, DeliveryChargeSchedule, TaxTable, PriceQuote; the invariants and error names are those of the model.
- Business decisions of the context:
- A sale price is always below the list price; sale windows of one item never overlap.
- Exactly one delivery charge schedule and one tax table are current; a new version supersedes, never edits.
- A PriceQuote is binding, immutable and expiring (policy QuoteTtl, default 15 min): the review page shows the binding total, and a changed total is visible before confirm (gap S2).
- The item master stays in offer-availability; pricing only references catalogItemId.
- Events use the storefront CloudEvents namespace `com.retailnetwork.storefront.pricing.<entity>.<Event>` on `retailnetwork.pricing.events`
  (analytics-only events on `.analytics`). Published: ItemPriceChanged, ItemPriceWithdrawn, DeliveryChargeScheduleChanged, TaxTableChanged, PriceQuoteIssued. Consumed: CatalogItemCreated, CatalogItemDiscontinued. No `warehouse.*` topic is consumed or produced.
- The storefront calls only the network's Open Host Service routes (model inbound rows for `shopper / storefront`),
  never an in-org or restricted route; sibling data used in a decision is a local event-fed read model, except the
  calls the model lists under `sync_calls`.
- No authentication on REST, consistent with the fleet and the network. This context stores no PII and no card data.

## Consequences

Relationships and the full consumed-event list are in the model and in the AsyncAPI contract added with the first feature PR.
