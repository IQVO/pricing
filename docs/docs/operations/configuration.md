---
id: configuration
title: Configuration
sidebar_label: Configuration
description: "Every environment variable read by the pricing binaries cmd/api and cmd/a2a: default, required or not, meaning."
---

# Configuration

Pricing ships two binaries (`cmd/api` and `cmd/a2a`). Both are configured
only through environment variables and both refuse to boot, naming the
variable, when a mandatory one is missing. There is no in-memory mode, no
`*_MODE` switch and no file-based configuration. The Helm chart
(`charts/pricing`) renders each variable from a `values.yaml` key; see the
[runbook](/docs/operations/runbook) for the chart side.

Source: `cmd/api/config.go`, `cmd/api/main.go`, `cmd/api/policies.go`,
`cmd/api/telemetry.go`, `cmd/a2a/config.go`, `cmd/a2a/main.go`,
`cmd/a2a/telemetry.go`, `internal/adapters/outbound/telemetry/telemetry.go`.

## `cmd/api` (REST, outbox relay, consumer, policy loop)

| Variable | Default | Required? | Meaning |
| --- | --- | --- | --- |
| `HTTP_ADDR` | `:8080` | no | Listen address of the REST server. Its port must equal the chart's `service.targetPort`. |
| `DATABASE_URL` | none | **yes** | Postgres DSN. Boot error `DATABASE_URL is required` when empty. |
| `MIGRATIONS_DATABASE_URL` | value of `DATABASE_URL` | no | Direct (non-pooled, session-mode) DSN used only for the embedded boot migrations. |
| `KAFKA_BROKERS` | none | **yes** | Comma-separated broker list (blanks trimmed). Used by the outbox relay, the consumer and the `/readyz` Kafka check. |
| `OFFER_AVAILABILITY_CONSUMER_GROUP` | none | **yes** | Stable consumer group id of the offer-availability consumer. Never derive it from the pod name. |
| `PRICING_CURRENCY` | `BRL` | no | The one ISO 4217 currency every price is in. Must be three upper-case letters or boot fails. |
| `POLICY_INTERVAL` | `5s` | no | Interval of the clock-driven policy pass (`publish-boundaries`, `expire-quotes`). Go duration or plain seconds; malformed or non-positive logs a warning and uses the default. |
| `OUTBOX_RELAY_INTERVAL` | `1s` | no | Outbox relay poll interval. Go duration or plain seconds; malformed or non-positive falls back to the default with a warning. |
| `OUTBOX_MAX_ATTEMPTS` | `20` | no | Failures before the relay excludes an outbox row from claims. `0` = unlimited; negative or unparsable falls back to `20` with a warning. |
| `SHUTDOWN_DRAIN_DELAY` | `5s` | no | Wait after `/readyz` flips to 503 before the listener closes. `0` disables; invalid or negative uses the default. |
| `LOG_LEVEL` | `info` | no | `debug`, `info`, `warn` (or `warning`) or `error`, case-insensitive; anything else is `info`. |
| `OTEL_SERVICE_NAME` | `pricing` | no | `service.name` of spans, metrics and the Loki `app` label. |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | no | Collector OTLP/gRPC endpoint (`host:port` or URL). Export never blocks startup or requests. |
| `SERVICE_VERSION` | `dev` | no | `service.version` resource attribute. |
| `ENVIRONMENT` | `local` | no | `deployment.environment.name` resource attribute. |

## `cmd/a2a` (read-only A2A agent)

The agent has no Kafka, outbox or consumer, so it reads none of those
variables.

| Variable | Default | Required? | Meaning |
| --- | --- | --- | --- |
| `A2A_ADDR` | `:8093` | no | Listen address of the JSON-RPC server. |
| `A2A_AGENT_URL` | none | **yes** | Absolute `http(s)` URL of this agent's JSON-RPC endpoint, advertised on the Agent Card (ADR 0007). A relative or schemeless value is refused at boot. The chart defaults it to `http://<release>-a2a.<ns>.svc.cluster.local:8093/`. |
| `DATABASE_URL` | none | **yes** | Postgres DSN of the same database the api uses. |
| `MIGRATIONS_DATABASE_URL` | value of `DATABASE_URL` | no | Direct DSN for the boot migrations (the agent runs the same embedded, idempotent migrations). |
| `LOG_LEVEL` | `info` | no | Same values as the api. |
| `OTEL_SERVICE_NAME` | `pricing-a2a` | no | `service.name` of spans and metrics. |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | no | Collector OTLP/gRPC endpoint. |
| `SERVICE_VERSION` | `dev` | no | `service.version` and the version on the Agent Card. |
| `ENVIRONMENT` | `local` | no | `deployment.environment.name` resource attribute. |

## Validation order and boot errors

`cmd/api` validates in `loadRuntime` before anything is opened or bound:
`DATABASE_URL`, then `KAFKA_BROKERS`, then `PRICING_CURRENCY` (if set), then
each consumer group variable. The first failure is returned (exit code 1,
message logged as `service exited with error`). `cmd/a2a` joins every problem
(`DATABASE_URL`, `A2A_AGENT_URL`) into one `invalid configuration:` error.

Unparsable tuning values (`POLICY_INTERVAL`, `OUTBOX_RELAY_INTERVAL`,
`OUTBOX_MAX_ATTEMPTS`, `SHUTDOWN_DRAIN_DELAY`) never abort boot: they log a
warning naming the variable and use the default, so a typo cannot silently
remove the relay's attempt cap.

## Chart values to variables

| Env var | Chart key |
| --- | --- |
| `HTTP_ADDR` | `config.httpAddr` |
| `LOG_LEVEL` | `config.logLevel` |
| `PRICING_CURRENCY` | `config.currency` (empty = not rendered) |
| `POLICY_INTERVAL` | `config.policyInterval` (empty = not rendered) |
| `OFFER_AVAILABILITY_CONSUMER_GROUP` | `config.offerAvailabilityConsumerGroup` (required) |
| `OUTBOX_RELAY_INTERVAL` | `config.outboxRelayInterval` |
| `OUTBOX_MAX_ATTEMPTS` | `config.outboxMaxAttempts` (chart default `20`) |
| `SHUTDOWN_DRAIN_DELAY` | `config.shutdownDrainDelay` |
| `DATABASE_URL` | `database.url` or `database.existingSecret` (always a Secret) |
| `MIGRATIONS_DATABASE_URL` | `database.migrationsExistingSecretKey` / `database.migrationsUrl` |
| `KAFKA_BROKERS` | `kafka.brokers` (required) |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `otel.endpoint` (rendered only when `otel.enabled`) |
| `OTEL_SERVICE_NAME` | `otel.serviceName` (a2a gets `<serviceName>-a2a`) |
| `ENVIRONMENT` | `environment` (chart default `production`) |
| `SERVICE_VERSION` | image tag, or `appVersion` when empty |
| `A2A_ADDR` | `a2a.httpAddr` |
| `A2A_AGENT_URL` | `a2a.agentUrl` |

The chart refuses any `*_MODE` variable and any `*_CONSUMER_GROUP` in
`extraEnv`, and refuses a `terminationGracePeriodSeconds` that does not exceed
the drain delay plus 40 seconds.
