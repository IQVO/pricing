Feature: Sale windows lower the effective price for a period
  A sale price is always below the list price and the windows of one item never
  overlap. A sale that starts or ends publishes ItemPriceChanged at its boundary;
  cancelling a live sale does too, because it has no event of its own.

  Background:
    Given the item "sku-1" is priced at 1000

  Scenario: A sale window that is open now lowers the price
    When the API posts to "/item-prices/sku-1/sales" the body:
      """
      {"price":{"amountMinor":800,"currency":"BRL"},"from":"2026-10-24T08:00:00Z","to":"2026-10-25T08:00:00Z"}
      """
    Then the API answers 201
    When the API gets "/item-prices/sku-1"
    Then the response field "effectivePrice.amountMinor" is 800
    And 2 "ItemPriceChanged" events were published

  Scenario: A sale not below the list price is refused
    When the API posts to "/item-prices/sku-1/sales" the body:
      """
      {"price":{"amountMinor":1000,"currency":"BRL"},"from":"2026-10-24T08:00:00Z","to":"2026-10-25T08:00:00Z"}
      """
    Then the API answers 422 with problem "sale-not-below-list"

  Scenario: Overlapping windows are refused
    Given the API posts to "/item-prices/sku-1/sales" the body:
      """
      {"price":{"amountMinor":800,"currency":"BRL"},"from":"2026-10-26T00:00:00Z","to":"2026-10-28T00:00:00Z"}
      """
    When the API posts to "/item-prices/sku-1/sales" the body:
      """
      {"price":{"amountMinor":700,"currency":"BRL"},"from":"2026-10-27T00:00:00Z","to":"2026-10-29T00:00:00Z"}
      """
    Then the API answers 409 with problem "overlapping-sale-window"

  Scenario: A future sale publishes its price when the window opens
    Given the API posts to "/item-prices/sku-1/sales" the body:
      """
      {"price":{"amountMinor":800,"currency":"BRL"},"from":"2026-10-24T10:00:00Z","to":"2026-10-24T20:00:00Z"}
      """
    And 61 minutes pass
    When the policies run
    And the API gets "/item-prices/sku-1"
    Then the response field "effectivePrice.amountMinor" is 800
    And 2 "ItemPriceChanged" events were published

  Scenario: Cancelling a live sale restores the list price and publishes it
    Given the API posts to "/item-prices/sku-1/sales" the body:
      """
      {"price":{"amountMinor":800,"currency":"BRL"},"from":"2026-10-24T08:00:00Z","to":"2026-10-25T08:00:00Z"}
      """
    And I remember the response field "saleId" as "sale"
    When the API posts to "/item-prices/sku-1/sales/sale-1/cancellations" with no body
    Then the API answers 200
    And the response field "effectivePrice.amountMinor" is 1000
    And 3 "ItemPriceChanged" events were published
