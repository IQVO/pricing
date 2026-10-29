# ADR 0003: Consumed contracts and the local CatalogItemView

## Status

Accepted (2026-10-25).

## Context

Pricing must know whether a catalogue item exists and is not discontinued before it
prices it, without calling offer-availability.

## Decision

- Pricing consumes two events from `retailnetwork.offer-availability.events`, under
  the consumer group in `OFFER_AVAILABILITY_CONSUMER_GROUP` (mandatory):
  - `com.retailnetwork.commerce.offer-availability.catalogitem.CatalogItemCreated`
  - `com.retailnetwork.commerce.offer-availability.catalogitem.CatalogItemDiscontinued`

  Only `data.catalogItemId` is used; other fields are ignored.
- They feed the local CatalogItemView. `CatalogItemDiscontinued` also runs the policy
  WithdrawOnDiscontinue: an ACTIVE price of the item is withdrawn and
  `ItemPriceWithdrawn` published, in the same transaction as the view update.
- Consumers claim the CloudEvents `id` in an inbox inside the unit of work, dispatch on
  the full `type` string and ignore unknown types and flat legacy messages.
- A payload that can never be applied (an invalid id, a wrong shape) goes to the
  dead-letter topic and the offset is committed; a transient failure is retried.
- Pricing never calls a sibling over HTTP.

## Consequences

Creating a price for an item the consumer has not yet seen is refused with
`unknown-catalog-item`; clients retry after the event is projected.
