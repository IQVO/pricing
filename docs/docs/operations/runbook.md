---
id: runbook
title: Operations runbook
sidebar_label: Runbook
description: "Deploy, probe, roll back and operate Pricing: Helm chart, readiness, migrations, Kafka topics and groups, outbox relay, dead letters and the policy loop."
---

# Pricing: operations runbook

Pricing owns item prices, charge schedules, sales, tax tables and price quotes
in one configured currency. It ships two deployables from one image:

- **`cmd/api`** (the `api` Deployment): REST, the transactional outbox relay,
  the offer-availability consumer and the clock-driven policy loop in a single
  process. Always on.
- **`cmd/a2a`** (the `<release>-a2a` Deployment): the read-only A2A agent.
  Optional (`a2a.enabled`, default `false`), needs Postgres only.

Runtime: real Postgres and real Kafka only. There is no in-memory or permissive
mode, so a pod that cannot reach either refuses to boot and never pretends to
be healthy. Every variable is on [Configuration](/docs/operations/configuration).

## Deploy

The chart is `charts/pricing` (packaged by the `release` job as
`oci://ghcr.io/iqvo/pricing:<version>`; the image is
`ghcr.io/iqvo/pricing:v<version>`). The kind cluster deploys it through the
warehouse-infra ArgoCD app with the local image `warehouse/pricing`. The
packaging checklist is [Before the first release](/docs/operations/before-first-release).

Mandatory values: the chart refuses to render without them, naming the value.

| Value | Env var | Why |
| --- | --- | --- |
| `database.url` or `database.existingSecret` | `DATABASE_URL` | Postgres DSN (from a Secret, never the ConfigMap) |
| `database.migrationsExistingSecretKey` / `database.migrationsUrl` | `MIGRATIONS_DATABASE_URL` | Direct (non-pooled) DSN for boot migrations; falls back to `DATABASE_URL` |
| `kafka.brokers` | `KAFKA_BROKERS` | Comma-separated brokers (relay, consumer, `/readyz`) |
| `config.offerAvailabilityConsumerGroup` | `OFFER_AVAILABILITY_CONSUMER_GROUP` | Stable, service-prefixed group id, never the pod name |

Useful optionals: `config.currency` (`PRICING_CURRENCY`, default `BRL`),
`config.policyInterval` (default 5s), `config.outboxRelayInterval` (default
1s), `config.outboxMaxAttempts` (default 20, 0 = unlimited),
`config.shutdownDrainDelay` (default 5s), `autoscaling.api.*` (HPA, default
off), `ingress` and `gatewayApi` (both off, both route to the api Service
only). `terminationGracePeriodSeconds` must exceed drain + 40s (HTTP 10s +
consumer 10s + policy loop 10s + relay 10s); the chart refuses anything shorter
and ships `50`.

Deploy or upgrade:

```sh
helm upgrade --install pricing charts/pricing \
  --namespace pricing --create-namespace \
  --set database.existingSecret=pricing-db \
  --set kafka.brokers=kafka.kafka.svc.cluster.local:9092 \
  --set config.offerAvailabilityConsumerGroup=pricing-offer-availability
kubectl -n pricing rollout status deployment/pricing
```

The pod runs as uid 1000 with a read-only root filesystem, all capabilities
dropped and `RuntimeDefault` seccomp.

### The A2A agent

```sh
helm upgrade --install pricing charts/pricing -n pricing --reuse-values \
  --set a2a.enabled=true
kubectl -n pricing rollout status deployment/pricing-a2a
```

`a2a.agentUrl` empty defaults `A2A_AGENT_URL` to
`http://<release>-a2a.<ns>.svc.cluster.local:8093/`; override it with the
gateway URL when the agent must be reachable off-cluster. With
`a2a.route.enabled=true` the chart renders an HTTPRoute at `/api/a2a/pricing`
(rewritten to `/`). The agent mounts neither the api ConfigMap nor any Kafka
value: if the Deployment has a `KAFKA_BROKERS` variable, something outside the
chart added it.

## Rollback

```sh
helm history pricing -n pricing
helm rollback pricing <revision> -n pricing
kubectl -n pricing rollout status deployment/pricing
```

The boot migration is forward-only (`0001_pricing_schema` is the only one
today). Before rolling back across a release that added a migration, confirm
the older binary tolerates the newer schema (columns are additive fleet-wide).
The consumer group is stable, so rolled-back pods resume from the committed
offsets with no replay storm.

## Probes

| Endpoint | Binary | Meaning |
| --- | --- | --- |
| `GET /healthz` | api, a2a | Liveness only; startup and liveness probes. The a2a agent also uses it as readiness |
| `GET /readyz` | api | `200` only while Postgres answers a ping **and** a Kafka broker answers a metadata request (verdict cached 3s, bounded 2s). Flips to `503` first during shutdown, before the listener drains |

Startup probe: `/healthz`, every 2s, 30 failures (about 60s). The migrations
and the first Postgres ping retry five times with a 1, 2, 4, 8 second backoff
(`internal/bootretry`) because the first outbound dial of an injected pod can
be reset by the mesh sidecar. After that the process exits and the
orchestrator restarts it.

```sh
kubectl -n pricing port-forward svc/pricing 8080:80
curl -i http://localhost:8080/healthz
curl -i http://localhost:8080/readyz
```

A pod that exits non-zero on its own means the relay, the consumer or the
policy loop returned: the process exits deliberately (`<worker> stopped
unexpectedly: exiting so the orchestrator restarts the service`). Read
`kubectl -n pricing logs deploy/pricing --previous` for the worker name.

## Kafka

| Topic | Direction | Notes |
| --- | --- | --- |
| `retailnetwork.pricing.events` | produced (outbox relay) | `ItemPriceChanged`, `ItemPriceWithdrawn`, `DeliveryChargeScheduleChanged`, `TaxTableChanged`; key = aggregate id |
| `retailnetwork.pricing.analytics` | produced (outbox relay) | `PriceQuoteIssued` only |
| `retailnetwork.offer-availability.events` | consumed | group = `OFFER_AVAILABILITY_CONSUMER_GROUP` (suggested `pricing-offer-availability`) |
| `retailnetwork.offer-availability.events.dlq` | dead letters | written by the consumer; see below |

The service never creates topics through an admin API. The producing writer
and the dead-letter writer set `AllowAutoTopicCreation`, so a broker with
auto-creation off needs the topics created by hand. Messages are CloudEvents
1.0 in structured mode (`content-type: application/cloudevents+json;
charset=UTF-8`); see [Events](/docs/api-reference/events).

### Consumer and dead letters

The consumer fetches one message at a time and commits the offset only after
the handler returned nil (at-least-once). Each message is handled inside one
unit of work together with the inbox claim on the CloudEvents `id`
(`processed_events`), so a redelivery is a no-op.

- Not a CloudEvent, or an unknown `type`: logged at WARN and committed past.
- Payload that can never apply (a `catalogItemId` that is not a valid id):
  dead-lettered at once with no retry.
- Transient failure: retried with capped exponential backoff (200ms doubling
  to 5s) up to 5 attempts, then dead-lettered.
- A lost or stalled broker connection while fetching is retried (up to 10
  consecutive failures), after which the consumer returns and the process
  exits for a restart.
- The dead-letter publish itself retries forever, so a message that cannot be
  dead-lettered blocks its partition instead of being dropped.

Dead letters carry the original key, value and headers plus
`x-dlq-source-topic`, `x-dlq-source-partition`, `x-dlq-source-offset`,
`x-dlq-error` and `x-dlq-failed-at`. Fix the cause, then re-publish the value
to `retailnetwork.offer-availability.events` (the inbox makes a re-drive safe).

### Outbox relay

Writes append rows to `outbox_events` in the same transaction as the aggregate.
The relay (always on, inside `cmd/api`) claims up to 100 unpublished rows per
pass with `FOR UPDATE SKIP LOCKED`, sends them one at a time in id order and
stops at the first failure so a later event for an aggregate never overtakes a
failed earlier one. A full batch is followed immediately by another pass, any
other pass sleeps `OUTBOX_RELAY_INTERVAL`. Delivery is at-least-once with the
CloudEvents `id` persisted at encode time.

The relay excludes a row after `OUTBOX_MAX_ATTEMPTS` failures (logged once at
ERROR: `outbox relay: row reached the attempt cap and is excluded from future
claims`); later rows overtake it. Inspect and re-arm:

```sql
SELECT id, event_type, subject, attempts, last_error FROM outbox_events
 WHERE published_at IS NULL ORDER BY id;
UPDATE outbox_events SET attempts = 0 WHERE id = 42;
```

Several api replicas are safe for the relay (row claims), the version-guarded
writes (`concurrent-update` on conflict) and the consumer (stable group, inbox
dedupe), but this has not been load-tested.

### Policy loop (sweepers)

Every `POLICY_INTERVAL` the api runs two passes, each over a batch of 200:

| Policy | Use case | Effect |
| --- | --- | --- |
| `publish-boundaries` | `PublishBoundaries` | For item prices whose sale window started or ended since last evaluated, publishes `ItemPriceChanged` when the effective price moved |
| `expire-quotes` | `ExpireQuotes` | Moves `ISSUED` quotes past `expiresAt` to `EXPIRED`; publishes nothing |

A failing pass is logged (`policy pass failed`) and retried on the next tick.
Reads never depend on the loop: a quote past `expiresAt` reads `EXPIRED` and
the effective price is computed at read time.

## Postgres

The service connects as role `pricing` (its own schema only). Boot migrations
run against `MIGRATIONS_DATABASE_URL` (a direct, session-mode connection); when
the pooled DSN goes through a transaction-mode pooler, keep the direct key
populated or migrations hang. Both binaries run the same embedded, idempotent
migrations at boot.

Tables: `item_prices`, `sale_windows`, `delivery_charge_schedules`,
`tier_charges`, `tax_tables`, `tax_bands`, `price_quotes`, `quote_lines`,
`quote_groups`, `catalog_items`, `processed_events`, `idempotency_keys`,
`outbox_events`. Nothing purges `processed_events`, `idempotency_keys` (rows
older than 24 hours are treated as absent but not deleted) or published
`outbox_events` rows: plan retention if the volume matters.

## Routine procedures

| Task | How |
| --- | --- |
| Publish a new delivery charge schedule | `POST /delivery-charge-schedules` with `version` greater than the current one and a charge for every tier; the previous one becomes `SUPERSEDED`. Quotes already issued keep the version they used |
| Publish a new tax table | `POST /tax-tables`, same versioning rule; bands must not overlap |
| Change the currency | `PRICING_CURRENCY` applies to every price and quote; the service has no conversion. Do not change it on a database that already holds prices |
| Stop the shopper seeing an item | `POST /item-prices/{catalogItemId}/withdrawals`; discontinuation from offer-availability does the same automatically |
| Check what the relay has not shipped | `SELECT count(*) FROM outbox_events WHERE published_at IS NULL` |

## Dashboards and telemetry

Logs are stdout JSON (level from `LOG_LEVEL`) collected by the cluster log
pipeline; traces and metrics go to the OpenTelemetry Collector over OTLP/gRPC.
The instruments, spans and log fields are on
[Observability](/docs/operations/observability). Dashboards and alerts for the
fleet live in the warehouse-infra repository; look for the pricing panels there.
For failure modes see [Troubleshooting](/docs/operations/troubleshooting).
