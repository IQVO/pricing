# charge_schedule_rules.feature — wave 3: DeliveryChargeSchedule rules from the
# documentation (aggregate-design-canvas, use-cases PublishChargeSchedule,
# domain-events, ADR 0002 A7 and A18, troubleshooting).
Feature: A delivery charge schedule names every tier and supersedes, never edits
  Exactly one schedule is CURRENT; a new version must be higher and moves the
  previous one to SUPERSEDED in the same transaction, keeping the history.

  Scenario: A new version supersedes the current schedule and the history is kept
    Given every delivery tier charges 500 from version 1
    And every delivery tier charges 700 from version 2
    When the API gets "/delivery-charge-schedules/current"
    Then the response field "version" is 2
    And the response field "state" is "CURRENT"
    And the response field "tiers.0.charge.amountMinor" is 700
    And the store holds 2 schedules
    And 2 "DeliveryChargeScheduleChanged" events were published
    And the latest "DeliveryChargeScheduleChanged" event carries version 2 and is keyed by "schedule-2"

  Scenario: A version below the current one is refused and the current schedule is untouched
    Given every delivery tier charges 500 from version 3
    When the API publishes a charge schedule of version 2 naming the tiers "SAME_DAY, NEXT_DAY, TWO_DAY, STANDARD"
    Then the API answers 409 with problem "non-increasing-version"
    When the API gets "/delivery-charge-schedules/current"
    Then the response field "version" is 3
    And the response field "state" is "CURRENT"
    And the store holds 1 schedules
    And 1 "DeliveryChargeScheduleChanged" events were published

  Scenario: A version only has to exceed the current one, it need not follow it
    Given every delivery tier charges 500 from version 3
    When the API publishes a charge schedule of version 4 naming the tiers "SAME_DAY, NEXT_DAY, TWO_DAY, STANDARD"
    Then the API answers 201
    When the API publishes a charge schedule of version 9 naming the tiers "SAME_DAY, NEXT_DAY, TWO_DAY, STANDARD"
    Then the API answers 201
    And the response field "version" is 9

  Scenario Outline: A schedule version is between 1 and 2147483647
    When the API publishes a charge schedule of version <version> naming the tiers "SAME_DAY, NEXT_DAY, TWO_DAY, STANDARD"
    Then the API answers <status>

    Examples:
      | version    | status |
      | 0          | 422    |
      | 1          | 201    |
      | 2147483647 | 201    |

  Scenario: A version above the integer ceiling is refused as invalid
    When the API publishes a charge schedule of version 2147483648 naming the tiers "SAME_DAY, NEXT_DAY, TWO_DAY, STANDARD"
    Then the API answers 422 with problem "validation-failed"

  Scenario Outline: A schedule names each of the four tiers exactly once
    When the API publishes a charge schedule of version 1 naming the tiers "<tiers>"
    Then the API answers 422 with problem "<problem>"
    And the store holds 0 schedules
    And 0 "DeliveryChargeScheduleChanged" events were published

    Examples: a tier missing, none at all, a tier twice, a tier that does not exist
      | tiers                                           | problem                     |
      | SAME_DAY, NEXT_DAY, TWO_DAY                     | incomplete-charge-schedule  |
      |                                                 | incomplete-charge-schedule  |
      | SAME_DAY, NEXT_DAY, TWO_DAY, STANDARD, STANDARD | validation-failed           |
      | SAME_DAY, NEXT_DAY, TWO_DAY, STANDARD, OVERNIGHT | validation-failed          |

  Scenario: Every amount of a schedule is non-negative and in the configured currency
    When the API posts to "/delivery-charge-schedules" the body:
      """
      {"version":1,"tiers":[
        {"tier":"SAME_DAY","charge":{"amountMinor":-1,"currency":"BRL"}},
        {"tier":"NEXT_DAY","charge":{"amountMinor":1,"currency":"BRL"}},
        {"tier":"TWO_DAY","charge":{"amountMinor":1,"currency":"BRL"}},
        {"tier":"STANDARD","charge":{"amountMinor":1,"currency":"BRL"}}]}
      """
    Then the API answers 422 with problem "negative-amount"
    When the API posts to "/delivery-charge-schedules" the body:
      """
      {"version":1,"tiers":[
        {"tier":"SAME_DAY","charge":{"amountMinor":1,"currency":"USD"}},
        {"tier":"NEXT_DAY","charge":{"amountMinor":1,"currency":"BRL"}},
        {"tier":"TWO_DAY","charge":{"amountMinor":1,"currency":"BRL"}},
        {"tier":"STANDARD","charge":{"amountMinor":1,"currency":"BRL"}}]}
      """
    Then the API answers 422 with problem "currency-mismatch"
    When the API posts to "/delivery-charge-schedules" the body:
      """
      {"version":1,"tiers":[
        {"tier":"SAME_DAY","charge":{"amountMinor":1,"currency":"BRL"},"freeOver":{"amountMinor":5000,"currency":"USD"}},
        {"tier":"NEXT_DAY","charge":{"amountMinor":1,"currency":"BRL"}},
        {"tier":"TWO_DAY","charge":{"amountMinor":1,"currency":"BRL"}},
        {"tier":"STANDARD","charge":{"amountMinor":1,"currency":"BRL"}}]}
      """
    Then the API answers 422 with problem "currency-mismatch"
    And the store holds 0 schedules

  Scenario: Tiers are served in speed order with their free-over thresholds
    When the API posts to "/delivery-charge-schedules" the body:
      """
      {"version":1,"tiers":[
        {"tier":"STANDARD","charge":{"amountMinor":590,"currency":"BRL"},"freeOver":{"amountMinor":10000,"currency":"BRL"}},
        {"tier":"TWO_DAY","charge":{"amountMinor":990,"currency":"BRL"}},
        {"tier":"NEXT_DAY","charge":{"amountMinor":1490,"currency":"BRL"},"freeOver":{"amountMinor":20000,"currency":"BRL"}},
        {"tier":"SAME_DAY","charge":{"amountMinor":2490,"currency":"BRL"}}]}
      """
    Then the API answers 201
    When the API gets "/delivery-charge-schedules/current"
    Then the response field "tiers.0.tier" is "SAME_DAY"
    And the response field "tiers.1.tier" is "NEXT_DAY"
    And the response field "tiers.1.freeOver.amountMinor" is 20000
    And the response field "tiers.2.tier" is "TWO_DAY"
    And the response has no field "tiers.2.freeOver"
    And the response field "tiers.3.tier" is "STANDARD"
    And the response field "tiers.3.freeOver.amountMinor" is 10000

  Scenario: A replayed publication does nothing twice and the same version under a new key is refused
    When the API posts to "/delivery-charge-schedules" with Idempotency-Key "pub-1" the body:
      """
      {"version":1,"tiers":[
        {"tier":"SAME_DAY","charge":{"amountMinor":1,"currency":"BRL"}},
        {"tier":"NEXT_DAY","charge":{"amountMinor":1,"currency":"BRL"}},
        {"tier":"TWO_DAY","charge":{"amountMinor":1,"currency":"BRL"}},
        {"tier":"STANDARD","charge":{"amountMinor":1,"currency":"BRL"}}]}
      """
    Then the API answers 201
    When the API posts to "/delivery-charge-schedules" with Idempotency-Key "pub-1" the body:
      """
      {"version":1,"tiers":[
        {"tier":"SAME_DAY","charge":{"amountMinor":1,"currency":"BRL"}},
        {"tier":"NEXT_DAY","charge":{"amountMinor":1,"currency":"BRL"}},
        {"tier":"TWO_DAY","charge":{"amountMinor":1,"currency":"BRL"}},
        {"tier":"STANDARD","charge":{"amountMinor":1,"currency":"BRL"}}]}
      """
    Then the API answers 201
    And the response is marked replayed
    And 1 "DeliveryChargeScheduleChanged" events were published
    When the API posts to "/delivery-charge-schedules" with Idempotency-Key "pub-2" the body:
      """
      {"version":1,"tiers":[
        {"tier":"SAME_DAY","charge":{"amountMinor":1,"currency":"BRL"}},
        {"tier":"NEXT_DAY","charge":{"amountMinor":1,"currency":"BRL"}},
        {"tier":"TWO_DAY","charge":{"amountMinor":1,"currency":"BRL"}},
        {"tier":"STANDARD","charge":{"amountMinor":1,"currency":"BRL"}}]}
      """
    Then the API answers 409 with problem "non-increasing-version"
