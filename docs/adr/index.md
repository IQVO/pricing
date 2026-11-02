---
id: index
title: Architecture decision records
sidebar_label: ADR index
slug: /
sidebar_position: 0
---

# Architecture decision records

The decisions of `pricing`, in the order they were taken. The ADR bodies are
immutable; a changed decision gets a new ADR that supersedes the old one. The
*Status* column is copied from each ADR's own status line.

| ADR | Title | Status |
| --- | --- | --- |
| [0001](/docs/adr/0001-pricing-bounded-context) | Pricing is a bounded context of the storefront domain | Accepted (bootstrap). Design source: warehouse-docs `storefront/model/model.yaml`, context `pricing`. |
| [0002](/docs/adr/0002-pricing-assumptions) | Assumptions where the model is silent or ambiguous | Accepted (2026-10-25). |
| [0003](/docs/adr/0003-consumed-contracts-and-local-read-model) | Consumed contracts and the local CatalogItemView | Accepted (2026-10-25). |
| [0004](/docs/adr/0004-cloudevents-envelope-and-type-catalogue) | CloudEvents envelope and type catalogue | Accepted (2026-10-25). |
| [0005](/docs/adr/0005-fleet-observability-standard) | Adopt the fleet observability standard (logs, metrics, traces) | Accepted (2026-10-26). |
| [0006](/docs/adr/0006-dataschema-stream-segment-names-the-actual-stream) | The dataschema stream segment names the actual stream | Accepted (2026-10-27). Amends [ADR 0004](/docs/adr/0004-cloudevents-envelope-and-type-catalogue) in exactly one point: its blanket `dataschema` `urn:retailnetwork:pricing:events:<EventName>:v1`. |
| [0007](/docs/adr/0007-a2a-inbound-adapter) | A2A inbound adapter | Accepted (2026-10-30). Follows the fleet's Agent2Agent rollout; builds on [ADR 0004](/docs/adr/0004-cloudevents-envelope-and-type-catalogue) and [ADR 0005](/docs/adr/0005-fleet-observability-standard). |
| [0008](/docs/adr/0008-adopt-the-fleet-rest-api-guideline) | Adopt the fleet REST API guideline | Accepted (2026-11-01). Supersedes the idempotency, paging and route parts of [ADR 0002](/docs/adr/0002-pricing-assumptions) (A13, A16, A19). |
