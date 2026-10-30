# ADR 0007: A2A inbound adapter

## Status

Accepted (2026-10-30). Follows the fleet's Agent2Agent rollout (reference: workforce-management ADR 0039
and wes-work-planning ADR 0046); builds on [ADR 0004](./0004-cloudevents-envelope-and-type-catalogue.md)
(CloudEvents on every Kafka message) and [ADR 0005](./0005-fleet-observability-standard.md) (observability).

## Context

The fleet is adopting the Agent2Agent protocol so that a bounded context can **delegate** a goal to another
context's agent. Three transports now carry three different things and must not overlap:

| Transport | Carries |
|---|---|
| Kafka + CloudEvents | **facts** between contexts ([ADR 0004](./0004-cloudevents-envelope-and-type-catalogue.md)) |
| MCP | an agent's **tools** |
| A2A | **delegation** of a goal to another context's agent |

pricing has no MCP surface and no `cmd/mcp`, so there is no tool charter to derive skills from and no
existing agent-facing write to mirror. The only agent-addressable surface is REST (`apis/openapi.yaml`).

## Decision

Add `internal/adapters/inbound/a2a`, a second driving adapter over the **same** application-layer use cases the
REST adapter uses, built on the official `a2aproject/a2a-go/v2` SDK (v2.7.0) and served by a new `cmd/a2a`
binary (JSON-RPC 2.0 over HTTP, port **8093**, the Agent Card at `/.well-known/agent-card.json`, the endpoint
mounted at `/` and `/a2a`, `GET /healthz` for the probes).

### Read-only, by construction

With no MCP charter to widen, the agent is **read-only**: it never changes a price, a sale window, a charge
schedule, a tax table or a quote, and no A2A call can write an outbox row. Each skill is backed by one existing
query use case that a REST `GET` already exposes, and returns exactly the fields that `GET` returns (nothing is
added: the answer field sets are pinned by a test to the REST response fields). pricing holds no cost or margin
data, and none could be exposed through this surface.

### Two curated skills, not one per endpoint

| Skill | Kind | Use case | REST equivalent |
|---|---|---|---|
| `item_price` | read | `GetItemPrice` | `GET /item-prices/{catalogItemId}` |
| `quote_status` | read | `GetPriceQuote` | `GET /price-quotes/{quoteId}` |

`item_price` answers "what does this item sell for right now": state, list price, the effective price at this
instant and every sale window. `quote_status` answers "what did this quote price and is it still valid":
`ISSUED` or `EXPIRED` at this instant (derived from `expiresAt` exactly as the REST read does), the lines, the
delivery charge per group, tax, total and the versions it used. The bootstrap list (`GET /item-prices`) and the
current charge schedule and tax table are deliberately not skills: the list is for projections rebuilding their
read model (a data-sync job, which is Kafka's and REST's, not delegation), and the policy tables are inputs to
quotes, not goals a peer delegates. Every state-changing operation (`IssuePriceQuote`, `SetListPrice`,
`ScheduleSale`, `WithdrawPrice`, the publishers) stays REST-only and is answered `unknown-skill` if asked.

### Message-only, answered in-band

Both skills are request/response reads, so the agent is message-only (no Task lifecycle). Request-shape
failures and domain rejections are **answered** as RFC 7807-shaped `problem`s inside the message envelope,
never raised as JSON-RPC transport errors, with the **same classification as the REST adapter** for these
reads: `404` not found (an unknown id; an id longer than the REST path bound of 128 or carrying U+0000, which
REST also answers 404 and which would otherwise reach Postgres as SQLSTATE 22021), `400` for what the A2A
surface owns (malformed or unknown skill request, missing or unknown argument), `500` for anything else with a
detail that never leaks the internal message (it is logged).

### Strict input

Unknown argument names and unknown top-level fields are **rejected**, not ignored, and trailing data after the
JSON document is rejected. A text part holding the same JSON as a data part is accepted.

### Facts still travel on Kafka

A2A never becomes a data-sync channel and this agent produces and consumes nothing. `cmd/a2a` is a deliberately
thin clone of `cmd/api`'s composition root: it keeps the embedded migrations, the pgx pool and the item-price and
quote repositories, and drops REST, the outbox relay, the offer-availability consumer, the policy loop and the
Kafka readiness gate. It reads only `DATABASE_URL`, `MIGRATIONS_DATABASE_URL`, `A2A_ADDR` (default `:8093`),
`A2A_AGENT_URL` (mandatory; an absolute http(s) URL, refused at boot otherwise, advertised on the card) and the
shared logging and telemetry variables; it does **not** read `KAFKA_BROKERS`, `EVENT_PUBLISHER` or any consumer
group. It runs the same idempotent embedded migrations as the api so that it never serves a schema older than its
code; the migrations DSN is the direct one when provided.

### Unauthenticated, like REST

The card declares no security schemes: this surface is unauthenticated by the same fleet-wide decision as REST.
Schemes are added to the card, not a redesign, if that decision is ever reversed.

### Fitness sensors

`internal/architecture/a2a_fitness_test.go` pins the shape: the adapter imports only the application layer and
the domain; nothing but `cmd/a2a` imports it; no shipped code imports the SDK's `a2aclient` (this context
receives delegation, it does not delegate); the only use cases the adapter and `cmd/a2a` name are the two queries
(so no command, and with it no outbox write, is reachable); and `cmd/a2a` imports no Kafka, outbox-relay or REST
server package and names no Kafka, outbox or consumer-group variable.

## Consequences

- A second deployable (`/app/a2a`) in the same image and an `a2a.*` chart block (`<release>-a2a`, default
  **off**), exposed through the gateway at `/api/a2a/pricing` rewritten to `/`. The chart renders no Kafka,
  outbox or consumer variable for it, and a chart test fails if one appears.
- Port 8093 is the fleet's A2A port (the api is on 8080).
- Because the agent can only read, a delegating agent that wants a price changed or a quote issued still calls
  REST (checkout issues quotes, an operator sets prices). Adding a write skill later needs its own ADR and an
  MCP-or-REST charter that already offers that write; it would go through the existing use case and the
  transactional outbox, with an integration test proving the unpublished outbox row.
- The agent holds a Postgres pool of its own (`postgres.MaxConns` = 10); a deployment that scales it should size
  Postgres connections for api replicas plus a2a replicas.
