# ADR 0005: Adopt the fleet observability standard (logs, metrics, traces)

## Status

Accepted (2026-10-26).

## Context

pricing logged JSON with `log/slog` and nothing else: no trace or span id on a log
line, no request id, no request log, no metrics, no traces and no `traceparent` on
the events it publishes. The warehouse-systems services (inventory-storage,
wes-work-planning) already share one pipeline and one convention, recorded in their
ADRs "standard metrics convention" (inventory-storage ADR 0016, wes-work-planning
ADR 0013), and customer-ordering adopted it in its ADR 0007:

- pods export OTLP/gRPC to the in-cluster Collector
  (`otel-collector.observability.svc.cluster.local:4317`), which feeds Jaeger
  (traces) and a Prometheus exporter (metrics);
- logs stay on stdout as JSON for Alloy and Loki.

Without the same wiring a price quote could not be followed from the REST call
through the Kafka event to the consumer in another service, a log line could not be
joined to its trace, and no dashboard could show quote rate, errors or latency. The
alternatives weighed were a service-specific setup (rejected: every repo should answer
the same query the same way) and OTel Go auto-instrumentation agents (rejected: they
cannot follow a trace across `segmentio/kafka-go`, nor name spans by chi route pattern).

## Decision

Tier 1 (identical in every repo) and Tier 2 (this context's business metric):

1. **Telemetry adapter.** `internal/adapters/outbound/telemetry` holds `Setup` (OTLP/gRPC
   traces and metrics, W3C TraceContext + Baggage propagator, Go runtime metrics;
   exporters dial lazily, so no Collector means dropped telemetry, never a blocked boot
   or request; returns a shutdown func) and `WithTraceContext` (stamps
   `trace_id`/`span_id` on a log record written with a span-carrying context). Only the
   composition root (`cmd/api`) and the other adapters import it; `internal/domain` and
   `internal/application` never import OpenTelemetry.
2. **Logger.** `slog.New(telemetry.WithTraceContext(slog.NewJSONHandler(os.Stdout, ...)))`,
   `LOG_LEVEL`, uppercase levels. Code on a request or message path uses the `*Context`
   slog variants; the bare variants have no span.
3. **HTTP middleware order** on the chi router: `RequestID` -> `otelchi.Middleware(serviceName,
   WithChiRoutes(r))` -> `otelchimetric.NewServerRequestDuration` -> `RequestLogger` ->
   `Recoverer`; the existing `rejectNUL` and Idempotency-Key middleware stay after them.
   `telemetry.Setup` runs before the router is built (otelchimetric binds the global
   MeterProvider at construction) and is flushed last on shutdown. The request line
   carries `method`, `route` (the chi pattern, never the raw path, which holds ids),
   `status`, `duration_ms`, `bytes` and `request_id`; 5xx is ERROR, 4xx WARN, the rest
   INFO; `/healthz` and `/readyz` are not logged. `RequestLogger` lives in
   `internal/adapters/inbound/requestlog` so a future binary that may not import the
   REST adapter logs identically.
4. **Traces over Kafka.** `OutboxRepo.Append` stamps `traceparent`/`tracestate` from the
   active span into the stored outbox headers, inside the transaction that writes the
   aggregate (`internal/adapters/outbound/postgres/outbox_trace.go`). The relay publishes
   the headers unchanged as Kafka headers; the inbound offer-availability consumer
   extracts them and runs every handling attempt in a consumer span named
   `<topic> process`, so the use case's logs carry the publisher's trace id. Only extra
   headers are added: the CloudEvents attributes and body are unchanged (ADR 0004).
5. **Environment contract.** `cmd/api` reads `OTEL_SERVICE_NAME` (default `pricing`),
   `OTEL_EXPORTER_OTLP_ENDPOINT` (default `telemetry.DefaultEndpoint`, a bare `host:port`
   or a URL), `SERVICE_VERSION`, `ENVIRONMENT` (read by `Setup`) and `LOG_LEVEL`.
6. **Tier 2 business metric.** One counter, `pricing.quotes.issued`, unit `{quote}`,
   attribute `outcome` (`issued` | `rejected`) and, on a rejection, `reason` (the problem
   slug the REST adapter answers with: `item-not-priced`, `empty-quote`,
   `no-charge-schedule`, `no-tax-table`, `concurrent-update`, `validation-failed`,
   `internal-error`, ...). `ports.QuoteMetrics` is the application port; `IssuePriceQuote`
   calls it once per attempt (outside the unit-of-work retry, so a retried transaction
   counts once); the OTel implementation is in the telemetry adapter and injected from
   `cmd/api`; a nil `Deps.Metrics` is a no-op.

### Why this metric

`POST /price-quotes` (`IssuePriceQuote`) is the one call checkout cannot proceed
without (ADR 0001, `apis/openapi.yaml`): a quote that cannot be issued means a shopper
cannot check out. Its rejections are exactly the operational failures a human acts on:
`item-not-priced` (a catalogue/pricing gap), `no-charge-schedule` / `no-tax-table` (a
reference table was never published), versus `internal-error` (the service itself). The
item-price commands are low-volume back-office operations and are already visible as
`ItemPriceChanged` events and request metrics.

### Deviations from the customer-ordering reference

- No `cmd/mcp` and no `docs/runtime.md` or `docs/adr/index.md`: those parts were skipped, not
  stubbed. The Helm chart landed on `develop` while this was in review, so it renders the
  `otel.*` values (`otel.enabled`, `otel.endpoint`, `otel.serviceName`, `environment`) as
  `OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_SERVICE_NAME`, `SERVICE_VERSION` and `ENVIRONMENT`;
  `tests/render_test.sh` asserts them and `extraEnv` refuses all four.
- Only one inbound consumer (offer-availability) exists; the consumer span helper
  (`internal/adapters/inbound/kafka/tracing.go`) sits in the shared run path, so any
  consumer added later is covered.
- The consumer tracing test drives `handleTraced` directly (the package's test fakes
  differ from the reference's).

Not part of this ADR: dashboards and alert rules, which live in warehouse-infra and are
generated per deployed context from the instrument names above.

## Consequences

- A request, its log lines, the events it enqueued and the consumers that handle them in
  other services share one trace id; `trace_id` in Loki jumps to Jaeger.
- Request rate, errors and duration come from `http.server.request.duration`
  (otelchimetric) with no hand-rolled counter; quote health from `pricing.quotes.issued`.
- Every new log call on a request or message path must use a `*Context` variant, or its
  line carries no trace id.
- A consumer span is created per handling attempt, so a retried message shows as several
  spans under the same parent.
- The trace parent of a consumed event is the span active at the outbox write; the relay
  is asynchronous and adds no span of its own. `OutboxRepo.Insert` (raw path used by
  tests) stamps nothing; only `Append` does.
