# readiness_rules.feature — wave 3: /readyz against /healthz (operations
# troubleshooting "Readiness and liveness"). The harness now wires the same
# Readiness gate cmd/api builds, with Postgres and Kafka checks it can fail.
Feature: Readiness follows the dependencies and the drain gate while liveness stays up
  Scenario: A dependency that does not answer makes the service not ready but keeps it live
    Given Kafka is unreachable
    When the API gets "/readyz"
    Then the API answers 503 with problem "dependency-unavailable"
    And the response field "detail" is "not ready: kafka"
    When the API gets "/healthz"
    Then the API answers 200
    And the response field "status" is "ok"

  Scenario: Every dependency that does not answer is named and the service recovers by itself
    Given Postgres is unreachable
    And Kafka is unreachable
    When the API gets "/readyz"
    Then the API answers 503 with problem "dependency-unavailable"
    And the response field "detail" is "not ready: postgres, kafka"
    Given Postgres answers again
    And Kafka answers again
    When the API gets "/readyz"
    Then the API answers 200
    And the response field "status" is "ready"

  Scenario: Shutdown flips readiness first and liveness stays up
    Given shutdown has started
    When the API gets "/readyz"
    Then the API answers 503
    And the response field "status" is "not_ready"
    When the API gets "/healthz"
    Then the API answers 200
    And the response field "status" is "ok"
