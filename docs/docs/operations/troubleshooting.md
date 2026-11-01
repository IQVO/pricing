---
id: troubleshooting
title: Troubleshooting
sidebar_label: Troubleshooting
description: "Symptom, cause, check and fix for Pricing's boot failures, readiness, REST problem types, outbox, consumer and A2A agent."
---

# Troubleshooting

Each entry is symptom, cause, check, fix, taken from the failure modes in the
code. Commands assume the chart's defaults (namespace and release `pricing`).
See the [Runbook](/docs/operations/runbook) for the procedures and
[Observability](/docs/operations/observability) for the log messages.

## Boot failures

| Symptom | Cause | Check | Fix |
| --- | --- | --- | --- |
| Exit 1, log `DATABASE_URL is required: this context runs on real Postgres, there is no in-memory mode` | `DATABASE_URL` empty | `kubectl -n pricing get secret pricing-db` has the `DATABASE_URL` key | Set `database.existingSecret` / `database.url`; there is no fallback mode |
| `KAFKA_BROKERS is required: this context runs on real Kafka (outbox relay, consumers, /readyz)` | `KAFKA_BROKERS` empty or only commas and blanks | `kafka.brokers` value | Set a comma-separated broker list |
| `OFFER_AVAILABILITY_CONSUMER_GROUP is required: the consumer always runs on real Kafka` | Group id not set | `config.offerAvailabilityConsumerGroup` | Set a stable, service-prefixed id such as `pricing-offer-availability`; the chart also refuses to render without it |
| `PRICING_CURRENCY "usd" is not an ISO 4217 code (three upper-case letters)` | Lower-case or wrong-length code | `config.currency` | Use `USD`, `BRL` and so on |
| `cmd/a2a`: `invalid configuration: A2A_AGENT_URL is required...` or `A2A_AGENT_URL="..." is not an absolute http(s) URL` | Agent URL missing, relative or schemeless | `a2a.agentUrl` (empty defaults to the in-cluster Service URL) | Give an absolute `http(s)` URL clients can dial |
| Pod in `CrashLoopBackOff`, logs show `retrying` with `op` = `run migrations` or `ping postgres`, then exit | Postgres unreachable for the whole bounded retry (five attempts, 1+2+4+8 s) or a migration fails | `kubectl -n pricing logs deploy/pricing --previous`; try the DSN with `psql` | Fix the DSN, network policy or mesh sidecar start-up; for a pooled DSN make sure the `MIGRATIONS_DATABASE_URL` key is a direct connection |
| Migrations hang | `DATABASE_URL` goes through a transaction-mode pooler and `MIGRATIONS_DATABASE_URL` is unset | Is the migrations key in the Secret? | Populate `database.migrationsExistingSecretKey` with a session-mode DSN |
| Tuning value ignored, WARN `invalid OUTBOX_RELAY_INTERVAL; using the default` (or `POLICY_INTERVAL`, `OUTBOX_MAX_ATTEMPTS`, `SHUTDOWN_DRAIN_DELAY`) | Malformed or non-positive value | The warning carries `value` and `default` | Use a Go duration (`5s`) or plain seconds; `OUTBOX_MAX_ATTEMPTS` takes a non-negative integer |
| Helm refuses to render | A mandatory value is missing, a `*_MODE` or `*_CONSUMER_GROUP` is in `extraEnv`, or `terminationGracePeriodSeconds` is below drain + 40 s | The failure names the value | Fix the value |

## Readiness and liveness

| Symptom | Cause | Check | Fix |
| --- | --- | --- | --- |
| `/readyz` is `503` with body `{"status":"not_ready"}` | Shutdown has started: the drain gate flipped | Pod is `Terminating` | Expected during a rollout; the pod stays live until the drain delay passes |
| `/readyz` is `503 dependency-unavailable` (problem detail `not ready: kafka` or `not ready: postgres`), `/healthz` is `200` | The named dependency does not answer within 2 s (verdict cached 3 s) | The WARN log `readiness: dependency is not ready` (`dependency`, `error`) carries the cause | Restore the dependency; the pod stays up and live and becomes ready on its own |
| Pod restarts with `<worker> stopped unexpectedly: exiting so the orchestrator restarts the service` | The relay, the consumer or the policy loop returned on its own, usually a lost broker after 10 consecutive fetch failures | `--previous` logs, the worker name, the preceding ERROR | Fix Kafka connectivity; the restart is deliberate |
| `cmd/a2a` ready while Postgres is down | Its readiness probe is `/healthz`, which checks nothing | A skill call answers a 500 problem | Check Postgres; each skill reports a failure in band |

## REST problem types

Errors are RFC 9457 `application/problem+json` with `type` `/problems/<slug>`
(`internal/adapters/inbound/http/errors.go`).

| Status | Slug | Cause | Fix |
| --- | --- | --- | --- |
| 400 | `malformed-request` | Bad JSON, unknown field, wrong type, body over 64 KiB, or a NUL character in path, query or body | Send exactly the fields of `apis/openapi.yaml` |
| 400 | `idempotency-key-required` | A `POST` without an `Idempotency-Key` header | Send a unique key per logical request |
| 400 | `idempotency-key-invalid` | An `Idempotency-Key` that is longer than 128 characters or has a byte outside printable ASCII | Send 1 to 128 printable ASCII characters, e.g. a UUID |
| 400 | `invalid-limit` / `invalid-cursor` / `unknown-query-parameter` | `GET /item-prices` with a `limit` outside 1 to 500, a `cursor` that does not decode, or any parameter other than `limit` and `cursor` (the retired `after`, `offset`, `nextToken`, `from`) | Page with `limit` and the `nextCursor` of the previous page |
| 409 | `idempotency-key-reused` | The same key with a different method, path or body (records live 24 h) | Use a new key, or resend the identical request to get the replay; this is a client bug, do not retry blindly |
| 404 | `not-found` | Unknown item price, quote or sale id, or no current schedule or tax table on `GET .../current` | Create it first |
| 409 | `overlapping-sale-window` | The new sale window overlaps a live one of the same item | Cancel the other sale or choose another window |
| 409 | `non-increasing-version` | Published `version` of a schedule or tax table is not above the current one | Publish current version + 1 |
| 409 | `concurrent-update` | A version mismatch: `PUT .../list-price` with a stale `version`, a second create for the same item, or a lost race after the in-process retry (2 attempts for commands, 5 for consumed events) | Re-read the resource and retry |
| 409 | `illegal-transition` | A command on a `WITHDRAWN` price (set list price, schedule or cancel a sale, withdraw again) | A withdrawn price is never revived (ADR 0002 A3) |
| 422 | `unknown-catalog-item` | The item is unknown or `DISCONTINUED` in the local `catalog_items` view | Check the offer-availability consumer is running and the `CatalogItemCreated` event arrived |
| 422 | `sale-not-below-list` | A sale price is not below the list price, or a new list price is not above an existing sale price | Lower the sale or raise the list price |
| 422 | `incomplete-charge-schedule` | A schedule does not name a charge for every tier (`SAME_DAY`, `NEXT_DAY`, `TWO_DAY`, `STANDARD`) | Add the missing tier |
| 422 | `overlapping-tax-band` | Tax bands overlap | Make the prefix ranges disjoint |
| 422 | `rate-out-of-range` | `rateBasisPoints` outside 0 to 10000 | Fix the rate |
| 422 | `item-not-priced` | A quote line names an item with no price or a `WITHDRAWN` one | Publish the price (`POST /item-prices`) |
| 422 | `empty-quote` | A quote request with no lines | Send at least one line |
| 422 | `no-charge-schedule` / `no-tax-table` | No `CURRENT` schedule or tax table was ever published | Publish one; until then every checkout fails (watch `pricing_quotes_issued_total` by `reason`) |
| 422 | `negative-amount` | A negative amount | Amounts are zero or more minor units |
| 422 | `currency-mismatch` | An amount is not in `PRICING_CURRENCY` (the kind cluster configures `USD`) | Send the configured currency |
| 422 | `validation-failed` | Any other value rule: invalid id (1 to 64 characters of `A-Z a-z 0-9 . _ : -`), quantity outside 1 to 1000, more than 100 lines or 50 groups, a line in no group or in two, a postal prefix that is not 3 upper-case letters or digits, more than 50 sale windows, a window that ends in the past | The `detail` names the rule |
| 500 | `internal-error` | An unexpected error (never leaks the message; it is logged) | Read the ERROR log line with the same `request_id` |

## Events and the outbox

| Symptom | Cause | Check | Fix |
| --- | --- | --- | --- |
| Downstream contexts do not see price changes | The outbox is not draining | `SELECT count(*), min(created_at) FROM outbox_events WHERE published_at IS NULL`; logs `outbox relay pass failed` | Restore Kafka; the relay resumes by itself |
| Rows stay unpublished with `attempts` equal to `OUTBOX_MAX_ATTEMPTS` | A row reached the attempt cap and was excluded (ERROR `outbox relay: row reached the attempt cap...`); later rows overtake it | `SELECT id, event_type, attempts, last_error FROM outbox_events WHERE published_at IS NULL AND attempts >= 20` | Fix the cause in `last_error`, then `UPDATE outbox_events SET attempts = 0 WHERE id = ...` |
| An item's price changed but no `ItemPriceChanged` | The published view did not move: the event is raised only when list price, effective price or sale end differs from the last published one | Compare the item's `evaluated_at` and `published_*` columns | Expected |
| A sale started but the event came late | The boundary pass runs every `POLICY_INTERVAL` (5 s) | Log `policy pass done` with `handled` | Lower `POLICY_INTERVAL`; the effective price on `GET` is correct meanwhile |
| Duplicate events seen downstream | Delivery is at-least-once; a crash between send and `UPDATE` resends the row | The `id` of the CloudEvent is stable | Consumers dedupe on `id` |
| `PriceQuoteIssued` is not on `retailnetwork.pricing.events` | It is analytics-only | Look at `retailnetwork.pricing.analytics` | Expected; there is no `PriceQuoteExpired` event at all |

## Consumer

| Symptom | Cause | Check | Fix |
| --- | --- | --- | --- |
| New catalogue items never become priceable (`unknown-catalog-item`) | The consumer is behind, stuck or on the wrong group | Log `consumer running` (`topic`, `group_id`); broker consumer-group lag; `SELECT * FROM catalog_items` | Verify `OFFER_AVAILABILITY_CONSUMER_GROUP` is the intended stable id (a fresh id replays the topic from the beginning) |
| A partition stops advancing | A message cannot be dead-lettered (the DLQ publish retries forever) | ERROR `offer-availability dead-letter publish failed; retrying the same message` | Make the DLQ topic writable (auto-creation or create it by hand) |
| Messages on `retailnetwork.offer-availability.events.dlq` | A payload that can never apply (invalid `catalogItemId`) or five failed transient attempts | Headers `x-dlq-error`, `x-dlq-source-offset` | Fix the cause and re-publish the value to the source topic |
| WARN `skipping a message that is not a valid CloudEvent` | A message that is not CloudEvents 1.0 JSON on the topic | `topic`, `consumer` in the line | Fix the producer; the message is committed past |
| A discontinued item still has an active price | The `CatalogItemDiscontinued` event was not consumed, or the price was already `WITHDRAWN` | `catalog_items.state` and `item_prices.state` | Withdraw manually with `POST /item-prices/{catalogItemId}/withdrawals` |

## A2A agent

| Symptom | Cause | Check | Fix |
| --- | --- | --- | --- |
| Agent Card advertises an unreachable URL | `A2A_AGENT_URL` is the in-cluster default but the client is off-cluster | `curl .../.well-known/agent-card.json` and read the interface URL | Set `a2a.agentUrl` to the gateway URL |
| Answer is a `problem` with status 400 and type `.../malformed-skill-request` or `.../invalid-argument` | The first message part is not `{"skill": ..., "arguments": {...}}`, a field is unknown (rejected, never ignored), or a required argument (`catalogItemId`, `quoteId`) is missing | The problem `detail`; types are under `https://errors.pricing.warehouse-systems.dev` | Send exactly the documented shape; skills are `item_price` and `quote_status` |
| Answer is a 404 problem (`.../not-found`) | Unknown id, an id longer than 128 characters, or an id with NUL | Same id on `GET /item-prices/{id}` | Expected |
| Answer is a 400 problem `.../unknown-skill` | Any `skill` other than the two | Agent Card skills | Use a listed skill; the agent has no write skill |
| Answer is a 500 problem `.../internal-error` | A use-case error that is not a miss, usually Postgres | ERROR log `a2a skill failed` | Check Postgres |
