# ADR 0010: Accept the CONSOLIDATED delivery tier as a free tier

## Status

Accepted (2026-11-18, user decision: accept CONSOLIDATED with a charge policy of FREE, "decided
later"). Extends the delivery-charge model of [ADR 0002](./0002-pricing-assumptions.md) (A6, A10,
A11) and keeps [ADR 0004](./0004-cloudevents-envelope-and-type-catalogue.md) (event envelope)
unchanged.

## Context

delivery-promise defines a fifth service tier, `CONSOLIDATED` (opt-in: the customer waits for a
consolidated shipment). shopping-cart and checkout already accept it and forward the tier verbatim in
the `groups[].tier` of `POST /price-quotes`. pricing named only `SAME_DAY`, `NEXT_DAY`, `TWO_DAY` and
`STANDARD`: the request enum rejected it (422 `validation-failed`), so a checkout session holding a
CONSOLIDATED offer could not be priced, and shopping-cart deliberately refuses a
`DeliveryChargeScheduleChanged` row for a tier it does not know.

The tier set sits in four places: the `shared.Tier` value object; the `DeliveryChargeSchedule`
aggregate, whose schedule must name a charge for every tier (`ErrIncompleteChargeSchedule`); the
`tier` CHECK constraints of `tier_charges` and `quote_groups`; and the published contracts (the
OpenAPI `Tier` schema, the AsyncAPI `DeliveryChargeScheduleChanged` tier enum, and the `tier` strings
of the MCP and A2A quote views).

Alternatives weighed:

1. **Make every schedule name CONSOLIDATED** (a required fifth row). Rejected: it turns every
   existing `POST /delivery-charge-schedules` body into a 422 and every published
   `DeliveryChargeScheduleChanged` into a different payload.
2. **Seed a CONSOLIDATED row in the database.** Rejected: pricing's rule lives in the domain, not in
   data; a seeded row would also differ per environment.
3. **Treat CONSOLIDATED as an alias of a classic tier.** Rejected: it hides which tier was quoted.
4. **Known tier, free unless the schedule says otherwise** (chosen).

## Decision

- **CONSOLIDATED is a known tier.** `shared.TierConsolidated` is valid wherever a tier is accepted
  (quote request groups, a schedule row, a stored quote group). Any other value stays rejected
  (422 `validation-failed`); the match is exact, so `consolidated` is unknown.
- **Its delivery charge is zero unless the schedule declares a row for it.** `shared.Tiers()` keeps
  returning the four classic tiers, which a schedule must still name
  (`ErrIncompleteChargeSchedule`). A CONSOLIDATED row is optional. `ChargeFor(CONSOLIDATED, goods)`
  with no declared row returns 0 in the schedule's currency, computed in the domain. Nothing is
  seeded or synthesised: an existing schedule, its snapshot, its `GET` body and its
  `DeliveryChargeScheduleChanged` payload are byte-identical, and so is every existing quote.
- **A declared row is honoured.** `POST /delivery-charge-schedules` accepts a fifth row
  `{"tier":"CONSOLIDATED","charge":...,"freeOver":...}`; it follows every rule of the other rows
  (non-negative, the schedule currency, no duplicate) and a quote group uses its charge and its
  free-over threshold exactly like a classic tier. It is stored, served and published last (after
  `STANDARD`). This is how the "decided later" charge policy gets set, with no code change.
- **Contracts name the tier.** OpenAPI `Tier` and the AsyncAPI `tier` enum gain `CONSOLIDATED`; the
  schedule request `tiers` array allows 5 items. The MCP and A2A quote views carry the tier as a plain
  string, so their schemas do not change. The Postgres CHECKs are widened by migration `0002`
  (additive; its down migration restores the four-tier CHECKs and fails loudly while a CONSOLIDATED
  row exists, it deletes nothing).
- **Money is unchanged**: integer minor units, one currency per schedule.
- **Spec-compatibility.** On the shared `Tier` schema the new value is a REQUEST-side widening (a
  consumer that sends it was refused before; one that does not is unaffected) and a RESPONSE-side
  addition (`response-property-enum-value-added`, a warning in the `spec-breaking` gate by the
  fleet's decision: clients must tolerate unknown enum values). The AsyncAPI addition is on a
  published event payload and only appears when a schedule declares the row.

## Consequences

- A checkout session with a CONSOLIDATED offer can be priced: the group's charge is 0 and tax covers
  goods plus the other groups' delivery as before.
- A response or event may now carry `CONSOLIDATED`. Consumers that pin the OpenAPI `Tier` (checkout,
  storefront-bff, product-discovery) must refresh their pin after merge; shopping-cart, which reads
  `DeliveryChargeScheduleChanged` from the topic, can stop refusing a CONSOLIDATED row (it will only
  see one once a schedule declares it).
- A later charge policy is a data change (publish a schedule with a CONSOLIDATED row), not a release.
- The existing "unknown tier" message now lists CONSOLIDATED; the problem slug and status are unchanged.
