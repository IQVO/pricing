---
id: subdomain-classification
title: Subdomain classification
sidebar_label: Subdomain classification
description: "Why Pricing is a Core subdomain, with its neighbours' classes taken from the storefront strategic design."
---

# Subdomain classification

**Pricing is a Core subdomain.** The verdict comes from competitive advantage,
not size or difficulty: what the business must be good at here is item prices,
sale windows, the price of speed per delivery tier and a binding total at
purchase.

## Reasoning

- **Cost is the top abandonment reason.** The storefront research behind the
  model names it, so the total has to be visible early, honest and stable at
  confirmation. Pricing owns both numbers a shopper weighs, the item and its
  delivery, and the moment they stop moving (the `PriceQuote`).
- **The tier charge is where the network's promise becomes revenue.** The retail
  network promises a delivery date per service tier; this context puts a price on
  choosing a faster one (`DeliveryChargeSchedule`, `TierCharge`). Nobody else's
  schedule will do: it is this shop's pricing policy.
- **Custom-built, not commodity.** The sale-window semantics (non-overlapping,
  always below list, boundary publication), the binding quote that pins the
  price, schedule and tax-table versions, and the 15-minute expiry are this
  storefront's rules. Only the plumbing (outbox, inbox, CloudEvents) is
  commodity.
- **Moderate model complexity.** Four small aggregates, none with more than two
  states, no synchronous call to any neighbour, but time is a first-class input
  (sale boundaries, quote expiry) and correctness depends on a feed (the local
  catalogue view). The plotted position is on the
  [Core Domain Chart](/docs/ddd/core-domain-chart).
- **What would move it.** A bought tax engine would shrink `TaxTable` to an
  adapter; the tax table is synthetic by decision and is the part most likely to
  be replaced. Promotions would raise complexity inside this context before
  becoming a context of their own.

Business model: **Revenue** (the price of speed per tier). Evolution:
**Custom-built**. Aggregates: `ItemPrice`, `DeliveryChargeSchedule`, `TaxTable`,
`PriceQuote`. Tier: `storefront`.

## Neighbours

Copied from the storefront strategic design in `warehouse-docs`
([Subdomain classification](https://iqvo.github.io/warehouse-docs/storefront/strategic-design/subdomain-classification)):

| Bounded context | Classification | Business model | Evolution | Aggregates |
| --- | --- | --- | --- | --- |
| `pricing` (this context) | Core | Revenue | Custom-built | ItemPrice, DeliveryChargeSchedule, TaxTable, PriceQuote |
| `product-content` | Generic | Engagement enabler (buy a PIM in production) | Commodity | ProductContent, CategoryTree |
| `product-discovery` | Supporting | Conversion enabler (finding the right purchasable item fast) | Custom-built | (read side only) |
| `shopping-cart` | Supporting | Conversion (holding intent without losing the total-cost-up-front rule) | Custom-built | Cart |
| `checkout` | Supporting | Conversion (the purchase transaction) | Custom-built | CheckoutSession |
| `customer-payments` | Generic | Revenue collection (buy a PSP) | Commodity | PaymentAuthorization, Refund |

Count in the storefront: 3 Supporting, 2 Generic, 1 Core. The context that feeds
pricing, `offer-availability`, belongs to the retail network
([network strategic design](https://iqvo.github.io/warehouse-docs/network/strategic-design/subdomain-classification));
this page does not restate its class.

Future storefront contexts named by the model but not built: `customer-notifications`
(Generic), `promotions` (Supporting candidate, a later phase inside or beside
pricing), `identity` (Generic, deferred because there is no authentication) and
`reviews-recommendations` (Supporting candidate). The experience layer
(`storefront-bff`, `storefront-web`) and the `payment-provider-simulator` carry
no domain model and are not bounded contexts.
