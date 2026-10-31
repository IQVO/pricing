# Pricing — Operations Runbook

Pricing owns item prices, charge schedules, sales, tax tables and price quotes
in one configured currency. One deployable: `cmd/api` (REST, the transactional
outbox relay, the offer-availability consumer and the clock-driven policy loop
in a single process). Runtime: real Postgres and real Kafka only — there is no
in-memory or permissive mode, so a pod that cannot reach either refuses to
boot and never pretends to be healthy.

## Deploy

The chart is `charts/pricing` (packaged by the `release` job as
`oci://ghcr.io/iqvo/pricing:<version>`; the image is
`ghcr.io/iqvo/pricing:v<version>`). The kind cluster deploys it through the
warehouse-infra ArgoCD app with the local image `warehouse/pricing`.

Mandatory values — the chart refuses to render without them:

| Value | Env var | Why |
| --- | --- | --- |
| `database.url` or `database.existingSecret` | `DATABASE_URL` | Postgres DSN (from a Secret, never the ConfigMap) |
| `database.migrationsExistingSecretKey` / `database.migrationsUrl` | `MIGRATIONS_DATABASE_URL` | Direct (non-pooled) DSN for boot migrations; falls back to `DATABASE_URL` |
| `kafka.brokers` | `KAFKA_BROKERS` | Comma-separated brokers (relay, consumer, `/readyz`) |
| `config.offerAvailabilityConsumerGroup` | `OFFER_AVAILABILITY_CONSUMER_GROUP` | Stable, service-prefixed group id — never the pod name |

Useful optionals: `config.currency` (`PRICING_CURRENCY`, default `BRL`),
`config.policyInterval` (default 5s), `config.outboxRelayInterval` (default
1s), `config.outboxMaxAttempts` (default 20, 0 = unlimited),
`config.shutdownDrainDelay` (default 5s). `terminationGracePeriodSeconds` must
exceed drain + 40s (HTTP 10s + consumer 10s + policy loop 10s + relay 10s);
the chart refuses anything shorter.

Deploy / upgrade:

```sh
helm upgrade --install pricing charts/pricing \
  --namespace pricing --create-namespace \
  --set database.existingSecret=pricing-db \
  --set kafka.brokers=kafka.kafka.svc.cluster.local:9092 \
  --set config.offerAvailabilityConsumerGroup=pricing-offer-availability
kubectl -n pricing rollout status deployment/pricing
```

## Rollback

```sh
helm history pricing -n pricing
helm rollback pricing <revision> -n pricing
kubectl -n pricing rollout status deployment/pricing
```

The boot migrations are forward-only; before rolling back across a release
that added a migration, confirm the older binary tolerates the newer schema
(columns are additive fleet-wide). The consumer group is stable, so the
rolled-back pods resume from the committed offsets — no replay storm.

## Health

- `GET /healthz` — liveness only (startup + liveness probes).
- `GET /readyz` — 200 only while Postgres answers a ping AND a Kafka broker
  answers a metadata request (verdict cached 3s, bounded 2s). Flips to 503
  first during shutdown, before the listener drains.

```sh
kubectl -n pricing port-forward svc/pricing 8080:80
curl -i http://localhost:8080/healthz
curl -i http://localhost:8080/readyz
```

A pod that exits non-zero on its own means the relay, the consumer or the
policy loop died — the process exits deliberately so the orchestrator restarts
it; check `kubectl -n pricing logs deploy/pricing --previous` for the worker
name in the final log line.

## Kafka

| Topic | Direction | Notes |
| --- | --- | --- |
| `retailnetwork.pricing.events` | produced (outbox relay) | price/quote lifecycle events |
| `retailnetwork.offer-availability.events` | consumed | group `pricing-offer-availability` (values-overridable) |
| `retailnetwork.offer-availability.events.dlq` | dead letters | poison messages land here; re-drive after fixing the cause |

The relay excludes an outbox row after `OUTBOX_MAX_ATTEMPTS` failures; re-arm a
row with `UPDATE outbox_events SET attempts = 0 WHERE id = ...`.

## Postgres

The service connects as role `pricing` (least privilege: its own schema only —
item prices, schedules, tax tables, quotes, inbox, outbox, idempotency). Boot
migrations run against `MIGRATIONS_DATABASE_URL` (direct, session-mode
connection); when the pooled DSN goes through a transaction-mode pooler, keep
the direct key populated or migrations hang. The migrations/first ping retry
with bounded backoff (`internal/bootretry`), so the startup probe allows ~60s.

## Dashboards and telemetry

Logs are stdout JSON (level from `LOG_LEVEL`) collected by the cluster log
pipeline. Dashboards and alerts for the fleet live in the warehouse-infra
repository (Grafana dashboards under its observability stack, backed by
Prometheus); look for the pricing service panels there.
