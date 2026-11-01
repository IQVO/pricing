---
id: ddd-artifacts
title: DDD artifacts
sidebar_label: DDD artifacts
description: "Index of the ddd-crew artifacts of the Pricing bounded context, one page per tool."
---

# DDD artifacts

**Pricing** (`pricing`, code `PR`, tier `storefront`, **Core**) owns what a
shopper pays: the list price and sale windows per catalogue item (`ItemPrice`),
the delivery charge per service tier (`DeliveryChargeSchedule`), a synthetic tax
table (`TaxTable`), and the binding, immutable, expiring `PriceQuote` that
`checkout` confirms against. One configured currency, integer minor units. The
item master stays in the retail network's `offer-availability` context; pricing
only references `catalogItemId`, kept current through `CatalogItemCreated` and
`CatalogItemDiscontinued`. Promotions are a later phase.

The pages below follow the [ddd-crew](https://github.com/ddd-crew) tools. Every
identifier, route, event type and table on them was checked against the code of
this repository; the business narrative is folded into the
[Introduction](/docs/overview/introduction).

## Pages

| Page | What it holds | ddd-crew tool it follows |
| --- | --- | --- |
| [Ubiquitous language](/docs/ddd/ubiquitous-language) | Terms, code identifiers, terms not to confuse | [Bounded Context Canvas](https://github.com/ddd-crew/bounded-context-canvas) (language section) |
| [Use cases](/docs/ddd/use-cases) | Every use case: trigger, inputs, invariants, errors, events | (application layer, fleet convention) |
| [Subdomain classification](/docs/ddd/subdomain-classification) | Why Core, and the neighbours' classes | (strategic design) |
| [Core Domain Chart](/docs/ddd/core-domain-chart) | Why this context is Core | [Core Domain Charts](https://github.com/ddd-crew/core-domain-charts) |
| [Bounded Context Canvas](/docs/ddd/bounded-context-canvas) | Purpose, classification, roles, inbound and outbound messages, decisions, metrics | [Bounded Context Canvas v5](https://github.com/ddd-crew/bounded-context-canvas) |
| [Context Map](/docs/ddd/context-map) | Every neighbour, direction and pattern | [Context Mapping](https://github.com/ddd-crew/context-mapping) |
| [Aggregate Design Canvas](/docs/ddd/aggregate-design-canvas) | `ItemPrice`, `DeliveryChargeSchedule`, `TaxTable`, `PriceQuote`: states, invariants, commands, events | [Aggregate Design Canvas v1.1](https://github.com/ddd-crew/aggregate-design-canvas) |
| [Domain Message Flow](/docs/ddd/domain-message-flow) | Price management, sale boundaries, quote issuing, discontinuation | [Domain Message Flow Modelling](https://github.com/ddd-crew/domain-message-flow-modelling) |
| [EventStorming](/docs/ddd/eventstorming) | Process-level boards with hotspots | [EventStorming glossary & cheat sheet](https://github.com/ddd-crew/eventstorming-glossary-cheat-sheet) |
| [Class Diagram](/docs/ddd/class-diagram) | Domain model, events and the hexagonal ports and adapters | (UML, fleet convention) |
| [Entity Relationship](/docs/ddd/entity-relationship) | The Postgres schema (migration 0001), 13 tables | (fleet convention) |
| [Sequence Diagrams](/docs/ddd/sequence-diagrams) | Adapter to Kafka for every command use case | (UML, fleet convention) |
| [Domain Events](/docs/ddd/domain-events) | Published and consumed CloudEvents, with an example envelope | [Domain Message Flow Modelling](https://github.com/ddd-crew/domain-message-flow-modelling) (message catalogue) |

## Where the design lives

- **The aggregator.** The cross-context view (storefront context map, glossary,
  model gap register) is in
  [warehouse-docs](https://iqvo.github.io/warehouse-docs/storefront/); the model
  there (`storefront/model/model.yaml`) is the design source this pack started
  from. Where the code differs from the model, these pages follow the code.
- **The contracts.** [API overview](/docs/api-reference) and the
  [event catalogue](/docs/api-reference/events).
- **The decisions.** The repository's seven [ADRs](/docs/adr), among them the
  assumptions where the model is silent
  ([0002](/docs/adr/0002-pricing-assumptions)) and the A2A adapter
  ([0007](/docs/adr/0007-a2a-inbound-adapter)).
- **Neighbours.** [offer-availability](https://iqvo.github.io/warehouse-docs/network/contexts/offer-availability)
  owns the catalogue item this context prices;
  [delivery-promise](https://iqvo.github.io/warehouse-docs/network/contexts/delivery-promise)
  owns the service tiers a `TierCharge` names.
