---
id: observability
title: Observability
sidebar_label: Observability
description: "Metrics, traces, log fields, exporter variables, dashboards and alerts for Pricing's two binaries."
---

# Observability

Pricing follows the fleet observability standard
([ADR 0005](/docs/adr/0005-fleet-observability-standard)): JSON logs on stdout
correlated with traces, OTLP/gRPC export of traces and metrics to the
OpenTelemetry Collector, one business counter and the HTTP RED metrics from
`otelchi`. Export never blocks: the gRPC exporters connect lazily with no
blocking dial, so a Collector that is down or absent costs telemetry and
nothing else (exporter failures are logged at DEBUG as `opentelemetry error`).

Source: `internal/adapters/outbound/telemetry/` (`telemetry.go`,
`quote_metrics.go`, `slogotel.go`), `cmd/api/telemetry.go`,
`cmd/a2a/telemetry.go`, `internal/adapters/inbound/requestlog/requestlog.go`.

## Exporter configuration

| Variable | `cmd/api` default | `cmd/a2a` default | Meaning |
| --- | --- | --- | --- |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | `localhost:4317` | Collector OTLP/gRPC endpoint, `host:port` or URL; a bare `host:port` is turned into `http://host:port` |
| `OTEL_SERVICE_NAME` | `pricing` | `pricing-a2a` | `service.name` of spans, metrics and the Loki `app` label source |
| `SERVICE_VERSION` | `dev` | `dev` | `service.version` |
| `ENVIRONMENT` | `local` | `local` | `deployment.environment.name` |

Traces are batched; metrics are pushed every 15 seconds by a periodic reader.
Propagation is W3C `tracecontext` plus `baggage`. The chart renders the OTLP
variables only when `otel.enabled` is true (default), with endpoint
`otel-collector.observability.svc.cluster.local:4317`.

## Metric instruments

| Name | Type | Unit | Attributes | Emitted by |
| --- | --- | --- | --- | --- |
| `pricing.quotes.issued` | Int64 counter | `{quote}` | `outcome` = `issued` \| `rejected`; `reason` (only when rejected) = the problem slug, for example `item-not-priced`, `no-charge-schedule`, `no-tax-table`, `empty-quote`, `internal-error` | `IssuePriceQuote` (`internal/application/usecases/quote_metrics.go`), once per attempt; only `cmd/api` |
| `http.server.request.duration` | histogram (via `otelchimetric`) | `s` | route pattern, method (as exported by `riandyrn/otelchi/metric`) | both binaries, every non-probe HTTP request |
| `go.*` / runtime metrics | various | various | | OTel Go runtime instrumentation, started in `telemetry.Setup` |

`pricing.quotes.issued` is exposed to Prometheus as
`pricing_quotes_issued_total`. The fleet dashboard states what the series mean:
a falling `issued` share together with a rising `item-not-priced` means shoppers
cart items that have no active price (a catalogue or pricing gap); a rising
`no-charge-schedule` or `no-tax-table` means a schedule or table was never
published, so every checkout fails until one is; a rising `internal-error` means
the service itself is failing.

There is no outbox-lag gauge, no consumer-lag metric and no dead-letter
counter: those signals are log lines and SQL queries only (see
[Troubleshooting](/docs/operations/troubleshooting)).

## Spans

| Span | Kind | Created by | Notes |
| --- | --- | --- | --- |
| server span per request | server | `otelchi.Middleware` in both routers | Named after the chi route pattern (for example `/item-prices/{catalogItemId}`, via `WithChiRoutes`), so one name per route and not per item id |
| `<topic> process` | consumer | `Consumer.handleTraced` (`internal/adapters/inbound/kafka/tracing.go`) | Parented to the producer's `traceparent` header when the message carries one, else a new trace. Attributes `messaging.system=kafka`, `messaging.destination.name`, `messaging.operation.name=process`. A returned error is recorded and sets the span status |
| `a2a.skill <skill>` | internal | `answer` in `internal/adapters/inbound/a2a/skills.go` | Attribute `a2a.skill`; status `Error` when the skill answers a problem |

The trace continues across Kafka: when a use case appends events, the outbox
repository injects the active span context into each row's headers
(`traceparent`, `tracestate`, `baggage` through the global propagator), and the
relay publishes them as ordinary Kafka headers. Consumers of
`retailnetwork.pricing.events` therefore see the request that raised the event
as their parent.

## Log fields

Logs are JSON from `log/slog` on stdout; `LOG_LEVEL` sets the level. A record
written with a span-carrying context (the `*Context` slog variants) also carries
`trace_id` and `span_id` (`telemetry.WithTraceContext`).

Access log (`requestlog.RequestLogger`, `cmd/api` only), message `http request`:

| Field | Meaning |
| --- | --- |
| `method` | HTTP method |
| `route` | matched chi pattern, `unmatched` when none; never the raw path |
| `status` | response status (5xx logs at ERROR, 4xx at WARN, otherwise INFO) |
| `duration_ms` | handling time |
| `bytes` | response bytes |
| `request_id` | chi `RequestID` |

`/healthz` and `/readyz` are not logged. Other fields in use:
`consumer`, `topic`, `group_id`, `brokers` (consumer start), `partition`,
`offset`, `attempt`, `max_attempts`, `retry_in` (consumer retries),
`event_id`, `type` (dead-lettered payloads), `outbox_id`, `event_type`, `key`,
`attempts`, `max_attempts` (relay attempt cap), `policy`, `handled` (policy
passes), `catalog_item_id`, `quote_id` (skipped policy items), `op`, `attempt`,
`in`, `err` (boot retry).

Key messages, in boot order for `cmd/api`: `retrying` / `succeeded after retry`
(`op` = `run migrations` or `ping postgres`), `postgres adapters configured`,
`telemetry configured`, `consumer running`, `outbox relay running`
(`interval`, `max_attempts`, `topic`), `policies running` (`interval`),
`http server listening`. At shutdown: `shutdown: readiness flipped to
not-ready; waiting for traffic to drain`. Warnings and errors worth alerting on:

| Message | Level | Meaning |
| --- | --- | --- |
| `outbox relay pass failed` | ERROR | A relay pass failed; rows stay unpublished and retry after the interval |
| `outbox relay: row reached the attempt cap and is excluded from future claims; re-arm it with attempts = 0 after fixing the cause` | ERROR | A poison outbox row was parked |
| `outbox relay stopped` / `consumer stopped` | ERROR | The worker returned; the process exits non-zero |
| `<consumer> handling failed; retrying the same message` | ERROR | Transient consumer failure, with `attempt` and `retry_in` |
| `<consumer> handling failed after the maximum attempts; dead-lettering the message` | ERROR | Five attempts used |
| `<consumer> message can never be applied; dead-lettering it without retry` | ERROR | Poison payload |
| `<consumer> broker connection stalled or lost; fetching again after the backoff` | WARN | Transient fetch failure (up to 10 in a row) |
| `skipping a message that is not a valid CloudEvent` | WARN | Committed past |
| `policy pass failed` | ERROR | A clock-driven pass failed; retried on the next tick |
| `boundary publication skipped` / `quote expiry skipped` | WARN | A concurrent writer got in; the next pass sees the item again |
| `invalid OUTBOX_RELAY_INTERVAL; using the default` and the sibling `POLICY_INTERVAL`, `OUTBOX_MAX_ATTEMPTS`, `SHUTDOWN_DRAIN_DELAY` messages | WARN | A malformed tuning value |
| `telemetry flush failed on shutdown` | WARN | The final export (bounded to 5 s) failed |
| `a2a skill failed` | ERROR | A skill hit an unexpected error; the answer is a generic 500 problem |

`<consumer>` is `offer-availability`.

## Dashboards and alerts

Seen in `warehouse-infra` (branch `develop`):

- **Dashboard** `warehouse-pricing` (`terraform/dashboards/contexts/pricing.json`,
  generated by `scripts/gen-context-dashboards.py`): Kong rows for the context's
  routes, request rate by route and p95 duration for `service_name=~"pricing.*"`,
  the `pricing_quotes_issued_total` panel by `outcome`, Go runtime, and the
  Loki log panels for `app="pricing"`. The Kong rows stay empty until the chart
  attaches a Kong prometheus plugin.
- **Alerts** (`terraform/alerting/warehouse-alert-rules.json`, groups
  `warehouse-pricing` and `warehouse-pricing-slo`): `WarehouseKong5xxRatioHigh`
  (over 5% 5xx for 5 minutes), `WarehousePodRestarting` (two restarts in 15
  minutes for pods matching `pricing-.+`) and the four SLO burn-rate alerts
  `WarehouseApiAvailabilityBurnFast`, `WarehouseApiAvailabilityBurnSlow`,
  `WarehouseApiLatencyBurnFast` and `WarehouseApiLatencyBurnSlow`.
- No `WarehouseOutboxLagHigh` rule exists for Pricing because the service
  exports no outbox-lag gauge. The alert annotations link
  `https://github.com/IQVO/pricing/blob/develop/docs/operations/runbook.md`,
  a path that no longer exists now that the runbook lives in this site
  (see [Runbook](/docs/operations/runbook)); the link needs updating in
  warehouse-infra.
