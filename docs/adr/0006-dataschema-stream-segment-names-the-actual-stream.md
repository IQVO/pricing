# ADR 0006: The dataschema stream segment names the actual stream

## Status

Accepted (2026-10-27). Amends [ADR 0004](./0004-cloudevents-envelope-and-type-catalogue.md)'s
envelope rule in exactly one point: its blanket `dataschema
urn:retailnetwork:pricing:events:<EventName>:v1`.

## Context

ADR 0004 pinned `dataschema` to the `events` segment for every event. The fleet event
standard (warehouse-systems CloudEvents standard and every other repo, checkout and
shopping-cart included) uses the segment to distinguish the integration payload from the
analytics payload of the same occurrence: `events` on the `.events` topic, `analytics` on
the `.analytics` topic. `buildMessage` stamped `StreamEvents` even when `payloadFor`
routed the event to the analytics topic, so `PriceQuoteIssued` — the only analytics-only
event — carried `urn:retailnetwork:pricing:events:PriceQuoteIssued:v1` on
`retailnetwork.pricing.analytics`. The stream segment then lied about the topic, and the
dataschema of a future integration copy of the same event could not be told apart.

## Decision

The dataschema stream segment names the actual stream: `buildMessage` takes the stream
`payloadFor` returns and stamps `Stream` and `dataschema` from it. `PriceQuoteIssued`
carries `urn:retailnetwork:pricing:analytics:PriceQuoteIssued:v1`; the four integration
events are unchanged. Wire-visible but safe: the event is analytics-only, no consumer
exists, and the version stays `v1` (same shape).

## Consequences

- `apis/asyncapi.yaml` and the `.claude/rules/integration-events.md` catalogue name the
  analytics urn for `PriceQuoteIssued`.
- The golden test derives the expected segment from the topic.
