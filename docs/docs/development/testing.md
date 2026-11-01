---
id: testing
title: Testing
sidebar_label: Testing
description: "Pricing's test pyramid: unit, godog features, repository contract, integration, fitness tests, mutation, the exact make targets and the CI jobs."
---

# Testing

Every CI sensor has a `make` target, so the feedback CI gives after a push is
available before it. `make help` lists them all.

## The pyramid as it is

| Layer | Where | What it proves | Runs with |
| --- | --- | --- | --- |
| Unit, domain | `internal/domain/{itemprice,quote,schedule,tax,shared}/*_test.go` | Invariants: sale below list, non-overlapping windows, quote arithmetic, version rules, id and prefix validation | `make test` |
| Unit, application | `internal/application/usecases/*_test.go` | Each use case against the in-memory test doubles: outbox rows, inbox dedupe, retry on `concurrent-update`, quote metrics | `make test` |
| Unit, adapters | `internal/adapters/**/**_test.go`, `cmd/api/*_test.go`, `cmd/a2a/*_test.go` | HTTP routes and problem mapping (`httptest`), idempotency, readiness gate, consumer loop (retry, dead letter, tracing), CloudEvents encoder against golden files, outbox relay, telemetry, boot configuration, A2A skills and card | `make test` |
| Godog features (BDD) | `features/*.feature`, wiring in `internal/bdd/` | Behaviour through the real HTTP router on in-memory doubles | `make bdd` |
| Repository contract | `internal/adapters/outbound/repocontract/` | One suite run against both the in-memory store (`memory_test.go`) and Postgres (`repositories_integration_test.go`), so the doubles cannot drift from the real repositories | `make test`, `make integration` |
| Integration | 9 files tagged `integration` | Real Postgres and Kafka through testcontainers: repositories, migrations, outbox claim and relay, outbox trace headers, `cmd/api` wiring, readiness, an end-to-end test, and `cmd/a2a` on real Postgres | `make integration` |
| Fitness (architecture) | `internal/architecture/` | Structural rules as executable tests, below | `make arch-test` |
| Mutation | `.gremlins.yaml`, `make mutation` | Tests assert rather than merely execute, on `internal/domain/*` | `make mutation` |

### Godog features

Five feature files, 26 scenarios: `item_price.feature` (6), `sales.feature` (5),
`price_quote.feature` (5), `policies.feature` (5) and
`api_idempotency.feature` (5). The runner is `TestFeatures` in
`internal/bdd/features_test.go` (steps in `steps_given_test.go`,
`steps_when_test.go`, `steps_then_test.go`); there are no Go files at the
repository root.

### Contract tests

Three contracts are pinned in tests rather than prose:

- The HTTP surface against `apis/openapi.yaml` (Spectral lint in the `api-lint`
  job, route and problem tests in `internal/adapters/inbound/http`).
- The wire format of every published event: `internal/adapters/outbound/kafka`
  compares the CloudEvents bytes with `testdata/*.golden.json` exactly
  (`ItemPriceChanged`, `ItemPriceChangedAtListPrice`, `ItemPriceWithdrawn`,
  `DeliveryChargeScheduleChanged`, `TaxTableChanged`, `PriceQuoteIssued`);
  rewrite them with the `-update` flag only for a deliberate change.
- `TestEventCatalogueMatchesContract` checks the event types in code against
  `apis/asyncapi.yaml`.

### Fitness tests

`internal/architecture/` holds these tests (each sensor also has a "bites" test
proving it fails on a bad fixture):

| Test | Rule |
| --- | --- |
| `TestHexagonalArchitecture` (arch-go) | The domain imports no outer layer; adapters do not reach sideways into siblings |
| `TestRuntimeBinaryHasNoInMemoryAdapters` | `cmd/**` never imports the in-memory adapters |
| `TestNoGoFilesAtRepoRoot` | Go files live under `cmd/`, `internal/`, `features/` |
| `TestEventCatalogueMatchesContract` | Event catalogue equals `apis/asyncapi.yaml` |
| `TestCloudEventsOnly`, `TestNoEventEnvelopeToggleOrFlatEnvelope` | Every Kafka message is a CloudEvents 1.0 envelope; no toggle or flat envelope |
| `TestKafkaConsumerGroupNeverHardcodedInline` | Consumer groups come from the environment |
| `TestReplayConsumersSetCommitInterval` | Offsets are committed explicitly after handling |
| `TestKafkaWritersSetRequiredAcks`, `TestKafkaWriterTopicNotSetTwice` | Writers use `RequireAll`; topic is set on the writer or the message, never both |
| `TestDeadLetterCallsGuardCancellation` | Dead-letter publishes guard against shutdown cancellation |
| `TestOutboxRelaysPassAttemptCap` | Every `cmd/*` that builds a relay passes the attempt cap |
| `TestNoAuthMiddlewareReintroduced` | No auth middleware under `internal/adapters/inbound` |
| `TestPostgresIntegrationTestsUseTestcontainers`, `TestKafkaIntegrationTestsUseTestcontainers` | Integration tests boot their own containers, no `DATABASE_URL` gate or `t.Skip` |
| `TestA2AAdapterDependsOnlyInward`, `TestNothingDependsOnTheA2AAdapter`, `TestNoOutboundA2AClient`, `TestA2AAgentIsReadOnly`, `TestA2ABinaryHasNoKafkaOrRelay` | The A2A adapter reads only, calls no one, and `cmd/a2a` has no Kafka or relay |

The package also carries the template's MCP sensors
(`TestMCPAdapterDependencyRule`, `TestMCPOptionalToolInputsCarryOmitempty`);
Pricing has no MCP binary, so they have nothing to check here.

### Mutation testing

`.gremlins.yaml` sets one worker, a timeout coefficient of 30, an efficacy
threshold of 85 and a mutant-coverage threshold of 95. The thresholds sit just
below the lowest measured package (quote, 87.36 on 2026-10-25; itemprice 95.35,
schedule 95.24, shared 100.00, tax 92.98). `make mutation` loops gremlins over
every directory under `internal/domain/` (so a new domain package is gated as
soon as it exists); `make mutation-full` runs `./internal/domain` in one go.

## Make targets

| Target | What it runs |
| --- | --- |
| `make build` | `go build ./...` |
| `make vet` | `go vet ./...` |
| `make fmt` / `make fmt-check` | `gofmt -w .` / fail if `gofmt -l .` is non-empty |
| `make lint` | `golangci-lint run --allow-parallel-runners ./...` (v2.14.0) |
| `make test` | `go test ./... -race` (unit, `httptest`, godog; no database) |
| `make coverage` | `go test ./... -race -coverprofile=coverage.out -coverpkg=./internal/domain/...,./internal/application/...`, then the 90% gate |
| `make bdd` | `go test ./... -run TestFeatures -v` |
| `make arch-test` | `go test ./internal/architecture/... -v` |
| `make integration` | `go test -tags=integration ./... -race -count=1` (Docker only; not part of `check`) |
| `make mutation` / `make mutation-full` | gremlins per domain package / on `./internal/domain` |
| `make vuln` | `govulncheck ./...` |
| `make check` | `fmt-check vet build lint test` (the pre-push hook) |
| `make check-all` | `check` + `coverage` + `arch-test` + `bdd` |
| `make check-fast` | `fmt-check vet arch-test` and the tests of changed packages (agent Stop hook) |
| `make guide-lint` / `make harness-test` | Agent-guide lint / unit tests of the agent hooks |

Hooks: `lefthook.yml` runs `fmt-check`, `vet`, `lint` at pre-commit and
`make check` at pre-push.

## CI jobs

`.github/workflows/ci.yml` runs on pushes to `main` and `develop`, on pull
requests to them, weekly (Monday 06:00 UTC, the mutation job only) and on
manual dispatch.

| Job | What it does |
| --- | --- |
| `lint` | golangci-lint v2.14.0 |
| `guide-lint` | `scripts/harness/guide_lint.py` and `test_hook.py`; advisory (`continue-on-error`) |
| `complexity` | golangci-lint restricted to `gocyclo,gocognit,cyclop,funlen,nestif`, plus an informational gocyclo report |
| `test` | `go build`, `go vet`, then `make coverage` (90% gate over domain and application) |
| `bdd` | `go test ./... -run TestFeatures -v` |
| `integration` | `go test -tags=integration ./... -race -count=1` (testcontainers) |
| `mutation-fast` | gremlins over every `internal/domain/*` package; blocking |
| `mutation` | exhaustive gremlins on `./internal/domain`; only on schedule and manual dispatch, opens or closes a `harness:red` issue |
| `api-lint` | Spectral on `apis/openapi.yaml` and `apis/asyncapi.yaml`, `--fail-severity=warn` |
| `vuln` | `govulncheck ./...` |
| `helm-lint` | pull requests only: `ct lint` and `helm lint` with each `ci/*-values.yaml` |
| `chart-tests` | every `charts/pricing/tests/*.sh` and `test_*.py`; a test that prints `SKIP` fails |
| `arch-test` | `go test ./internal/architecture/... -v` |
| `docker-publish` | push to `main` only, after the jobs above except the chart ones: builds and pushes `ghcr.io/iqvo/pricing`, cosign-signs it, attaches an SBOM |
| `release` | push to `main` only: tags `vX.Y.Z`, tags the image, packages and pushes the chart, creates the GitHub release |

The documentation site has its own workflow, `.github/workflows/docs.yml`
(`Docs`): it builds on pull requests and deploys on pushes to `main`. This
repository's `ci.yml` has no `docs-api-drift` job; regenerate the REST pages
with `npm run gen-api-docs` after a change to `apis/openapi.yaml` and commit the
result.
