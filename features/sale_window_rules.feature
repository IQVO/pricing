# sale_window_rules.feature — wave 3: sale windows and the boundary policy
# (aggregate-design-canvas ItemPrice invariants and PublishEffectivePriceOnWindowBoundary,
# use-cases ScheduleSale / CancelSale / boundary publication, ADR 0002 A4 and A6,
# operations troubleshooting). The clock starts at 2026-10-24T09:00:00Z.
Feature: Sale windows never overlap, are bounded, and publish at their boundaries
  A window is live from its start instant and over at its end instant. The
  boundary policy publishes the effective price once per boundary; a price
  that was withdrawn or a sale that was cancelled publishes nothing.

  Background:
    Given the item "sku-1" is priced at 1000

  Scenario: A sale one minor unit below the list price is allowed
    When the API posts to "/item-prices/sku-1/sales" the body:
      """
      {"price":{"amountMinor":999,"currency":"BRL"},"from":"2026-11-01T00:00:00Z","to":"2026-11-02T00:00:00Z"}
      """
    Then the API answers 201
    And the response field "price.amountMinor" is 999

  Scenario Outline: Windows that only touch or sit apart do not overlap
    Given the API accepts a POST to "/item-prices/sku-1/sales" the body:
      """
      {"price":{"amountMinor":800,"currency":"BRL"},"from":"2026-10-27T00:00:00Z","to":"2026-10-29T00:00:00Z"}
      """
    When the API posts to "/item-prices/sku-1/sales" the body:
      """
      {"price":{"amountMinor":700,"currency":"BRL"},"from":"<from>","to":"<to>"}
      """
    Then the API answers 201

    Examples: the new window ends exactly when the sale starts, or starts exactly when it ends
      | from                 | to                   |
      | 2026-10-25T00:00:00Z | 2026-10-27T00:00:00Z |
      | 2026-10-29T00:00:00Z | 2026-10-30T00:00:00Z |

  Scenario Outline: Windows that share any instant overlap
    Given the API accepts a POST to "/item-prices/sku-1/sales" the body:
      """
      {"price":{"amountMinor":800,"currency":"BRL"},"from":"2026-10-27T00:00:00Z","to":"2026-10-29T00:00:00Z"}
      """
    When the API posts to "/item-prices/sku-1/sales" the body:
      """
      {"price":{"amountMinor":700,"currency":"BRL"},"from":"<from>","to":"<to>"}
      """
    Then the API answers 409 with problem "overlapping-sale-window"
    And 1 "ItemPriceChanged" events were published

    Examples: the new window starts before, ends after, sits inside or covers the sale
      | from                 | to                   |
      | 2026-10-25T00:00:00Z | 2026-10-28T00:00:00Z |
      | 2026-10-28T00:00:00Z | 2026-10-30T00:00:00Z |
      | 2026-10-27T12:00:00Z | 2026-10-28T00:00:00Z |
      | 2026-10-26T00:00:00Z | 2026-10-30T00:00:00Z |

  Scenario Outline: A sale window must start before it ends and end in the future
    When the API posts to "/item-prices/sku-1/sales" the body:
      """
      {"price":{"amountMinor":800,"currency":"BRL"},"from":"<from>","to":"<to>"}
      """
    Then the API answers 422 with problem "validation-failed"

    Examples: ended in the past, ends exactly now, has no length, ends before it starts
      | from                 | to                   |
      | 2026-10-22T00:00:00Z | 2026-10-23T00:00:00Z |
      | 2026-10-24T08:00:00Z | 2026-10-24T09:00:00Z |
      | 2026-10-25T00:00:00Z | 2026-10-25T00:00:00Z |
      | 2026-10-26T00:00:00Z | 2026-10-25T00:00:00Z |

  Scenario: A window that ends one second from now is still accepted and is live
    When the API posts to "/item-prices/sku-1/sales" the body:
      """
      {"price":{"amountMinor":800,"currency":"BRL"},"from":"2026-10-24T08:00:00Z","to":"2026-10-24T09:00:01Z"}
      """
    Then the API answers 201
    When the API gets "/item-prices/sku-1"
    Then the response field "effectivePrice.amountMinor" is 800

  Scenario: An item holds at most 50 sale windows
    Given the item "sku-1" has 50 future one-day sales priced at 900
    When the API schedules a one-day sale for "sku-1" priced at 900 on day 51
    Then the API answers 422 with problem "validation-failed"
    When the API gets "/item-prices/sku-1"
    Then the response list "sales" has 50 items

  Scenario: A future sale changes nothing until its window opens
    When the API posts to "/item-prices/sku-1/sales" the body:
      """
      {"price":{"amountMinor":800,"currency":"BRL"},"from":"2026-10-24T10:00:00Z","to":"2026-10-24T20:00:00Z"}
      """
    Then the API answers 201
    And 1 "ItemPriceChanged" events were published
    When the API gets "/item-prices/sku-1"
    Then the response field "effectivePrice.amountMinor" is 1000
    And the response list "sales" has 1 items

  Scenario: A sale is live from its start instant and over at its end instant
    Given the API accepts a POST to "/item-prices/sku-1/sales" the body:
      """
      {"price":{"amountMinor":800,"currency":"BRL"},"from":"2026-10-24T10:00:00Z","to":"2026-10-24T20:00:00Z"}
      """
    When 59 minutes pass
    And the policies run
    And the API gets "/item-prices/sku-1"
    Then the response field "effectivePrice.amountMinor" is 1000
    And 1 "ItemPriceChanged" events were published
    When 1 minutes pass
    And the policies run
    And the API gets "/item-prices/sku-1"
    Then the response field "effectivePrice.amountMinor" is 800
    And 2 "ItemPriceChanged" events were published
    And the latest "ItemPriceChanged" event has list price 1000 and effective price 800
    And the latest "ItemPriceChanged" event has its sale ending at "2026-10-24T20:00:00Z"
    When 599 minutes pass
    And the policies run
    And the API gets "/item-prices/sku-1"
    Then the response field "effectivePrice.amountMinor" is 800
    And 2 "ItemPriceChanged" events were published
    When 1 minutes pass
    And the API gets "/item-prices/sku-1"
    Then the response field "effectivePrice.amountMinor" is 1000
    When the policies run
    Then 3 "ItemPriceChanged" events were published
    And the latest "ItemPriceChanged" event has list price 1000 and effective price 1000
    And the latest "ItemPriceChanged" event has no sale end

  Scenario: The boundary policy publishes a boundary once however often it runs
    Given the API accepts a POST to "/item-prices/sku-1/sales" the body:
      """
      {"price":{"amountMinor":800,"currency":"BRL"},"from":"2026-10-24T10:00:00Z","to":"2026-10-24T20:00:00Z"}
      """
    When 30 minutes pass
    And the policies run
    Then 1 "ItemPriceChanged" events were published
    When 30 minutes pass
    And the policies run
    And the policies run
    And the policies run
    Then 2 "ItemPriceChanged" events were published

  Scenario: A sale cancelled before it opens publishes nothing at its boundary
    Given the API accepts a POST to "/item-prices/sku-1/sales" the body:
      """
      {"price":{"amountMinor":800,"currency":"BRL"},"from":"2026-10-24T10:00:00Z","to":"2026-10-24T20:00:00Z"}
      """
    When the API posts to "/item-prices/sku-1/sales/sale-1/cancellations" with no body
    Then the API answers 200
    And 1 "ItemPriceChanged" events were published
    When 720 minutes pass
    And the policies run
    And the API gets "/item-prices/sku-1"
    Then the response field "effectivePrice.amountMinor" is 1000
    And the response list "sales" has 0 items
    And 1 "ItemPriceChanged" events were published

  Scenario: A withdrawn price publishes nothing when its sale ends
    Given the API accepts a POST to "/item-prices/sku-1/sales" the body:
      """
      {"price":{"amountMinor":800,"currency":"BRL"},"from":"2026-10-24T08:00:00Z","to":"2026-10-24T10:00:00Z"}
      """
    When the API posts to "/item-prices/sku-1/withdrawals" with no body
    Then the API answers 200
    When 61 minutes pass
    And the policies run
    Then 2 "ItemPriceChanged" events were published
    And 1 "ItemPriceWithdrawn" events were published

  Scenario: Cancelling a sale that does not exist is not-found
    When the API posts to "/item-prices/sku-1/sales/sale-99/cancellations" with no body
    Then the API answers 404 with problem "not-found"

  Scenario: A cancellation replays under its key and a second cancellation under a new key is not-found
    Given the API accepts a POST to "/item-prices/sku-1/sales" the body:
      """
      {"price":{"amountMinor":800,"currency":"BRL"},"from":"2026-10-24T08:00:00Z","to":"2026-10-25T08:00:00Z"}
      """
    When the API posts to "/item-prices/sku-1/sales/sale-1/cancellations" with Idempotency-Key "cancel-1" the body:
      """
      {}
      """
    Then the API answers 200
    When the API posts to "/item-prices/sku-1/sales/sale-1/cancellations" with Idempotency-Key "cancel-1" the body:
      """
      {}
      """
    Then the API answers 200
    And the response is marked replayed
    And 3 "ItemPriceChanged" events were published
    When the API posts to "/item-prices/sku-1/sales/sale-1/cancellations" with no body
    Then the API answers 404 with problem "not-found"

  Scenario: Every price movement carries the list price, the effective price and a rising version
    Given the API accepts a POST to "/item-prices/sku-1/sales" the body:
      """
      {"price":{"amountMinor":800,"currency":"BRL"},"from":"2026-10-24T08:00:00Z","to":"2026-10-25T08:00:00Z"}
      """
    Then the latest "ItemPriceChanged" event has list price 1000 and effective price 800
    And the latest "ItemPriceChanged" event has its sale ending at "2026-10-25T08:00:00Z"
    And the latest "ItemPriceChanged" event carries version 2 and is keyed by "sku-1"
    When the API posts to "/item-prices/sku-1/sales/sale-1/cancellations" with no body
    Then the latest "ItemPriceChanged" event has list price 1000 and effective price 1000
    And the latest "ItemPriceChanged" event has no sale end
    And the "ItemPriceChanged" events carry versions "1,2,3"
